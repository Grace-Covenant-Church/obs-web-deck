package server

import (
	"fmt"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
)

// runAction maps a deck button onto an OBS or PTZ operation.
//
// Native requests are preferred over hotkeys wherever one exists, because
// obs-websocket documents hotkey support as best-effort; the `hotkey` case is
// the escape hatch that still reaches everything else OBS can bind.
func (s *Server) runAction(a config.Action) error {
	switch a.Type {
	case "scene":
		return s.obs.SetProgramScene(a.Scene)
	case "preview":
		return s.obs.SetPreviewScene(a.Scene)
	case "transition":
		return s.obs.Transition()
	case "crossfade":
		return s.obs.Crossfade(a.Percent)
	case "source":
		return s.obs.ToggleSource(a.Scene, a.Source)
	case "mute":
		return s.obs.ToggleMute(a.Input)
	case "stream":
		return s.obs.ToggleStream()
	case "record":
		return s.obs.ToggleRecord()
	case "recordPause":
		return s.obs.ToggleRecordPause()
	case "replay":
		return s.obs.SaveReplayBuffer()
	case "hotkey":
		return s.obs.TriggerHotkey(a.Hotkey)

	case "ptzMove":
		return s.ptz.Move(a.Pan, a.Tilt, a.Zoom)
	case "ptzStop":
		return s.ptz.Stop()
	case "ptzPreset":
		return s.ptz.PresetRecall(a.Preset)

	case "page":
		return nil // handled entirely client side

	default:
		return fmt.Errorf("unknown action type: %s", a.Type)
	}
}
