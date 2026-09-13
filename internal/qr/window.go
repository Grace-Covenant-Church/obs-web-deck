package qr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/*
The desktop QR is a badge, not a page: a small square of code pinned to the
bottom-right corner of the operator's screen, above whatever else is running,
with nothing on it to read and nothing to click. Someone glances at it, a
volunteer scans it, and it never gets in the way of OBS.

Chromium in app mode draws it - no tabs, no address bar, and it takes its own
size and position from the command line. The window itself is then taken over
in x11.go, which is what makes it bare, keeps it on top and lets the mouse
through it.
*/

// BadgeSize is the edge length of the QR window, in pixels.
const BadgeSize = 250

// badgeLift keeps the badge clear of the very bottom of the screen, where a
// dock or OBS's own status bar tends to sit.
const badgeLift = 75

// WindowTitle is the <title> the QR page carries. Nothing shows it once the
// decorations are gone; it is how the window is found again after the browser
// opens it.
const WindowTitle = "obs-deck-qr"

// Chromium derivatives, in the order they are worth trying. Only the family
// matters: they all understand --app and the geometry flags.
var chromiumBinaries = []string{
	"chromium",
	"chromium-browser",
	"google-chrome",
	"google-chrome-stable",
	"brave-browser",
	"microsoft-edge",
	"vivaldi-stable",
}

/*
OpenWindow shows the QR page as a badge and returns a function that closes it
again.

Without a Chromium-family browser it falls back to handing the URL to the
desktop, which opens an ordinary tab: still scannable, just not placed or
pinned. The returned stop function is always safe to call.
*/
func OpenWindow(url string) (stop func(), err error) {
	noop := func() {}

	bin, ok := chromium()
	if !ok {
		return noop, OpenBrowser(url)
	}

	x, xerr := dialX11()

	args := []string{
		"--app=" + url,
		fmt.Sprintf("--window-size=%d,%d", BadgeSize, BadgeSize),
		// A profile of our own. Pointed at the operator's usual one, an
		// already-running browser would handle the request itself and drop
		// the geometry flags on the floor.
		"--user-data-dir=" + profileDir(bin),
		"--no-first-run",
		"--no-default-browser-check",
	}
	if xerr == nil {
		// Wayland gives a window no say in where it lands or what sits above
		// it, so where there is an X server to talk to - XWayland included -
		// the badge is asked for as an X11 window and pinned afterwards.
		bx, by := x.workArea().badgeCorner()
		args = append(args,
			"--ozone-platform=x11",
			// Opening in the right place as well as moving it there later
			// keeps the badge from appearing mid-screen for a moment.
			fmt.Sprintf("--window-position=%d,%d", bx, by))
	}

	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		if xerr == nil {
			x.Close()
		}
		return noop, err
	}
	// Reap it rather than leave a zombie for as long as obs-deck runs.
	go func() { _ = cmd.Wait() }()

	if xerr == nil {
		// The browser has to map the window before anything can be done to it,
		// and a cold profile takes its time.
		go func() {
			defer x.Close()
			if win, ok := x.waitForWindow(WindowTitle, 30*time.Second); ok {
				x.pin(win, x.workArea())
			}
		}()
	}

	return func() {
		// The window first, since it is not necessarily this process that owns
		// it, then the process, since it is not necessarily the window that
		// ends it.
		closeBadge()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}, nil
}

// closeBadge shuts the badge window through the window manager, on a
// connection of its own: the one used to pin it is long closed by now.
func closeBadge() {
	x, err := dialX11()
	if err != nil {
		return
	}
	defer x.Close()

	if win, ok := x.findWindow(WindowTitle); ok {
		x.closeWindow(win)
	}
}

func chromium() (string, bool) {
	for _, name := range chromiumBinaries {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

/*
profileDir is where the badge's browser keeps its profile.

Anywhere will do, so long as it is not the operator's own profile - a browser
already running on that one answers the request itself, which loses the window
flags and leaves nothing for obs-deck to kill afterwards.

A snap browser is confined to its own tree and silently falls back to its
default profile for a path outside it, which is the same failure with no
warning, so a snap gets a directory inside its home instead.
*/
func profileDir(bin string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "obs-web-deck", "qr-window")

	if strings.HasPrefix(bin, "/snap/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, "snap", filepath.Base(bin), "common", "obs-deck-qr")
		}
	}

	// Left to create it itself, a confined browser may not be allowed to.
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

type rect struct{ x, y, w, h int }

// badgeCorner is the top-left position that puts a badge in the bottom-right
// corner of the area, lifted clear of the bottom edge.
func (r rect) badgeCorner() (x, y int) {
	return r.x + r.w - BadgeSize, r.y + r.h - BadgeSize - badgeLift
}

func (r rect) intersect(o rect) rect {
	x, y := max(r.x, o.x), max(r.y, o.y)
	right, bottom := min(r.x+r.w, o.x+o.w), min(r.y+r.h, o.y+o.h)
	if right <= x || bottom <= y {
		// Nothing in common, which means one of the two answers is stale. The
		// monitor is the safer one: a badge in the wrong corner still beats a
		// badge placed off screen entirely.
		return r
	}
	return rect{x, y, right - x, bottom - y}
}
