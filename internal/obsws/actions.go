package obsws

import (
	"github.com/andreykaipov/goobs/api/requests/general"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/record"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/andreykaipov/goobs/api/requests/stream"
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
