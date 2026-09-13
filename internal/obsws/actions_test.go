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

/* A program change must not disturb the staged scene.
 *
 * A program change in studio mode is a transition, and OBS ends one by putting
 * the scene coming off air into preview. The shot the operator lined up next is
 * gone unless the deck puts it back, so what preview held is read before the
 * cut - afterwards it is already overwritten.
 */
func TestPreviewToRestore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		studioMode bool
		preview    string
		want       string
	}{
		{"staged shot is kept", true, "Cam 2", "Cam 2"},
		// Outside studio mode there is no preview scene, and a name left over
		// in the state would otherwise be staged into a program-only OBS.
		{"no preview scene outside studio mode", false, "Cam 2", ""},
		{"nothing staged yet", true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(nil, nil)
			c.state.StudioMode = tc.studioMode
			c.state.PreviewScene = tc.preview

			if got := c.previewToRestore(); got != tc.want {
				t.Errorf("previewToRestore() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Staging a shot and then cutting straight to it is ordinary, and the
// transition moves preview off it just the same - so preview being the scene
// going on air is no reason to skip the restore. Measured against OBS 31: a cut
// to the scene already on program still left preview holding it.
func TestPreviewToRestoreWhenStagedSceneIsTheOneGoingOnAir(t *testing.T) {
	c := New(nil, nil)
	c.state.StudioMode = true
	c.state.ProgramScene = "Cam 1"
	c.state.PreviewScene = "Cam 2"

	if got := c.previewToRestore(); got != "Cam 2" {
		t.Errorf("previewToRestore() = %q, want %q", got, "Cam 2")
	}
}

/* The scene already on air is the case the operator complained about: pressing
 * it changes nothing on air, and every visible effect - preview jumping to it,
 * a stinger playing over an unchanged shot - is one nobody asked for. So the
 * request is not sent at all.
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

// A restore sent when the request returns is overwritten: OBS moves preview at
// the end of the transition, about 300ms later. The waiter is what holds the
// restore back until then.
func TestAwaitTransition(t *testing.T) {
	c := New(nil, nil)
	done := c.awaitTransition()

	select {
	case <-done:
		t.Fatal("released before the transition ended")
	default:
	}

	c.mu.Lock()
	c.transitionDone()
	c.mu.Unlock()

	select {
	case <-done:
	default:
		t.Error("the end of a transition releases the waiter")
	}
}

// A transition that never ends - or one whose request failed before it started -
// must not leave a channel behind for the next transition to close, whoever
// caused that one.
func TestForgetTransition(t *testing.T) {
	c := New(nil, nil)
	done := c.awaitTransition()
	c.forgetTransition(done)

	c.mu.RLock()
	n := len(c.transitionWaiters)
	c.mu.RUnlock()
	if n != 0 {
		t.Errorf("waiters left registered: %d", n)
	}

	// The dropped waiter is not closed by the next transition, and dropping it
	// twice - or dropping the nil one a failed request leaves - is harmless.
	c.forgetTransition(done)
	c.forgetTransition(nil)
	c.mu.Lock()
	c.transitionDone()
	c.mu.Unlock()

	select {
	case <-done:
		t.Error("a forgotten waiter was closed anyway")
	default:
	}
}

// Every waiter is released, and the slice is cleared so a second transition
// cannot close the same channel twice.
func TestTransitionDoneReleasesEveryWaiter(t *testing.T) {
	c := New(nil, nil)
	a, b := c.awaitTransition(), c.awaitTransition()

	c.mu.Lock()
	c.transitionDone()
	c.transitionDone()
	c.mu.Unlock()

	for i, ch := range []chan struct{}{a, b} {
		select {
		case <-ch:
		default:
			t.Errorf("waiter %d was not released", i)
		}
	}
}
