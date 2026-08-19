package obsws

import (
	"github.com/andreykaipov/goobs/api/requests/general"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/record"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/andreykaipov/goobs/api/requests/stream"
	"github.com/andreykaipov/goobs/api/requests/transitions"
)

func (c *Client) SetProgramScene(name string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Scenes.SetCurrentProgramScene(
		scenes.NewSetCurrentProgramSceneParams().WithSceneName(name))
	return err
}

func (c *Client) SetPreviewScene(name string) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.Scenes.SetCurrentPreviewScene(
		scenes.NewSetCurrentPreviewSceneParams().WithSceneName(name))
	return err
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
	_, err := obs.Transitions.TriggerStudioModeTransition()
	return err
}

// Crossfade holds the studio-mode T-bar part way between program and preview,
// putting a partial mix of the two on air. 100 releases at full travel, which
// completes the swap exactly like a transition; 0 returns to program only.
//
// OBS applies the mix through the current scene transition, so it needs one
// that supports manual control (Fade does; Cut, being instant, has nothing to
// hold part way).
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
	_, err := obs.Transitions.SetTBarPosition(
		transitions.NewSetTBarPositionParams().
			WithPosition(crossfadePosition(percent)).
			WithRelease(true))
	return err
}

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
