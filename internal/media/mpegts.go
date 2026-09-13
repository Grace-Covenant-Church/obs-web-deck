package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/asticode/go-astits"
	"github.com/pion/webrtc/v4"
	pmedia "github.com/pion/webrtc/v4/pkg/media"
)

/*
MPEG-TS ingest.

OBS's Stream output can only go to one place, so when the Stream slot is
committed to a real destination (YouTube), the preview has to come out of the
Recording slot instead. That slot is a Custom Output (FFmpeg), which handles
`mpegts` reliably - unlike the WHIP muxer, which OBS's FFmpeg wrapper cannot
drive.

MPEG-TS can carry H264 and Opus, which are exactly the two codecs WebRTC
wants, so frames are unwrapped and handed to Pion without any transcoding.
*/

// PTS and DTS in MPEG-TS tick at 90kHz.
const ptsClock = 90000

// opusSyncByte is the first byte of the 11-bit 0x3FF prefix on every
// Opus-in-MPEG-TS control header. 0x3FF as 11 bits is 0111 1111 111, so the
// leading byte is 0x7F rather than the 0xFF the value suggests at a glance.
const opusSyncByte = 0x7F

// opusRegistration is the "Opus" format identifier carried in the PMT
// registration descriptor, which is how an Opus stream is told apart from any
// other private-data stream.
const opusRegistration = 0x4F707573 // 'O','p','u','s'

// Frame durations are derived from PTS deltas. These bound the result so a
// discontinuity or a stream restart cannot emit an absurd duration.
const (
	minFrameDur = time.Millisecond
	maxFrameDur = 500 * time.Millisecond

	defaultVideoDur = time.Second / 30
	defaultAudioDur = 20 * time.Millisecond
)

/*
Realtime guard.

MPEG-TS over UDP has no back channel, so nothing upstream slows down when this
process does. A stall - a CPU-starved machine, a scheduler hiccup, a burst of
datagrams behind a keyframe - leaves a backlog queued in the reader, and
draining it normally replays every buffered frame in order. The preview then
stays exactly as late as the worst stall was, for the rest of the session.

That is the wrong trade for a control surface: an operator would rather lose a
second of video than watch a second-old program feed while pressing buttons.
So the ingest measures how far the media timeline has slipped behind the wall
clock and, past maxLag, discards frames until it has caught up.

H264 is only decodable from an IDR onwards, so a partial GOP cannot be
dropped; the unit of catch-up is everything up to the next keyframe. Opus
access units are independent, so audio is dropped one unit at a time.
*/
const (
	// DefaultMaxLag is the slip past which frames start being dropped. It sits
	// above a phone's jitter buffer (50-150 ms) so ordinary network variation
	// never trips it, and well below the point where a preview stops being
	// usable for cueing.
	DefaultMaxLag = 400 * time.Millisecond

	// baseWindow bounds how far back the on-time baseline looks. Without a
	// window, a source clock running slightly slow against ours would look
	// like ever-growing lag and eventually drop every GOP forever. With one,
	// a slip that cannot be drained is accepted as the new normal after
	// 10-20 seconds instead.
	baseWindow = 10 * time.Second
)

// StartMPEGTS listens for an MPEG-TS stream pushed over UDP and republishes it
// on the given path. It returns once the socket is open; ingest runs until ctx
// is cancelled.
//
// maxLag is how far the preview may fall behind wall clock before frames are
// dropped to catch up; zero disables the guard and forwards everything that
// arrives, however late.
func (s *Server) StartMPEGTS(ctx context.Context, addr, path string, maxLag time.Duration) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	// A 4 MB kernel buffer absorbs the bursts a keyframe produces; the default
	// is small enough that a 1080p IDR can overrun it and shred the frame.
	_ = conn.SetReadBuffer(4 << 20)

	if maxLag > 0 {
		log.Printf("[media] MPEG-TS listening on udp://%s -> /%s (drop frames past %v behind)",
			addr, path, maxLag)
	} else {
		log.Printf("[media] MPEG-TS listening on udp://%s -> /%s (realtime guard off)", addr, path)
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	go s.ingestLoop(ctx, conn, path, maxLag)
	return nil
}

// ingestLoop keeps demuxing, restarting after an error so that stopping and
// restarting the OBS recording reconnects rather than needing a bounce.
func (s *Server) ingestLoop(ctx context.Context, conn *net.UDPConn, path string, maxLag time.Duration) {
	for ctx.Err() == nil {
		if err := s.ingest(ctx, conn, path, maxLag); err != nil && ctx.Err() == nil {
			log.Printf("[media] MPEG-TS ingest ended: %v", err)
		}
		s.clearRoom(path)

		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Server) clearRoom(path string) {
	rm := s.roomFor(path)
	rm.mu.Lock()
	had := rm.video != nil
	rm.video, rm.audio = nil, nil
	rm.mu.Unlock()
	if had {
		log.Printf("[media] %s MPEG-TS publisher gone", path)
	}
}

func (s *Server) ingest(ctx context.Context, conn *net.UDPConn, path string, maxLag time.Duration) error {
	reader := newDatagramReader(ctx, conn)
	defer reader.Close()
	// Each attempt starts a new demuxer, so alignment is found again.

	dmx := astits.NewDemuxer(ctx, reader)
	rm := s.roomFor(path)

	var (
		videoPID, audioPID uint16
		video, audio       *webrtc.TrackLocalStaticSample
		lastVideoPTS       int64 = -1
		lastAudioPTS       int64 = -1
		warnedCodec              = map[astits.StreamType]bool{}
		loggedPMT          bool

		// One tracker per elementary stream: the two PIDs share a clock but
		// not an arrival pattern, and only video has to wait for a keyframe
		// before it can resume.
		videoLag, audioLag lagTracker
		gate               = videoGate{max: maxLag}
	)

	for {
		d, err := dmx.NextData()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}

		// The PMT names the elementary streams. It repeats, so this also
		// re-syncs if OBS restarts with a different layout.
		if d.PMT != nil {
			if !loggedPMT {
				loggedPMT = true
				for _, es := range d.PMT.ElementaryStreams {
					log.Printf("[media] %s: PMT pid=%d stream_type=0x%02X",
						path, es.ElementaryPID, byte(es.StreamType))
				}
			}
			for _, es := range d.PMT.ElementaryStreams {
				switch es.StreamType {
				case astits.StreamTypeH264Video:
					videoPID = es.ElementaryPID

				case astits.StreamTypePrivateData:
					// Private data covers several codecs, so only claim this
					// PID when the registration descriptor actually says Opus.
					if isOpusStream(es) {
						audioPID = es.ElementaryPID
					}

				// The remaining cases are all "this codec cannot cross
				// WebRTC". They are reported rather than ignored, because a
				// silent black preview gives no clue that the encoder, not
				// the plumbing, is the problem. OBS's stock defaults for a
				// custom FFmpeg output land here.
				default:
					if !warnedCodec[es.StreamType] {
						warnedCodec[es.StreamType] = true
						log.Printf("[media] %s: %s, which WebRTC cannot carry. %s",
							path, describeStream(es.StreamType), fixFor(es.StreamType))
					}
				}
			}
			continue
		}

		if d.PES == nil || d.PES.Header == nil {
			continue
		}
		pts := ptsOf(d.PES)

		switch d.PID {
		case videoPID:
			if video == nil {
				video, err = webrtc.NewTrackLocalStaticSample(
					webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
					"video", "obs-deck")
				if err != nil {
					return err
				}
				rm.mu.Lock()
				rm.video = video
				rm.mu.Unlock()
				log.Printf("[media] %s publishing video (MPEG-TS H264)", path)
			}

			lag := videoLag.observe(pts, time.Now())
			catching := gate.active
			if gate.drop(lag, d.PES.Data) {
				if !catching {
					log.Printf("[media] %s is %v behind, dropping to the next keyframe",
						path, lag.Round(time.Millisecond))
				}
				rm.dropped.Add(1)
				continue
			}
			if catching {
				log.Printf("[media] %s caught up, %d frames dropped", path, gate.dropped)
			}

			dur := frameDuration(pts, &lastVideoPTS, defaultVideoDur)
			// The PES payload is Annex-B, which is what Pion's H264 payloader
			// expects, so the bytes go straight through.
			if err := video.WriteSample(pmedia.Sample{Data: d.PES.Data, Duration: dur}); err != nil {
				return err
			}

		case audioPID:
			units, ok := splitOpusUnits(d.PES.Data)
			if !ok {
				continue // not Opus after all; ignore rather than emit noise
			}
			if audio == nil {
				audio, err = webrtc.NewTrackLocalStaticSample(
					webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
					"audio", "obs-deck")
				if err != nil {
					return err
				}
				rm.mu.Lock()
				rm.audio = audio
				rm.mu.Unlock()
				log.Printf("[media] %s publishing audio (MPEG-TS Opus)", path)
			}
			// Opus units decode independently, so late audio needs no
			// equivalent of a keyframe - dropping one unit is enough.
			if lag := audioLag.observe(pts, time.Now()); maxLag > 0 && lag > maxLag {
				rm.dropped.Add(int64(len(units)))
				continue
			}

			dur := frameDuration(pts, &lastAudioPTS, defaultAudioDur)
			// A PES packet can hold several access units; split the measured
			// span across them rather than repeating it.
			each := dur / time.Duration(len(units))
			if each <= 0 {
				each = defaultAudioDur
			}
			for _, u := range units {
				if err := audio.WriteSample(pmedia.Sample{Data: u, Duration: each}); err != nil {
					return err
				}
			}
		}
	}
}

// describeStream names a stream type in terms an operator can act on.
func describeStream(t astits.StreamType) string {
	switch t {
	case astits.StreamTypeMPEG2Video:
		return "video is MPEG-2"
	case astits.StreamTypeMPEG4Video:
		return "video is MPEG-4 part 2"
	case astits.StreamTypeHEVCVideo:
		return "video is HEVC"
	case astits.StreamTypeAACAudio:
		return "audio is AAC"
	case astits.StreamTypeMPEG1Audio, astits.StreamTypeMPEG2Audio:
		return "audio is MPEG audio (mp2/mp3)"
	case astits.StreamTypeAC3Audio, astits.StreamTypeEAC3Audio:
		return "audio is AC-3"
	case astits.StreamTypePrivateData:
		return "a private-data stream is present that is not Opus"
	}
	return fmt.Sprintf("stream type 0x%02X is present", byte(t))
}

func fixFor(t astits.StreamType) string {
	switch t {
	case astits.StreamTypeMPEG2Video, astits.StreamTypeMPEG4Video, astits.StreamTypeHEVCVideo:
		return "Set the recording video encoder to libx264 or h264_nvenc."
	}
	return "Set the recording audio encoder to libopus."
}

func isOpusStream(es *astits.PMTElementaryStream) bool {
	for _, d := range es.ElementaryStreamDescriptors {
		if d.Registration != nil && d.Registration.FormatIdentifier == opusRegistration {
			return true
		}
	}
	return false
}

func ptsOf(pes *astits.PESData) int64 {
	if pes.Header.OptionalHeader == nil || pes.Header.OptionalHeader.PTS == nil {
		return -1
	}
	return pes.Header.OptionalHeader.PTS.Base
}

/*
videoGate decides which late video frames to discard.

Two rules, and the second is the one that makes it converge. A frame is
dropped while the stream is behind, and having started, dropping continues
until a decodable entry point arrives - so a partial GOP is never forwarded to
a decoder that cannot use it. And the entry point itself is only taken once
the lag is back inside budget, which means a backlog deeper than one GOP is
discarded a GOP at a time rather than declared solved at the first keyframe.
*/
type videoGate struct {
	max     time.Duration // zero disables the gate entirely
	active  bool
	dropped int // frames discarded during the catch-up in progress
}

// drop reports whether this access unit should be discarded. The payload is
// scanned for a keyframe only once a catch-up is under way and the lag is back
// inside budget, so the common path never touches the frame bytes.
func (g *videoGate) drop(lag time.Duration, au []byte) bool {
	if g.max <= 0 {
		return false
	}
	behind := lag > g.max
	if behind && !g.active {
		g.active, g.dropped = true, 0
	}
	if !g.active {
		return false
	}
	if behind || !containsIDR(au) {
		g.dropped++
		return true
	}
	g.active = false
	return false
}

/*
lagTracker converts PTS timestamps into how far behind wall clock a stream has
fallen.

The raw offset between the encoder's clock and ours means nothing on its own:
it carries the encoder's own pipeline delay and an arbitrary PTS origin. What
matters is the offset relative to the best one recently seen - the frame that
arrived earliest for its timestamp defines "on time", and everything else is
late by the difference.

The baseline is the minimum over a sliding pair of windows rather than over all
time, so a slip that never drains is eventually accepted as the new normal
instead of dropping every GOP forever.
*/
type lagTracker struct {
	started bool
	start   time.Time
	origin  int64

	prev     time.Duration // minimum skew in the window before this one
	cur      time.Duration // minimum skew in the window in progress
	windowAt time.Time
}

// observe records a frame's timestamp and returns how far behind realtime the
// stream now is. A frame without a PTS carries no timing information, so it is
// reported as on time rather than guessed at.
func (l *lagTracker) observe(pts int64, now time.Time) time.Duration {
	if pts < 0 {
		return 0
	}
	// PTS runs backwards when the encoder restarts, and once every 26.5 hours
	// when the 33-bit counter wraps. Either way the old origin is useless.
	if !l.started || pts < l.origin {
		l.reset(pts, now)
		return 0
	}

	media := time.Duration(pts-l.origin) * time.Second / ptsClock
	skew := now.Sub(l.start) - media

	if skew < l.cur {
		l.cur = skew
	}
	if now.Sub(l.windowAt) >= baseWindow {
		l.prev, l.cur, l.windowAt = l.cur, skew, now
	}

	base := l.cur
	if l.prev < base {
		base = l.prev
	}
	if lag := skew - base; lag > 0 {
		return lag
	}
	return 0
}

func (l *lagTracker) reset(pts int64, now time.Time) {
	l.started = true
	l.start, l.windowAt = now, now
	l.origin = pts
	l.prev, l.cur = 0, 0
}

/*
containsIDR reports whether an Annex-B access unit carries an IDR picture,
which is the only point a decoder can be re-entered after dropped frames.

A parameter set counts too: x264 and NVENC both emit SPS/PPS immediately
before each IDR when muxing to MPEG-TS, and resuming at the SPS hands the
decoder its configuration along with the keyframe.
*/
func containsIDR(b []byte) bool {
	for i := 0; i+3 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 {
			continue
		}
		// Both the three- and four-byte start codes end in a single 0x01.
		j := i + 2
		if b[j] == 0 {
			j++
		}
		if j+1 >= len(b) || b[j] != 1 {
			continue
		}
		switch b[j+1] & 0x1F {
		case 5, 7: // IDR slice, sequence parameter set
			return true
		}
	}
	return false
}

// frameDuration turns a PTS delta into a wall-clock duration, falling back to
// the codec's nominal rate whenever the delta is missing or implausible.
//
// Dropped frames never reach this, so after a catch-up the delta spans the
// whole discarded run and lands outside maxFrameDur. Falling back to the
// nominal rate is what collapses that gap: the media timeline resumes as if
// the dropped frames had never existed, which is the point of dropping them.
func frameDuration(pts int64, last *int64, fallback time.Duration) time.Duration {
	if pts < 0 {
		return fallback
	}
	prev := *last
	*last = pts
	if prev < 0 || pts <= prev {
		return fallback
	}
	d := time.Duration(pts-prev) * time.Second / ptsClock
	if d < minFrameDur || d > maxFrameDur {
		return fallback
	}
	return d
}

/*
splitOpusUnits strips the Opus-in-MPEG-TS control headers.

Each access unit is prefixed by an 11-bit 0x3FF sync word, three flag bits,
two reserved bits, then a size encoded as a run of 0xFF bytes terminated by a
byte below 0xFF, then the optional trim and extension fields. Everything after
that, for `size` bytes, is the Opus packet WebRTC wants.
*/
func splitOpusUnits(b []byte) ([][]byte, bool) {
	var out [][]byte

	for len(b) >= 2 {
		// The 11-bit prefix is 0x3FF, which lands as 0x7F in byte 0 and the
		// top three bits of byte 1 - not 0xFF, which is the easy misreading.
		if b[0] != opusSyncByte || b[1]&0xE0 != 0xE0 {
			return out, len(out) > 0
		}
		startTrim := b[1]&0x10 != 0
		endTrim := b[1]&0x08 != 0
		ctrlExt := b[1]&0x04 != 0

		i := 2
		size := 0
		for {
			if i >= len(b) {
				return out, len(out) > 0
			}
			v := b[i]
			i++
			size += int(v)
			if v != 0xFF {
				break
			}
		}

		if startTrim {
			i += 2
		}
		if endTrim {
			i += 2
		}
		if ctrlExt {
			if i >= len(b) {
				return out, len(out) > 0
			}
			i += 1 + int(b[i])
		}

		if i+size > len(b) {
			return out, len(out) > 0
		}
		out = append(out, b[i:i+size])
		b = b[i+size:]
	}

	return out, len(out) > 0
}

/*
datagramReader turns a UDP socket into the byte stream the demuxer wants.

Reading the socket directly would work until the demuxer fell behind, at which
point the kernel would drop datagrams mid-packet and desync the parser. A
decoupled reader goroutine keeps draining, and drops whole datagrams under
pressure so what does get through stays TS-aligned.
*/
type datagramReader struct {
	ch      chan []byte
	rest    []byte
	aligned bool
	closed  chan struct{}
	once    sync.Once
}

// tsPacketLen is the fixed MPEG-TS packet size, and tsSync the byte every
// packet starts with.
const (
	tsPacketLen = 188
	tsSync      = 0x47
)

/*
align finds the start of a TS packet in a datagram.

Joining a live stream means the first datagram can begin mid-packet, and the
demuxer's packet-size autodetection then sees only one sync byte in its first
read and gives up. Rather than rely on the retry loop to eventually land on a
clean boundary, the offset is found here: a sync byte with another exactly one
packet later. Returns -1 when this datagram has no usable boundary.
*/
func align(b []byte) int {
	for i := 0; i+tsPacketLen < len(b); i++ {
		if b[i] == tsSync && b[i+tsPacketLen] == tsSync {
			return i
		}
	}
	// A short final datagram may hold a lone packet start.
	if len(b) > 0 && b[0] == tsSync {
		return 0
	}
	return -1
}

func newDatagramReader(ctx context.Context, conn *net.UDPConn) *datagramReader {
	r := &datagramReader{
		ch:     make(chan []byte, 2048),
		closed: make(chan struct{}),
	}

	go func() {
		defer close(r.ch)
		buf := make([]byte, 65536)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n == 0 {
				continue
			}
			pkt := make([]byte, n)
			copy(pkt, buf[:n])

			select {
			case r.ch <- pkt:
			case <-ctx.Done():
				return
			case <-r.closed:
				return
			default:
				// Backed up: drop this datagram rather than block the socket.
			}
		}
	}()

	return r
}

/*
Read fills p from as many queued datagrams as are available.

Returning a single datagram per call is valid io.Reader behaviour but starves
the demuxer's packet-size autodetection, which wants a contiguous run of TS
packets and fails on a short read when joining a stream already in flight.
So this blocks for the first datagram and then drains whatever else is already
buffered, without waiting for more to arrive.
*/
func (r *datagramReader) Read(p []byte) (int, error) {
	if len(r.rest) == 0 {
		pkt, ok := <-r.ch
		if !ok {
			return 0, io.EOF
		}
		r.rest = pkt
	}

	// Discard whatever precedes the first whole packet, once per stream.
	for !r.aligned {
		if off := align(r.rest); off >= 0 {
			r.rest = r.rest[off:]
			r.aligned = true
			break
		}
		pkt, ok := <-r.ch
		if !ok {
			return 0, io.EOF
		}
		r.rest = pkt
	}

	n := copy(p, r.rest)
	r.rest = r.rest[n:]

	for n < len(p) {
		if len(r.rest) > 0 {
			c := copy(p[n:], r.rest)
			r.rest = r.rest[c:]
			n += c
			continue
		}
		// Block rather than return short. The demuxer autodetects packet size
		// from a single read and fails if it gets fewer bytes than it asked
		// for, which showed up as an intermittent "only one sync byte
		// detected" on startup. This is a continuous stream, so waiting for
		// the next datagram is correct; shutdown closes the channel and
		// surfaces as EOF.
		pkt, ok := <-r.ch
		if !ok {
			return n, nil // drained; the next call reports EOF
		}
		r.rest = pkt
	}
	return n, nil
}

func (r *datagramReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}
