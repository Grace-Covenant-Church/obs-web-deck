package obsws

import "testing"

func TestCrossfadePosition(t *testing.T) {
	cases := []struct {
		percent int
		want    float64
	}{
		{0, 0},
		{50, 0.5},
		{100, 1},
		// Out-of-range values clamp rather than error, so a hand-edited
		// deck.json cannot produce a request OBS would reject.
		{-20, 0},
		{250, 1},
	}
	for _, c := range cases {
		if got := crossfadePosition(c.percent); got != c.want {
			t.Errorf("crossfadePosition(%d) = %v, want %v", c.percent, got, c.want)
		}
	}
}
