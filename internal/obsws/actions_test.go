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
 * In studio mode OBS gets to a new program scene through preview, so the scene
 * the operator lined up for their next shot is gone the moment someone cuts -
 * even a cut to the scene already on air, where preview is the only thing that
 * moves. The scene to restore is read before the cut, because after it OBS has
 * already overwritten it.
 */
func TestPreviewToRestore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		studioMode bool
		preview    string
		program    string // the scene being cut to
		want       string
	}{
		{"staged shot is kept", true, "Cam 2", "Cam 1", "Cam 2"},
		{"nothing to put back when preview is the scene going on air", true, "Cam 1", "Cam 1", ""},
		// Outside studio mode there is no preview scene, and a stale name left
		// in the state would otherwise be staged into a program-only OBS.
		{"no preview scene outside studio mode", false, "Cam 2", "Cam 1", ""},
		{"nothing staged", true, "", "Cam 1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(nil, nil)
			c.state.StudioMode = tc.studioMode
			c.state.PreviewScene = tc.preview

			if got := c.previewToRestore(tc.program); got != tc.want {
				t.Errorf("previewToRestore(%q) = %q, want %q", tc.program, got, tc.want)
			}
		})
	}
}

// The case the operator actually complained about: the scene pressed is the one
// already on air, so the cut changes nothing on air and OBS's only visible
// effect is dragging preview off the shot being lined up.
func TestPreviewToRestoreOnSceneAlreadyOnAir(t *testing.T) {
	c := New(nil, nil)
	c.state.StudioMode = true
	c.state.ProgramScene = "Cam 1"
	c.state.PreviewScene = "Cam 2"

	if got := c.previewToRestore("Cam 1"); got != "Cam 2" {
		t.Errorf("previewToRestore(%q) = %q, want %q", "Cam 1", got, "Cam 2")
	}
}
