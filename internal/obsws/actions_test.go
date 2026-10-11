package obsws

import "testing"

func TestCrossfadePosition(t *testing.T) {
	cases := []struct {
		percent int
		want    float64
	}{
		{0, 0},
		{50, 0.5},
		{100, 1},
		// Out-of-range values clamp rather than error, so a hand-edited
		// deck.json cannot produce a request OBS would reject.
		{-20, 0},
		{250, 1},
	}
	for _, c := range cases {
		if got := crossfadePosition(c.percent); got != c.want {
			t.Errorf("crossfadePosition(%d) = %v, want %v", c.percent, got, c.want)
		}
	}
}

// The release flag is what decides whether a crossfade survives the request.
// OBS cancels a released T-bar back to program, so only a percent of 0 - where
// that cancellation is the point - may release it.
func TestCrossfadeRelease(t *testing.T) {
	for _, c := range []struct {
		percent int
		want    bool
	}{{0, true}, {1, false}, {50, false}, {99, false}, {100, false}, {-20, true}} {
		if got := crossfadeRelease(c.percent); got != c.want {
			t.Errorf("crossfadeRelease(%d) = %v, want %v", c.percent, got, c.want)
		}
	}
}

// Full travel cannot be held: OBS clamps a manual transition to 0.99, so 100
// takes the ordinary transition path instead.
func TestCrossfadeCompletes(t *testing.T) {
	for _, c := range []struct {
		percent int
		want    bool
	}{{0, false}, {50, false}, {99, false}, {100, true}, {250, true}} {
		if got := crossfadeCompletes(c.percent); got != c.want {
			t.Errorf("crossfadeCompletes(%d) = %v, want %v", c.percent, got, c.want)
		}
	}
}

// Only a partial mix stays on air, and only that is what a following scene
// change or transition has to clear first.
func TestCrossfadeHolds(t *testing.T) {
	for _, c := range []struct {
		percent int
		want    bool
	}{
		{0, false}, // released back to program
		{1, true},
		{50, true},
		{99, true},
		{100, false}, // an ordinary transition, which ends by itself
		{-20, false},
		{250, false},
	} {
		if got := crossfadeHolds(c.percent); got != c.want {
			t.Errorf("crossfadeHolds(%d) = %v, want %v", c.percent, got, c.want)
		}
	}
}

// A held mix has to survive until a release actually succeeds, or a scene
// change would go out into an OBS still mid-transition and be dropped.
func TestFadeNeedsClearing(t *testing.T) {
	c := New(nil, nil)
	c.state.StudioMode = true

	if c.fadeNeedsClearing() {
		t.Error("a fresh client is holding no mix")
	}

	c.setFadeHeld(true)
	if !c.fadeNeedsClearing() {
		t.Error("a held mix needs clearing")
	}
	// Asking twice must not consume it: the answer stands until the release
	// request comes back clean.
	if !c.fadeNeedsClearing() {
		t.Error("a held mix is still held on the second look")
	}

	c.setFadeHeld(false)
	if c.fadeNeedsClearing() {
		t.Error("a released mix needs no clearing")
	}
}

// Leaving studio mode ends any manual transition on OBS's side, so a flag set
// before that is stale. Acting on it would put a stray T-bar request on the
// wire, which OBS rejects outright outside studio mode.
func TestFadeNeedsClearingDropsStaleFlag(t *testing.T) {
	c := New(nil, nil)
	c.state.StudioMode = true
	c.setFadeHeld(true)

	c.state.StudioMode = false
	if c.fadeNeedsClearing() {
		t.Error("no mix survives studio mode being turned off")
	}

	c.state.StudioMode = true
	if c.fadeNeedsClearing() {
		t.Error("the stale flag should have been dropped, not just ignored")
	}
}

/* The scene already on air is the press that used to break the Cut button.
 *
 * A program change in studio mode is a transition, and OBS ends one by putting
 * the scene coming off air into preview. Asked to put the scene already on air
 * on air, OBS runs that transition anyway and the side effect is all that is
 * left of it: preview lands on the scene already on program, so preview and
 * program hold the same scene and Cut has nowhere to bounce back to. A stinger
 * would play its animation over a shot that was never going to change, too.
 *
 * So the request is not sent at all. A real cut is left alone, swap included -
 * the outgoing scene belongs in preview, which is what makes Cut work.
 */
func TestAlreadyOnProgram(t *testing.T) {
	c := New(nil, nil)
	c.state.ProgramScene = "Cam 1"

	if !c.alreadyOnProgram("Cam 1") {
		t.Error("the scene on air is on air")
	}
	if c.alreadyOnProgram("Cam 2") {
		t.Error("a scene that is not on air has somewhere to go")
	}

	// Nothing is on air before OBS has said what is, and a press then has to
	// reach OBS rather than be swallowed.
	c.state.ProgramScene = ""
	if c.alreadyOnProgram("") {
		t.Error("an unknown program scene must not swallow a press")
	}
}

// Busy has to match OBS's own test exactly. Waiting on a finished (1) or
// cancelled (0) transition would hold every press for the full timeout, and
// not waiting on one in flight is the dropped press this exists to prevent.
func TestTransitionBusy(t *testing.T) {
	for _, c := range []struct {
		cursor float64
		want   bool
	}{
		{0, false},  // idle after a cancelled T-bar
		{1, false},  // idle after a finished transition
		{0.2, true}, // a held crossfade, or one gliding home after release
		{0.001, true},
		{0.999, true},
	} {
		if got := transitionBusy(c.cursor); got != c.want {
			t.Errorf("transitionBusy(%v) = %v, want %v", c.cursor, got, c.want)
		}
	}
}
