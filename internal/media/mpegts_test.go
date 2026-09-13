package media

import (
	"testing"
	"time"
)

// pts converts a wall-clock offset into a 90kHz timestamp, so the tests can be
// written in the units the behaviour is specified in.
func pts(d time.Duration) int64 {
	return int64(d * ptsClock / time.Second)
}

// annexB builds an access unit out of NAL units of the given types, using the
// four-byte start code x264 emits into MPEG-TS.
func annexB(types ...byte) []byte {
	var out []byte
	for _, t := range types {
		out = append(out, 0, 0, 0, 1, t&0x1F)
		out = append(out, 0xDE, 0xAD, 0xBE, 0xEF) // stand-in slice payload
	}
	return out
}

var (
	keyframe = annexB(7, 8, 5) // SPS, PPS, IDR - what OBS sends each GOP
	interAU  = annexB(1)       // a plain P frame
)

func TestContainsIDR(t *testing.T) {
	cases := []struct {
		name string
		au   []byte
		want bool
	}{
		{"keyframe access unit", keyframe, true},
		{"inter frame", interAU, false},
		{"idr alone", annexB(5), true},
		{"sps alone", annexB(7), true},
		{"three byte start code", []byte{0, 0, 1, 0x65, 0xAA}, true},
		{"three byte start code, inter", []byte{0, 0, 1, 0x41, 0xAA}, false},
		{"leading zero run", []byte{0, 0, 0, 0, 0, 1, 0x65, 0xAA}, true},
		{"empty", nil, false},
		{"truncated after start code", []byte{0, 0, 0, 1}, false},
		{"no start code at all", []byte{0xDE, 0xAD, 0xBE, 0xEF}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsIDR(tc.au); got != tc.want {
				t.Errorf("containsIDR = %v, want %v", got, tc.want)
			}
		})
	}
}

// A stream arriving at a steady offset from its own clock is on time no matter
// how large that offset is: it is the encoder's pipeline delay, not our lag.
func TestLagTrackerSteadyStreamIsOnTime(t *testing.T) {
	var l lagTracker
	base := time.Now()
	const pipeline = 2 * time.Second // however far behind the encoder starts

	for i := range 100 {
		at := time.Duration(i) * 33 * time.Millisecond
		if lag := l.observe(pts(at), base.Add(at+pipeline)); lag != 0 {
			t.Fatalf("frame %d: lag = %v, want 0", i, lag)
		}
	}
}

func TestLagTrackerReportsBacklog(t *testing.T) {
	var l lagTracker
	base := time.Now()

	l.observe(pts(0), base)
	l.observe(pts(33*time.Millisecond), base.Add(33*time.Millisecond))

	// The process stalls for a second; the frames queue up and then arrive in
	// a rush, each one a second later than its timestamp says.
	lag := l.observe(pts(66*time.Millisecond), base.Add(66*time.Millisecond+time.Second))
	if lag < 900*time.Millisecond || lag > 1100*time.Millisecond {
		t.Fatalf("lag = %v, want about 1s", lag)
	}

	// Draining the backlog brings the stream back to where it started.
	lag = l.observe(pts(1500*time.Millisecond), base.Add(1500*time.Millisecond))
	if lag != 0 {
		t.Fatalf("after draining, lag = %v, want 0", lag)
	}
}

// A slip that never drains has to stop being reported, or a source clock
// running slow against ours would drop every GOP for the rest of the session.
func TestLagTrackerAcceptsAPermanentSlip(t *testing.T) {
	var l lagTracker
	base := time.Now()
	l.observe(pts(0), base)

	const slip = time.Second
	var lag time.Duration
	for at := 33 * time.Millisecond; at < 3*baseWindow; at += 33 * time.Millisecond {
		lag = l.observe(pts(at), base.Add(at+slip))
	}
	if lag != 0 {
		t.Fatalf("lag = %v after %v of steady slip, want 0", lag, 3*baseWindow)
	}
}

// A restarted encoder rewinds its clock. The old origin is meaningless then,
// and keeping it would read as hours of lag and blank the preview.
func TestLagTrackerResetsOnRewind(t *testing.T) {
	var l lagTracker
	base := time.Now()
	l.observe(pts(time.Hour), base)

	if lag := l.observe(pts(0), base.Add(time.Second)); lag != 0 {
		t.Fatalf("lag = %v after a PTS rewind, want 0", lag)
	}
	if lag := l.observe(pts(33*time.Millisecond), base.Add(time.Second+33*time.Millisecond)); lag != 0 {
		t.Fatalf("lag = %v on the frame after a rewind, want 0", lag)
	}
}

func TestVideoGatePassesEverythingWhenOnTime(t *testing.T) {
	g := videoGate{max: 400 * time.Millisecond}
	for i := range 10 {
		if g.drop(50*time.Millisecond, interAU) {
			t.Fatalf("frame %d dropped while on time", i)
		}
	}
}

func TestVideoGateDisabled(t *testing.T) {
	var g videoGate // max 0
	if g.drop(30*time.Second, interAU) {
		t.Fatal("dropped a frame with the guard disabled")
	}
}

// The point of the gate: a partial GOP is never forwarded, so once dropping
// starts it runs to the next keyframe even after the lag has gone.
func TestVideoGateDropsToTheNextKeyframe(t *testing.T) {
	g := videoGate{max: 400 * time.Millisecond}

	if !g.drop(time.Second, interAU) {
		t.Fatal("a frame a second late was forwarded")
	}
	// Lag is back inside budget, but the decoder still has nowhere to re-enter.
	for i := range 5 {
		if !g.drop(10*time.Millisecond, interAU) {
			t.Fatalf("inter frame %d forwarded mid catch-up", i)
		}
	}
	if g.drop(10*time.Millisecond, keyframe) {
		t.Fatal("keyframe dropped once the stream had caught up")
	}
	if g.active {
		t.Fatal("still catching up after resuming")
	}
	if g.dropped != 6 {
		t.Fatalf("dropped = %d, want 6", g.dropped)
	}

	// And it stays open afterwards.
	if g.drop(10*time.Millisecond, interAU) {
		t.Fatal("dropped a frame after catching up")
	}
}

// A backlog deeper than one GOP has to be discarded a GOP at a time. Taking
// the first keyframe on offer would leave the preview permanently late.
func TestVideoGateSkipsKeyframesWhileStillBehind(t *testing.T) {
	g := videoGate{max: 400 * time.Millisecond}

	lag := 3 * time.Second
	for range 3 {
		if !g.drop(lag, keyframe) {
			t.Fatalf("keyframe forwarded while %v behind", lag)
		}
		lag -= time.Second // each discarded GOP drains a second of backlog
	}
	if g.drop(0, keyframe) {
		t.Fatal("keyframe dropped after the backlog had drained")
	}
	if g.dropped != 3 {
		t.Fatalf("dropped = %d, want 3", g.dropped)
	}
}

// The counter describes the catch-up in progress, not the session, so an
// operator reading the log sees how deep this backlog was.
func TestVideoGateCountsEachCatchUpSeparately(t *testing.T) {
	g := videoGate{max: 400 * time.Millisecond}

	g.drop(time.Second, interAU)
	g.drop(0, keyframe)
	if g.dropped != 1 {
		t.Fatalf("first catch-up dropped = %d, want 1", g.dropped)
	}

	g.drop(time.Second, interAU)
	g.drop(0, interAU)
	g.drop(0, keyframe)
	if g.dropped != 2 {
		t.Fatalf("second catch-up dropped = %d, want 2", g.dropped)
	}
}
