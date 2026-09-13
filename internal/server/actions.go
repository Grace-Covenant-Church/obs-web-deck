package server

import (
	"errors"
	"fmt"
	"time"

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
	case "chain":
		return s.runChain(a.Steps)
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

/* Chain bounds.
 *
 * A chain runs on the read loop of the phone that pressed it, so that phone's
 * next message waits for the chain to finish. That is the right order for a
 * sequence of presses, but it means a hand-edited deck must not be able to hold
 * the loop open indefinitely.
 */
const (
	maxChainSteps = 12
	maxStepDelay  = 2 * time.Second
	maxChainDelay = 5 * time.Second
)

/* runChain runs each step in order, stopping at the first failure.
 *
 * Later steps generally depend on earlier ones - a crossfade means nothing until
 * the preview scene it mixes toward is set - so carrying on past an error would
 * put the tile's second half on air without its first. The step number is in
 * the message because a chain that half ran is otherwise hard to read from a
 * toast.
 */
func (s *Server) runChain(steps []config.Action) error {
	if len(steps) == 0 {
		return errors.New("this button has no steps")
	}
	if len(steps) > maxChainSteps {
		return fmt.Errorf("a button runs at most %d steps, this one has %d",
			maxChainSteps, len(steps))
	}

	var waited time.Duration
	for i, step := range steps {
		// One level only. Nesting buys nothing a flat list cannot express, and
		// a deck.json that points a chain at itself would otherwise recurse
		// until the stack gives out.
		if step.Type == "chain" {
			return fmt.Errorf("step %d: a chain cannot contain another chain", i+1)
		}
		if d := stepDelay(step.DelayMs, i, waited); d > 0 {
			time.Sleep(d)
			waited += d
		}
		if err := s.runAction(step); err != nil {
			return fmt.Errorf("step %d (%s): %w", i+1, step.Type, err)
		}
	}
	return nil
}

/* stepDelay is how long to wait before step i, clamped so that neither one step
 * nor a whole chain can hold a connection open for long.
 *
 * The first step never waits: a chain begins when the tile is pressed, and a
 * button that sits still before doing anything reads as a dropped press.
 */
func stepDelay(ms, i int, waited time.Duration) time.Duration {
	if ms <= 0 || i == 0 {
		return 0
	}
	d := min(time.Duration(ms)*time.Millisecond, maxStepDelay)
	return min(d, max(maxChainDelay-waited, 0))
}
