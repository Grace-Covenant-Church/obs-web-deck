package server

import (
	"testing"
	"time"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
)

// A chain holds the pressing phone's read loop, so the waits it can ask for are
// bounded twice: per step, and across the whole chain.
func TestStepDelay(t *testing.T) {
	for _, c := range []struct {
		name   string
		ms     int
		i      int
		waited time.Duration
		want   time.Duration
	}{
		// The press itself is the start of the chain; delaying step one only
		// makes the button feel dropped.
		{"first step never waits", 500, 0, 0, 0},
		{"no delay asked for", 0, 1, 0, 0},
		{"negative delay", -200, 1, 0, 0},
		{"ordinary delay", 300, 1, 0, 300 * time.Millisecond},
		{"clamped to the per-step cap", 30000, 1, 0, maxStepDelay},
		// The chain budget is what one step is left after the others.
		{"trimmed to what the chain has left", 2000, 3, 4 * time.Second, time.Second},
		{"nothing left", 2000, 5, maxChainDelay, 0},
		{"over budget already", 2000, 5, 9 * time.Second, 0},
	} {
		if got := stepDelay(c.ms, c.i, c.waited); got != c.want {
			t.Errorf("%s: stepDelay(%d, %d, %v) = %v, want %v",
				c.name, c.ms, c.i, c.waited, got, c.want)
		}
	}
}

// The whole point of the cap: no chain can hold a connection for longer than
// the budget however its delays are spread.
func TestChainDelayBudget(t *testing.T) {
	var waited time.Duration
	for i := 0; i < maxChainSteps; i++ {
		waited += stepDelay(60000, i, waited)
	}
	if waited > maxChainDelay {
		t.Errorf("a chain of maximum delays waited %v, over the %v budget", waited, maxChainDelay)
	}
}

// Guards that fire before any request reaches OBS, so they can be checked
// without one. A Server with no OBS client is enough: none of these get far
// enough to touch it.
func TestRunChainRejects(t *testing.T) {
	s := &Server{}

	for _, c := range []struct {
		name  string
		steps []config.Action
	}{
		{"no steps", nil},
		{"empty steps", []config.Action{}},
		{"nested chain", []config.Action{
			{Type: "chain", Steps: []config.Action{{Type: "transition"}}},
		}},
		{"too many steps", make([]config.Action, maxChainSteps+1)},
	} {
		if err := s.runChain(c.steps); err == nil {
			t.Errorf("%s: ran without complaint", c.name)
		}
	}
}

/* A chain of chains has to be refused before it is walked into, and the refusal
 * has to come back rather than spend the read loop descending. Go values cannot
 * actually be cyclic, so this is depth rather than a true loop - which is the
 * shape a hand-edited deck.json can produce. */
func TestRunChainRefusesDepthPromptly(t *testing.T) {
	nested := []config.Action{{Type: "transition"}}
	for range 200 {
		nested = []config.Action{{Type: "chain", Steps: nested}}
	}

	done := make(chan error, 1)
	go func() { done <- (&Server{}).runChain(nested) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a nested chain was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a nested chain did not return")
	}
}
