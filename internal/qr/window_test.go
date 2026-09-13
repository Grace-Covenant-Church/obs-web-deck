package qr

import "testing"

// A taskbar reserves part of the screen and the badge has to end up above it
// rather than behind it, which is why the work area is consulted as well as
// the monitor.
func TestIntersectClipsMonitorToWorkArea(t *testing.T) {
	monitor := rect{0, 0, 1920, 1080}
	free := rect{0, 0, 5760, 1028}

	got := monitor.intersect(free)
	if want := (rect{0, 0, 1920, 1028}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	x, y := got.badgeCorner()
	if x != 1670 || y != 703 {
		t.Fatalf("badge at %d,%d, want 1670,703", x, y)
	}
	if x+BadgeSize > got.x+got.w || y+BadgeSize+badgeLift > got.y+got.h {
		t.Fatalf("badge extends past the work area")
	}
}

// The badge belongs on the operator's screen. On a booth machine the other
// heads are program and preview feeds, so a monitor offset into the desktop
// has to keep its own corner.
func TestBadgeCornerOnSecondMonitor(t *testing.T) {
	x, y := rect{1920, 0, 1920, 1080}.badgeCorner()
	if x != 3590 || y != 755 {
		t.Fatalf("badge at %d,%d, want 3590,755", x, y)
	}
}

// Disjoint rectangles mean one of the two answers is stale; placing the badge
// off screen would be worse than placing it in the wrong corner.
func TestIntersectFallsBackToMonitorWhenDisjoint(t *testing.T) {
	monitor := rect{3840, 0, 1920, 1080}
	if got := monitor.intersect(rect{0, 0, 1920, 1080}); got != monitor {
		t.Fatalf("got %+v, want %+v", got, monitor)
	}
}
