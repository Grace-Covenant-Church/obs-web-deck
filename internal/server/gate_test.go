package server

import (
	"strings"
	"testing"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
)

func gateDeck() *config.Deck {
	return &config.Deck{
		Grid: config.Grid{Cols: 3, Rows: 2},
		Pages: []config.Page{
			{ID: "main", Buttons: []config.Button{
				{Row: 0, Col: 0, Label: "Lyrics",
					Enable: &config.Enable{Scenes: []string{"Worship"}}},
				{Row: 0, Col: 1, Label: "Cut",
					Enable: &config.Enable{Scenes: []string{"Countdown"}, Invert: true}},
				{Row: 0, Col: 2, Label: "Record"},
			}},
			// Same coordinates, no gate: the page has to be part of the match
			// or a press from here would answer for main's gated button.
			{ID: "other", Buttons: []config.Button{
				{Row: 0, Col: 0, Label: "Elsewhere"},
			}},
		},
	}
}

func TestGateClosed(t *testing.T) {
	deck := gateDeck()

	for _, c := range []struct {
		name    string
		scene   string
		from    *origin
		blocked bool
	}{
		{"gated button in its scene", "Worship", &origin{"main", 0, 0}, false},
		{"gated button out of its scene", "Sermon", &origin{"main", 0, 0}, true},
		{"inverted button in the excluded scene", "Countdown", &origin{"main", 0, 1}, true},
		{"inverted button elsewhere", "Worship", &origin{"main", 0, 1}, false},
		{"ungated button", "Countdown", &origin{"main", 0, 2}, false},

		// A press with no tile behind it: the PTZ strip sends these.
		{"no origin", "Sermon", nil, false},
		// Generated autoScenes tiles are in no deck, and a button deleted
		// under a stale phone cannot be judged. Both fail open.
		{"empty cell", "Sermon", &origin{"main", 1, 2}, false},
		{"unknown page", "Sermon", &origin{"ghost", 0, 0}, false},
		// The gate belongs to the button, not to the coordinates.
		{"same cell on another page", "Sermon", &origin{"other", 0, 0}, false},
		// No scene on air yet: an "only" gate cannot be satisfied.
		{"no program scene", "", &origin{"main", 0, 0}, true},
	} {
		why := gateClosed(deck, c.scene, c.from)
		if blocked := why != ""; blocked != c.blocked {
			t.Errorf("%s: blocked = %v (%q), want %v", c.name, blocked, why, c.blocked)
		}
	}
}

// The refusal is the only thing the operator sees when a stale press is
// dropped, so it has to name the button and the scene that stopped it.
func TestGateClosedMessage(t *testing.T) {
	why := gateClosed(gateDeck(), "Sermon", &origin{"main", 0, 0})
	for _, want := range []string{"Lyrics", "Sermon"} {
		if !strings.Contains(why, want) {
			t.Errorf("refusal %q does not mention %q", why, want)
		}
	}

	// With nothing on air there is no scene to name, so it must not say so.
	if why := gateClosed(gateDeck(), "", &origin{"main", 0, 0}); strings.Contains(why, " in ") {
		t.Errorf("refusal names a scene that does not exist: %q", why)
	}
}

// A deck that has not loaded yet must not block the deck outright.
func TestGateClosedNilDeck(t *testing.T) {
	if why := gateClosed(nil, "Sermon", &origin{"main", 0, 0}); why != "" {
		t.Errorf("nil deck blocked a press: %q", why)
	}
}
