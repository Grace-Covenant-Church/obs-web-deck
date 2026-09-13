package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// The gate is the whole feature, so these are the cases the deck rests on.
func TestEnabledIn(t *testing.T) {
	for _, c := range []struct {
		name   string
		enable *Enable
		scene  string
		want   bool
	}{
		// No rule at all: every button that predates the field, and every one
		// saved without a gate.
		{"no rule", nil, "Worship", true},
		// A mode picked with nothing ticked reaches here as an empty list. It
		// must read as no gate, not as a gate that closes everywhere.
		{"empty list", &Enable{}, "Worship", true},

		{"only, listed", &Enable{Scenes: []string{"Sermon", "Worship"}}, "Worship", true},
		{"only, not listed", &Enable{Scenes: []string{"Sermon"}}, "Worship", false},

		{"except, listed", &Enable{Scenes: []string{"Countdown"}, Invert: true}, "Countdown", false},
		{"except, not listed", &Enable{Scenes: []string{"Countdown"}, Invert: true}, "Sermon", true},

		// No scene on air - OBS not connected, or connected before the first
		// scene list arrives. An "only" gate cannot be satisfied by nothing,
		// and an "except" gate has nothing to exclude.
		{"only, no scene", &Enable{Scenes: []string{"Sermon"}}, "", false},
		{"except, no scene", &Enable{Scenes: []string{"Countdown"}, Invert: true}, "", true},

		// Scene names are OBS's, matched exactly: OBS treats two scenes
		// differing only in case as two scenes.
		{"case matters", &Enable{Scenes: []string{"Sermon"}}, "sermon", false},
	} {
		b := Button{Label: c.name, Enable: c.enable}
		if got := b.EnabledIn(c.scene); got != c.want {
			t.Errorf("%s: EnabledIn(%q) = %v, want %v", c.name, c.scene, got, c.want)
		}
	}
}

// A field the Go struct does not know about is dropped on the first save, since
// every deck edit round trips the whole layout through here. That failure is
// silent and costs the operator their rule, so it is worth pinning.
func TestButtonGateSurvivesRoundTrip(t *testing.T) {
	const raw = `{
	  "grid": {"cols": 3, "rows": 2},
	  "pages": [{"id": "main", "buttons": [
	    {"row": 0, "col": 1, "label": "Lyrics",
	     "action": {"type": "source", "scene": "Worship", "source": "Lyrics"},
	     "enable": {"scenes": ["Worship", "Sermon"]}},
	    {"row": 1, "col": 0, "label": "Cut",
	     "action": {"type": "transition"},
	     "enable": {"scenes": ["Countdown"], "invert": true}}
	  ]}]
	}`

	var deck Deck
	if err := json.Unmarshal([]byte(raw), &deck); err != nil {
		t.Fatalf("parse: %v", err)
	}

	blob, err := json.Marshal(&deck)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Deck
	if err := json.Unmarshal(blob, &again); err != nil {
		t.Fatalf("reparse: %v", err)
	}

	buttons := again.Pages[0].Buttons
	if len(buttons) != 2 {
		t.Fatalf("got %d buttons, want 2", len(buttons))
	}

	lyrics, cut := buttons[0], buttons[1]
	if lyrics.Enable == nil || len(lyrics.Enable.Scenes) != 2 || lyrics.Enable.Invert {
		t.Errorf("lyrics gate did not survive: %+v", lyrics.Enable)
	}
	if cut.Enable == nil || !cut.Enable.Invert {
		t.Errorf("cut gate did not survive: %+v", cut.Enable)
	}
	// And the rule still means what it meant before the trip.
	if !lyrics.EnabledIn("Sermon") || lyrics.EnabledIn("Countdown") {
		t.Error("lyrics gate changed meaning")
	}
	if cut.EnabledIn("Countdown") || !cut.EnabledIn("Worship") {
		t.Error("cut gate changed meaning")
	}
}

// Chain steps go through the same round trip as everything else, and a step's
// own fields have to survive it or the second half of a tile quietly stops
// happening.
func TestChainSurvivesRoundTrip(t *testing.T) {
	const raw = `{
	  "grid": {"cols": 2, "rows": 1},
	  "pages": [{"id": "main", "buttons": [
	    {"row": 0, "col": 0, "label": "Take Cam 2",
	     "action": {"type": "chain", "steps": [
	       {"type": "preview", "scene": "Cam 2"},
	       {"type": "crossfade", "percent": 50, "delayMs": 250}
	     ]}}
	  ]}]
	}`

	var deck Deck
	if err := json.Unmarshal([]byte(raw), &deck); err != nil {
		t.Fatalf("parse: %v", err)
	}
	blob, err := json.Marshal(&deck)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Deck
	if err := json.Unmarshal(blob, &again); err != nil {
		t.Fatalf("reparse: %v", err)
	}

	steps := again.Pages[0].Buttons[0].Action.Steps
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(steps))
	}
	if steps[0].Type != "preview" || steps[0].Scene != "Cam 2" {
		t.Errorf("first step did not survive: %+v", steps[0])
	}
	if steps[1].Percent != 50 || steps[1].DelayMs != 250 {
		t.Errorf("second step did not survive: %+v", steps[1])
	}
}

// A plain action must not grow empty chain keys, so decks that never chain stay
// byte-for-byte what they were.
func TestPlainActionOmitsChainKeys(t *testing.T) {
	blob, err := json.Marshal(Action{Type: "transition"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"steps", "delayMs"} {
		if strings.Contains(string(blob), key) {
			t.Errorf("plain action carries %q: %s", key, blob)
		}
	}
}

// A button with no gate must marshal without the key, so decks that never use
// the feature stay byte-for-byte what they were.
func TestUngatedButtonOmitsEnable(t *testing.T) {
	blob, err := json.Marshal(Button{Label: "Cut", Action: Action{Type: "transition"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(blob); strings.Contains(got, "enable") {
		t.Errorf("ungated button carries an enable key: %s", got)
	}
}
