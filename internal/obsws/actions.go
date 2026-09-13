package obsws

import (
	"log"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/general"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/record"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/andreykaipov/goobs/api/requests/stream"
	"github.com/andreykaipov/goobs/api/requests/transitions"
)

/*
SetProgramScene puts a scene on air and leaves the staged one alone.

In studio mode a program change is not a jump, it is a transition, and OBS runs
it the same way the T-bar does: the scene goes to program and the one coming off
air lands in preview. The operator's next shot is thrown away by the cut, which
is what this has to undo. Pressing the scene already on air is the plain case of
it - nothing changes on air at all, and the only visible effect is preview
jumping to the scene just pressed.

Two things follow, and both are needed:

  - A press on the scene already on air does not go to OBS at all. There is
    nothing for it to do, and the transition it would run is not free: a Fade to
    the same scene is invisible, but a Stinger plays its animation over a shot
    that was never going to change.

  - Anywhere else, preview is put back after the transition ends rather than
    when the request returns. The request returns immediately and OBS moves
    preview around 300ms later, at the end of the transition, so a restore sent
    any earlier is simply overwritten.
*/
func (c *Client) SetProgramScene(name string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	c.resetFade(obs)

	// The press may still have meant something - clearing a held mix above is
	// the usual one - but the scene change itself has nowhere to go.
	if c.alreadyOnProgram(name) {
		return nil
	}

	// Read before the cut. Afterwards OBS has already overwritten it, and the
	// value worth keeping is gone.
	restore := c.previewToRestore()

	// Registered before the request, because the transition it starts can end
	// before the next line runs.
	var done chan struct{}
	if restore != "" {
		done = c.awaitTransition()
	}

	if _, err := obs.Scenes.SetCurrentProgramScene(
		scenes.NewSetCurrentProgramSceneParams().WithSceneName(name)); err != nil {
		c.forgetTransition(done)
		return err
	}
	if restore == "" {
		return nil
	}

	select {
	case <-done:
	case <-time.After(transitionGrace):
		// Long enough for any sane transition. Restoring on a timeout risks
		// putting preview back while OBS is still mid-transition, which is
		// still better than leaving the operator's shot discarded.
		c.forgetTransition(done)
	}

	// Nothing to do when the transition left preview where it was - which is
	// what happens when the scene coming off air is the one that was staged.
	if c.State().PreviewScene == restore {
		return nil
	}
	// The cut is what was pressed and it has already gone out, so a preview
	// that will not go back is logged rather than raised as a failed press.
	if err := c.setPreviewScene(obs, restore); err != nil {
		log.Printf("[obs] restoring preview scene %q: %v", restore, err)
	}
	return nil
}

// transitionGrace bounds the wait for a transition to end. The press is being
// held on the pressing phone's connection while this runs, so it cannot be
// open-ended; it is the same order as the longest delay a chain step may ask
// for, and far longer than the 300ms a default Fade takes.
const transitionGrace = 2 * time.Second

// alreadyOnProgram reports whether a scene is the one on air, and so whether a
// request to put it there has anything to do.
//
// This is the scene the last CurrentProgramSceneChanged named, which is a few
// hundred milliseconds behind a change made somewhere else - OBS's own UI, a
// hotkey - while its transition runs. Being behind cannot swallow a press: for
// that the state would have to name the scene being asked for while OBS is on
// a different one, which is the one thing a stale program scene is not.
func (c *Client) alreadyOnProgram(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// A button naming no scene matches an OBS that has not said what is on air
	// yet. That press is a broken deck.json, and it belongs at OBS, where it
	// comes back as an error the operator can see.
	return name != "" && c.state.ProgramScene == name
}

// previewToRestore is the scene a program change has to put back in preview, or
// "" when there is nothing to put back. Outside studio mode there is no preview
// scene at all; a preview scene OBS has not told us about yet is not worth
// guessing at.
//
// What preview holds is deliberately not compared against the scene going on
// air. They can be the same - staging a shot and then cutting straight to it is
// ordinary - and the transition moves preview off it just the same.
func (c *Client) previewToRestore() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.state.StudioMode {
		return ""
	}
	return c.state.PreviewScene
}

/* awaitTransition returns a channel closed when OBS reports the end of a
 * transition.
 *
 * SceneTransitionEnded is the signal rather than the scene-changed events
 * because it is the last of them: OBS moves preview a few milliseconds before
 * it, and puts the new scene on program a few milliseconds after. Waiting for
 * the end of the whole thing is what makes a restore stick.
 */
func (c *Client) awaitTransition() chan struct{} {
	ch := make(chan struct{})
	c.mu.Lock()
	c.transitionWaiters = append(c.transitionWaiters, ch)
	c.mu.Unlock()
	return ch
}

// forgetTransition drops a waiter nobody is listening to any more. A waiter
// left behind would be closed by the next transition, whoever caused it.
func (c *Client) forgetTransition(ch chan struct{}) {
	if ch == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, w := range c.transitionWaiters {
		if w == ch {
			c.transitionWaiters = append(c.transitionWaiters[:i], c.transitionWaiters[i+1:]...)
			return
		}
	}
}

// transitionDone releases everything waiting on a transition. Callers hold the
// lock.
func (c *Client) transitionDone() {
	for _, ch := range c.transitionWaiters {
		close(ch)
	}
	c.transitionWaiters = nil
}

func (c *Client) SetPreviewScene(name string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	c.resetFade(obs)
	return c.setPreviewScene(obs, name)
}

// setPreviewScene stages a scene and records it straight away, rather than
// waiting for OBS to say so. A chain that stages one scene and then cuts to
// another runs faster than the event comes back, and the cut would otherwise
// read a preview scene from before the step that just ran and restore that.
func (c *Client) setPreviewScene(obs *goobs.Client, name string) error {
	if _, err := obs.Scenes.SetCurrentPreviewScene(
		scenes.NewSetCurrentPreviewSceneParams().WithSceneName(name)); err != nil {
		return err
	}
	c.mu.Lock()
	c.state.PreviewScene = name
	c.mu.Unlock()
	return nil
}

func (c *Client) Transition() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	// Only meaningful in studio mode. Outside it this is a no-op rather than
	// an error, so a mis-tapped Cut does not raise a toast.
	if !c.State().StudioMode {
		return nil
	}
	c.resetFade(obs)
	_, err := obs.Transitions.TriggerStudioModeTransition()
	return err
}

// Crossfade holds the studio-mode T-bar part way between program and preview,
// putting a partial mix of the two on air. 0 returns to program only and 100
// completes the swap, which is a plain transition rather than a held T-bar.
//
// OBS applies the mix through a transition that can be held part way. If the
// current one cannot be (Cut, Stinger) it substitutes Fade for the duration,
// so any transition setting works here.
func (c *Client) Crossfade(percent int) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	// Same courtesy as Transition: outside studio mode there is no preview
	// scene to mix in, so this is a no-op rather than an error.
	if !c.State().StudioMode {
		return nil
	}
	percent = min(max(percent, 0), 100)

	// A finished swap is not something the T-bar can express: OBS clamps a
	// manual transition to 0.99 and only completes one when the T-bar widget
	// is let go near the end of its travel, which never happens over
	// obs-websocket (see the release comment below). Full travel is therefore
	// handed to the ordinary transition, which is what it amounts to anyway.
	if crossfadeCompletes(percent) {
		c.resetFade(obs)
		_, err := obs.Transitions.TriggerStudioModeTransition()
		return err
	}

	// Release only at 0, where a cancelled mix is exactly what is wanted.
	//
	// OBS moves the mix from SetTBarPosition but not the T-bar widget itself,
	// so a release reads a slider still sitting at zero, decides the operator
	// let go at the program end, and cancels straight back to program. That
	// makes releasing a partial mix self-defeating: it would undo the very
	// crossfade being asked for. Leaving it unreleased is what keeps the mix
	// on air; a later crossfade or transition ends it.
	if _, err := obs.Transitions.SetTBarPosition(
		transitions.NewSetTBarPositionParams().
			WithPosition(crossfadePosition(percent)).
			WithRelease(crossfadeRelease(percent))); err != nil {
		return err
	}

	c.setFadeHeld(crossfadeHolds(percent))
	return nil
}

// resetFade cancels a held crossfade, putting program alone back on air.
//
// A held mix leaves OBS part way through a manual transition, and while one is
// in progress OBS refuses the two things a deck is most likely to be asked for
// next: a scene change and a transition. Both were simply dead while a fade was
// up. So each clears the mix first, which is what the operator means by pressing
// them anyway - they are done with the blend.
//
// The mix snaps back to program before a transition then runs from there, rather
// than carrying on from where the T-bar sat. Resuming a manual transition is not
// something obs-websocket can express; see Crossfade.
func (c *Client) resetFade(obs *goobs.Client) {
	if !c.fadeNeedsClearing() {
		return
	}

	// Release is the whole point here. OBS reads the T-bar widget - which never
	// moved from zero, because SetTBarPosition does not touch it - as the
	// operator letting go at the program end, and cancels the mix back to
	// program. The behaviour Crossfade has to avoid is exactly what makes this
	// the cancel.
	if _, err := obs.Transitions.SetTBarPosition(
		transitions.NewSetTBarPositionParams().WithPosition(0).WithRelease(true)); err != nil {
		// Keep the flag set: the mix is still up, so the next press should try
		// again rather than assume it is gone.
		log.Printf("[obs] clearing held crossfade: %v", err)
		return
	}
	c.setFadeHeld(false)
}

// fadeNeedsClearing reports whether a mix of ours is still on air. It drops a
// flag that studio mode has already invalidated, because leaving studio mode
// ends any manual transition on OBS's side; what it does not do is clear a live
// one, which only a successful release may do.
func (c *Client) fadeNeedsClearing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.state.StudioMode {
		c.fadeHeld = false
	}
	return c.fadeHeld
}

// setFadeHeld records whether a partial crossfade is sitting on air. This is the
// only account of it anywhere: obs-websocket reports no T-bar position and emits
// no event when one moves, so a mix put up by SetTBarPosition is invisible to
// every other part of this program.
func (c *Client) setFadeHeld(held bool) {
	c.mu.Lock()
	c.fadeHeld = held
	c.mu.Unlock()
}

// crossfadeHolds reports whether this percent leaves a mix on air. Only a
// partial one does: a release returns to program, and a completed swap is an
// ordinary transition that ends by itself.
func crossfadeHolds(percent int) bool {
	return !crossfadeCompletes(percent) && !crossfadeRelease(percent)
}

// crossfadeCompletes reports whether this percent is a finished swap, which
// goes out as an ordinary transition instead of a held T-bar.
func crossfadeCompletes(percent int) bool { return percent >= 100 }

// crossfadeRelease reports whether the T-bar should be released. Only a return
// to program is, because releasing anywhere else cancels the mix; see
// Crossfade.
func crossfadeRelease(percent int) bool { return percent <= 0 }

// crossfadePosition maps 0-100 onto the T-bar's 0.0-1.0 travel, clamping
// rather than erroring so a hand-edited deck.json cannot produce an
// out-of-range request.
func crossfadePosition(percent int) float64 {
	return float64(min(max(percent, 0), 100)) / 100
}

func (c *Client) ToggleSource(scene, source string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	idResp, err := obs.SceneItems.GetSceneItemId(
		sceneitems.NewGetSceneItemIdParams().WithSceneName(scene).WithSourceName(source))
	if err != nil {
		return err
	}
	enResp, err := obs.SceneItems.GetSceneItemEnabled(
		sceneitems.NewGetSceneItemEnabledParams().
			WithSceneName(scene).WithSceneItemId(idResp.SceneItemId))
	if err != nil {
		return err
	}
	_, err = obs.SceneItems.SetSceneItemEnabled(
		sceneitems.NewSetSceneItemEnabledParams().
			WithSceneName(scene).
			WithSceneItemId(idResp.SceneItemId).
			WithSceneItemEnabled(!enResp.SceneItemEnabled))
	return err
}

func (c *Client) ToggleMute(input string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Inputs.ToggleInputMute(inputs.NewToggleInputMuteParams().WithInputName(input))
	return err
}

func (c *Client) ToggleStream() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Stream.ToggleStream(&stream.ToggleStreamParams{})
	return err
}

func (c *Client) ToggleRecord() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Record.ToggleRecord(&record.ToggleRecordParams{})
	return err
}

func (c *Client) ToggleRecordPause() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Record.ToggleRecordPause(&record.ToggleRecordPauseParams{})
	return err
}

func (c *Client) SaveReplayBuffer() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Outputs.SaveReplayBuffer()
	return err
}

// TriggerHotkey fires any hotkey OBS knows by name. This is what makes the
// full set of OBS keybinds, including plugin ones, reachable from a tile.
func (c *Client) TriggerHotkey(name string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.General.TriggerHotkeyByName(
		general.NewTriggerHotkeyByNameParams().WithHotkeyName(name))
	return err
}
