// Package config loads and hot-reloads the deck layout and runtime settings.
//
// Both files allow "//"-prefixed keys as inline documentation; encoding/json
// ignores unknown fields, so they cost nothing at parse time.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------- settings.json ----------

type Settings struct {
	Server ServerSettings `json:"server"`
	OBS    OBSSettings    `json:"obs"`
	Video  VideoSettings  `json:"video"`
	PTZ    PTZSettings    `json:"ptz"`
}

type ServerSettings struct {
	Port        int    `json:"port"`
	BindAddress string `json:"bindAddress"`
}

type OBSSettings struct {
	Host        string `json:"host"`
	Password    string `json:"password"`
	ReconnectMs int    `json:"reconnectMs"`
}

type VideoSettings struct {
	// Mode selects how the preview arrives:
	//   "builtin" - OBS pushes WHIP to this binary (Stream output)
	//   "mpegts"  - OBS pushes MPEG-TS over UDP (Recording output), leaving
	//               the Stream output free for a real destination
	//   "external"- an already-running MediaMTX or similar
	//   "none"    - controls only
	Mode string `json:"mode"`

	// StreamPath is the path segment the preview is published and played on.
	StreamPath string `json:"streamPath"`

	// TSListen is the UDP address OBS pushes MPEG-TS to when Mode is "mpegts".
	TSListen string `json:"tsListen"`

	// ExternalWhepURL is used only when Mode is "external". Blank means
	// derive it from the request host and ExternalPort.
	ExternalWhepURL string `json:"externalWhepUrl"`
	ExternalPort    int    `json:"externalPort"`
}

type PTZSettings struct {
	Enabled    bool   `json:"enabled"`
	VendorName string `json:"vendorName"`
	DeviceID   int    `json:"deviceId"`

	PanSpeed  float64 `json:"panSpeed"`
	TiltSpeed float64 `json:"tiltSpeed"`
	ZoomSpeed float64 `json:"zoomSpeed"`

	// Profile maps abstract ops ("move", "presetRecall", "presetSave") onto
	// concrete obs-ptz vendor requests, so a plugin rename is a config edit
	// rather than a code change.
	Profile map[string]VendorSpec `json:"profile"`
}

type VendorSpec struct {
	Request string         `json:"request"`
	Data    map[string]any `json:"data"`
}

// ---------- deck.json ----------

type Deck struct {
	Grid  Grid   `json:"grid"`
	Pages []Page `json:"pages"`
}

// Grid bounds. Below two columns the tiles stop reading as a deck; above five
// they get too small to hit reliably on a phone.
const (
	MinCols = 2
	MaxCols = 5
	MinRows = 1
	MaxRows = 8
)

type Grid struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// normalize clamps the grid into the supported range. Applied on both load and
// save, so neither a hand-edited file nor a malformed POST can produce a
// layout the UI cannot render.
func (d *Deck) normalize() {
	d.Grid.Cols = clampInt(d.Grid.Cols, MinCols, MaxCols, MaxCols)
	d.Grid.Rows = clampInt(d.Grid.Rows, MinRows, MaxRows, 5)
}

func clampInt(v, lo, hi, fallback int) int {
	if v == 0 {
		return fallback
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// OffGrid counts buttons that fall outside the current grid. Narrowing the
// deck does not delete them - they stay in the file and reappear when it is
// widened again - so the UI needs to say how many are hidden.
func (d *Deck) OffGrid() int {
	n := 0
	for _, p := range d.Pages {
		if p.Layout == "ptz" {
			continue // the PTZ page is not laid out on the grid
		}
		for _, b := range p.Buttons {
			if b.Col >= d.Grid.Cols || b.Row >= d.Grid.Rows {
				n++
			}
		}
	}
	return n
}

type Page struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Layout string `json:"layout,omitempty"` // "" = grid, "ptz" = joystick page

	// AutoScenes fills this page's free cells from OBS's live scene list, so
	// the deck tracks whatever collection is loaded instead of hardcoding
	// scene names that may not exist. Explicit buttons always win their cell.
	AutoScenes bool `json:"autoScenes,omitempty"`

	Buttons []Button `json:"buttons"`
}

type Button struct {
	Row      int       `json:"row"`
	Col      int       `json:"col"`
	Label    string    `json:"label"`
	Icon     string    `json:"icon,omitempty"`
	Color    string    `json:"color,omitempty"`
	Action   Action    `json:"action"`
	Feedback *Feedback `json:"feedback,omitempty"`
}

type Action struct {
	Type    string  `json:"type"`
	Scene   string  `json:"scene,omitempty"`
	Source  string  `json:"source,omitempty"`
	Input   string  `json:"input,omitempty"`
	Hotkey  string  `json:"hotkey,omitempty"`
	Page    string  `json:"page,omitempty"`
	Preset  int     `json:"preset,omitempty"`
	Percent int     `json:"percent,omitempty"`
	Pan     float64 `json:"pan,omitempty"`
	Tilt    float64 `json:"tilt,omitempty"`
	Zoom    float64 `json:"zoom,omitempty"`
}

type Feedback struct {
	Type   string `json:"type"`
	Scene  string `json:"scene,omitempty"`
	Source string `json:"source,omitempty"`
	Input  string `json:"input,omitempty"`
	Invert bool   `json:"invert,omitempty"`
}

// SceneItemTargets lists every (scene, source) pair the deck needs visibility
// feedback for, so we only query what is actually displayed.
func (d *Deck) SceneItemTargets() [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	for _, p := range d.Pages {
		for _, b := range p.Buttons {
			fb := b.Feedback
			if fb == nil || fb.Type != "sourceVisible" || fb.Scene == "" || fb.Source == "" {
				continue
			}
			key := fb.Scene + "\x00" + fb.Source
			if !seen[key] {
				seen[key] = true
				out = append(out, [2]string{fb.Scene, fb.Source})
			}
		}
	}
	return out
}

// InputTargets lists every audio input the deck needs mute feedback for.
func (d *Deck) InputTargets() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range d.Pages {
		for _, b := range p.Buttons {
			fb := b.Feedback
			if fb == nil || fb.Type != "inputMuted" || fb.Input == "" {
				continue
			}
			if !seen[fb.Input] {
				seen[fb.Input] = true
				out = append(out, fb.Input)
			}
		}
	}
	return out
}

// ---------- loading ----------

type Store struct {
	dir string

	mu       sync.RWMutex
	settings *Settings
	deck     *Deck
}

func NewStore(dir string) (*Store, error) {
	s := &Store{dir: dir}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Reload() error {
	var set Settings
	if err := readJSON(filepath.Join(s.dir, "settings.json"), &set); err != nil {
		return err
	}
	var deck Deck
	if err := readJSON(filepath.Join(s.dir, "deck.json"), &deck); err != nil {
		return err
	}
	applyEnvOverrides(&set)
	applyDefaults(&set)
	deck.normalize()

	s.mu.Lock()
	s.settings, s.deck = &set, &deck
	s.mu.Unlock()
	return nil
}

func (s *Store) Settings() *Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Store) Deck() *Deck {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deck
}

// SaveDeck persists a layout edited from the UI and swaps it in atomically, so
// a crash mid-write can't leave a truncated deck.json behind.
func (s *Store) SaveDeck(d *Deck) error {
	d.normalize()
	blob, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "deck.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.mu.Lock()
	s.deck = d
	s.mu.Unlock()
	return nil
}

func readJSON(path string, into any) error {
	blob, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(blob, into); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func applyEnvOverrides(s *Settings) {
	if v := os.Getenv("OBS_PASSWORD"); v != "" {
		s.OBS.Password = v
	}
	if v := os.Getenv("OBS_HOST"); v != "" {
		s.OBS.Host = v
	}
}

func applyDefaults(s *Settings) {
	if s.Server.Port == 0 {
		s.Server.Port = 8080
	}
	if s.Server.BindAddress == "" {
		s.Server.BindAddress = "0.0.0.0"
	}
	if s.OBS.Host == "" {
		s.OBS.Host = "localhost:4455"
	}
	if s.OBS.ReconnectMs == 0 {
		s.OBS.ReconnectMs = 3000
	}
	if s.Video.Mode == "" {
		s.Video.Mode = "builtin"
	}
	if s.Video.StreamPath == "" {
		s.Video.StreamPath = "preview"
	}
	if s.Video.TSListen == "" {
		s.Video.TSListen = "127.0.0.1:9000"
	}
	if s.PTZ.VendorName == "" {
		s.PTZ.VendorName = "ptz-controls"
	}
	if s.PTZ.PanSpeed == 0 {
		s.PTZ.PanSpeed = 0.5
	}
	if s.PTZ.TiltSpeed == 0 {
		s.PTZ.TiltSpeed = 0.5
	}
	if s.PTZ.ZoomSpeed == 0 {
		s.PTZ.ZoomSpeed = 0.4
	}
}

func (s *Settings) ReconnectDelay() time.Duration {
	return time.Duration(s.OBS.ReconnectMs) * time.Millisecond
}
