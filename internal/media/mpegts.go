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

// StartMPEGTS listens for an MPEG-TS stream pushed over UDP and republishes it
// on the given path. It returns once the socket is open; ingest runs until ctx
// is cancelled.
func (s *Server) StartMPEGTS(ctx context.Context, addr, path string) error {
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

	log.Printf("[media] MPEG-TS listening on udp://%s -> /%s", addr, path)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	go s.ingestLoop(ctx, conn, path)
	return nil
}

// ingestLoop keeps demuxing, restarting after an error so that stopping and
// restarting the OBS recording reconnects rather than needing a bounce.
func (s *Server) ingestLoop(ctx context.Context, conn *net.UDPConn, path string) {
	for ctx.Err() == nil {
		if err := s.ingest(ctx, conn, path); err != nil && ctx.Err() == nil {
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

func (s *Server) ingest(ctx context.Context, conn *net.UDPConn, path string) error {
	reader := newDatagramReader(ctx, conn)
	defer reader.Close()

	dmx := astits.NewDemuxer(ctx, reader)
	rm := s.roomFor(path)

	var (
		videoPID, audioPID uint16
		video, audio       *webrtc.TrackLocalStaticSample
		lastVideoPTS       int64 = -1
		lastAudioPTS       int64 = -1
		warnedCodec              = map[astits.StreamType]bool{}
		loggedPMT          bool
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

// frameDuration turns a PTS delta into a wall-clock duration, falling back to
// the codec's nominal rate whenever the delta is missing or implausible.
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
	ch     chan []byte
	rest   []byte
	closed chan struct{}
	once   sync.Once
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

func (r *datagramReader) Read(p []byte) (int, error) {
	if len(r.rest) == 0 {
		pkt, ok := <-r.ch
		if !ok {
			return 0, io.EOF
		}
		r.rest = pkt
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

func (r *datagramReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}
