// Package ptz drives obs-ptz over the same obs-websocket connection used for
// everything else, via CallVendorRequest.
//
// The request names and parameters come from the installed plugin's own
// signatures:
//
//	void ptz_move_continuous(int device_id, float pan, float tilt, float zoom, float focus)
//	void ptz_preset_recall(int device_id, int preset_id)
//	void ptz_preset_save(int device_id, int preset_id)
//
// They live in settings.json ("ptz.profile") rather than in code, so a plugin
// update that renames a request is a config edit, not a patch.
//
// Note that pan, tilt and zoom are parameters of the *same* continuous-move
// call: a joystick deflection and a zoom adjustment travel as one request.
package ptz

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
)

const (
	// deadman is how long a continuous move survives without a refresh from
	// the client before the camera is halted.
	deadman = 700 * time.Millisecond

	// epsilon is the floor below which an axis counts as centred.
	epsilon = 0.004
)

// VendorCaller is the subset of the OBS client this package needs.
type VendorCaller interface {
	CallVendor(vendor, request string, data map[string]any) error
}

type Controller struct {
	obs      VendorCaller
	settings func() *config.Settings

	mu     sync.Mutex
	timer  *time.Timer
	moving bool
	last   vec
}

type vec struct{ pan, tilt, zoom float64 }

func New(obs VendorCaller, settings func() *config.Settings) *Controller {
	return &Controller{obs: obs, settings: settings}
}

func (c *Controller) cfg() config.PTZSettings { return c.settings().PTZ }

func (c *Controller) Enabled() bool { return c.cfg().Enabled }

// armDeadman restarts the safety timer. A phone that drops off WiFi mid-pan
// would otherwise leave the camera slewing until it hits a hard limit.
func (c *Controller) armDeadman() {
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(deadman, func() {
		c.mu.Lock()
		c.moving = false
		c.last = vec{}
		c.mu.Unlock()
		_ = c.send(0, 0, 0)
	})
}

func (c *Controller) disarm() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

func (c *Controller) vendor(op string, vars map[string]any) error {
	cfg := c.cfg()
	spec, ok := cfg.Profile[op]
	if !ok {
		return errors.New("no PTZ profile mapping for op: " + op)
	}

	// Payload templates use "$name" placeholders filled from vars.
	data := make(map[string]any, len(spec.Data))
	for k, v := range spec.Data {
		if s, isStr := v.(string); isStr && len(s) > 1 && s[0] == '$' {
			data[k] = vars[s[1:]]
		} else {
			data[k] = v
		}
	}
	return c.obs.CallVendor(cfg.VendorName, spec.Request, data)
}

func (c *Controller) send(pan, tilt, zoom float64) error {
	return c.vendor("move", map[string]any{
		"deviceId": c.cfg().DeviceID,
		"pan":      pan,
		"tilt":     tilt,
		"zoom":     zoom,
	})
}

// Move accepts joystick deflections in -1..1 and scales them by the configured
// max speeds, so a small stick movement creeps and a full deflection slews.
func (c *Controller) Move(pan, tilt, zoom float64) error {
	if !c.Enabled() {
		return errors.New("PTZ is disabled in settings.json")
	}
	cfg := c.cfg()
	p := clamp(pan) * cfg.PanSpeed
	t := clamp(tilt) * cfg.TiltSpeed
	z := clamp(zoom) * cfg.ZoomSpeed

	c.mu.Lock()
	idle := math.Abs(p) < epsilon && math.Abs(t) < epsilon && math.Abs(z) < epsilon
	// The joystick emits a steady sample stream; only tell the camera when the
	// values actually change, so VISCA isn't flooded with duplicates.
	same := near(p, c.last.pan) && near(t, c.last.tilt) && near(z, c.last.zoom)

	if idle {
		c.disarm()
		c.moving = false
		if same {
			c.mu.Unlock()
			return nil
		}
		c.last = vec{}
		c.mu.Unlock()
		return c.send(0, 0, 0)
	}

	c.moving = true
	c.armDeadman()
	if same {
		c.mu.Unlock()
		return nil
	}
	c.last = vec{p, t, z}
	c.mu.Unlock()
	return c.send(p, t, z)
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	c.disarm()
	c.moving = false
	c.last = vec{}
	c.mu.Unlock()
	if !c.Enabled() {
		return nil
	}
	return c.send(0, 0, 0)
}

func (c *Controller) PresetRecall(n int) error {
	if !c.Enabled() {
		return errors.New("PTZ is disabled in settings.json")
	}
	return c.vendor("presetRecall", map[string]any{
		"deviceId": c.cfg().DeviceID, "preset": n,
	})
}

func (c *Controller) PresetSave(n int) error {
	if !c.Enabled() {
		return errors.New("PTZ is disabled in settings.json")
	}
	return c.vendor("presetSave", map[string]any{
		"deviceId": c.cfg().DeviceID, "preset": n,
	})
}

// Keepalive refreshes the deadman without re-sending an unchanged velocity.
func (c *Controller) Keepalive() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.moving {
		c.armDeadman()
	}
}

func clamp(v float64) float64 {
	return math.Max(-1, math.Min(1, v))
}

func near(a, b float64) bool { return math.Abs(a-b) < epsilon }
