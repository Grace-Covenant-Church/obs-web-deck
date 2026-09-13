package qr

import (
	"log"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

/*
Everything the browser cannot do to its own window is done here, by talking X11
directly.

xgb speaks the protocol over a socket in pure Go, so this costs no cgo and no
runtime library - the single static binary survives. It also does what no
command line tool can: an empty input region, which is what actually makes the
badge non-interactable. Clicks land on OBS behind it rather than on a browser
window nobody wants to think about.

Under a Wayland session this talks to XWayland, which is why the browser is
asked for an X11 window in the first place.
*/

type x11 struct {
	conn  *xgb.Conn
	root  xproto.Window
	atoms map[string]xproto.Atom
}

func dialX11() (*x11, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	return &x11{
		conn:  conn,
		root:  screen.Root,
		atoms: map[string]xproto.Atom{},
	}, nil
}

func (x *x11) Close() { x.conn.Close() }

func (x *x11) atom(name string) xproto.Atom {
	if a, ok := x.atoms[name]; ok {
		return a
	}
	reply, err := xproto.InternAtom(x.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return xproto.AtomNone
	}
	x.atoms[name] = reply.Atom
	return reply.Atom
}

/*
pin makes the badge what it is meant to be: no title bar, above everything, in
the corner, and transparent to the mouse.

Order matters, and so does persistence. Chromium writes its own decoration hint
while the window comes up and will happily overwrite ours, so that one is
asserted repeatedly for the few seconds the browser takes to settle. Losing the
decorations also makes Chromium grow the window by what the title bar occupied,
which is why the geometry is only worth setting at the end.
*/
func (x *x11) pin(win xproto.Window, area rect) {
	const settle = 300 * time.Millisecond

	for range 10 {
		x.undecorate(win)
		x.conn.Sync()
		time.Sleep(settle)
	}

	xp, yp := area.badgeCorner()
	xproto.ConfigureWindow(x.conn, win,
		xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(xp), uint32(yp), BadgeSize, BadgeSize})

	x.setState(win, "_NET_WM_STATE_ABOVE", "_NET_WM_STATE_SKIP_TASKBAR")
	x.setState(win, "_NET_WM_STATE_SKIP_PAGER", "")
	x.conn.Sync()

	// Chromium reshapes the window whenever it resizes, so the input region
	// goes on last of all, once the resize above has been dealt with.
	time.Sleep(settle * 2)
	x.clickThrough(win)
	x.conn.Sync()
}

// undecorate asks the window manager for a bare window, through the Motif hint
// every window manager still honours: flags say "the decoration field is
// meaningful", and the decoration field says "none".
func (x *x11) undecorate(win xproto.Window) {
	const flagDecorations = 2

	hints := make([]byte, 20)
	xgb.Put32(hints[0:], flagDecorations)
	// functions, decorations, input mode and status all stay zero.

	motif := x.atom("_MOTIF_WM_HINTS")
	xproto.ChangeProperty(x.conn, xproto.PropModeReplace, win, motif, motif, 32, 5, hints)
}

/*
setState adds up to two _NET_WM_STATE properties.

A mapped window's state belongs to the window manager, so it is asked rather
than told: the client message is what the "Always on Top" menu item sends, and
it is the only thing that sticks.
*/
func (x *x11) setState(win xproto.Window, first, second string) {
	const add = 1
	// Source 2 means a pager rather than the application itself, which is what
	// window managers trust for a state change on a window they already manage.
	const fromPager = 2

	var secondAtom xproto.Atom
	if second != "" {
		secondAtom = x.atom(second)
	}

	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: win,
		Type:   x.atom("_NET_WM_STATE"),
		Data: xproto.ClientMessageDataUnionData32New([]uint32{
			add, uint32(x.atom(first)), uint32(secondAtom), fromPager, 0,
		}),
	}
	xproto.SendEvent(x.conn, false, x.root,
		uint32(xproto.EventMaskSubstructureNotify|xproto.EventMaskSubstructureRedirect),
		string(ev.Bytes()))
}

/*
clickThrough gives the window an empty input region, so the pointer passes
straight through to whatever is behind it. The window still draws; it simply
stops being somewhere a click can land.

Chromium sets input regions of its own and takes ours off again when it does,
so this reads back what stuck rather than assuming. It is the one step worth
complaining about when it fails: without it the badge is a browser window
sitting over OBS, and a stray click lands in it.
*/
func (x *x11) clickThrough(win xproto.Window) {
	if err := shape.Init(x.conn); err != nil {
		log.Printf("[qr] no X shape extension, the badge will take clicks: %v", err)
		return
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		err := shape.RectanglesChecked(x.conn, shape.SoSet, shape.SkInput,
			xproto.ClipOrderingUnsorted, win, 0, 0, nil).Check()
		if err != nil {
			log.Printf("[qr] could not make the badge click-through: %v", err)
			return
		}

		time.Sleep(300 * time.Millisecond)
		if x.inputRegionEmpty(win) {
			return
		}
		if time.Now().After(deadline) {
			log.Print("[qr] the browser keeps reclaiming the badge's input region; it will take clicks")
			return
		}
	}
}

func (x *x11) inputRegionEmpty(win xproto.Window) bool {
	region, err := shape.GetRectangles(x.conn, win, shape.SkInput).Reply()
	return err == nil && region != nil && len(region.Rectangles) == 0
}

/*
waitForWindow polls the window manager's client list for the badge.

The browser takes a second or two to start, and longer on a cold profile, so
this waits rather than assumes. Matching on the title is what the page's
otherwise pointless <title> is for.
*/
func (x *x11) waitForWindow(title string, limit time.Duration) (xproto.Window, bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if win, ok := x.findWindow(title); ok {
			return win, true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return 0, false
}

func (x *x11) findWindow(title string) (xproto.Window, bool) {
	for _, win := range x.clientList() {
		if x.windowName(win) == title {
			return win, true
		}
	}
	return 0, false
}

/*
closeWindow asks the window manager to close the badge.

Killing the process obs-deck started is not enough on its own: a browser that
was already running takes the request over and opens the window itself, and the
process we spawned is gone by then. The window, however, is always findable and
always closeable.
*/
func (x *x11) closeWindow(win xproto.Window) {
	// Timestamp CurrentTime, and source 2 for a pager rather than the
	// application - the same pair the window manager's own close button sends.
	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: win,
		Type:   x.atom("_NET_CLOSE_WINDOW"),
		Data:   xproto.ClientMessageDataUnionData32New([]uint32{0, 2, 0, 0, 0}),
	}
	xproto.SendEvent(x.conn, false, x.root,
		uint32(xproto.EventMaskSubstructureNotify|xproto.EventMaskSubstructureRedirect),
		string(ev.Bytes()))
	x.conn.Sync()
}

func (x *x11) clientList() []xproto.Window {
	reply, err := xproto.GetProperty(x.conn, false, x.root,
		x.atom("_NET_CLIENT_LIST"), xproto.GetPropertyTypeAny, 0, 4096).Reply()
	if err != nil || reply == nil {
		return nil
	}

	out := make([]xproto.Window, 0, len(reply.Value)/4)
	for i := 0; i+4 <= len(reply.Value); i += 4 {
		out = append(out, xproto.Window(xgb.Get32(reply.Value[i:])))
	}
	return out
}

func (x *x11) windowName(win xproto.Window) string {
	for _, prop := range []string{"_NET_WM_NAME", "WM_NAME"} {
		reply, err := xproto.GetProperty(x.conn, false, win, x.atom(prop),
			xproto.GetPropertyTypeAny, 0, 256).Reply()
		if err == nil && reply != nil && len(reply.Value) > 0 {
			return string(reply.Value)
		}
	}
	return ""
}

/*
workArea is where the badge is allowed to sit.

Two questions. RandR says which monitor: on a booth machine the other heads are
usually program and preview feeds, so the badge belongs on the primary one
rather than in the far corner of the desktop as a whole. _NET_WORKAREA then
says how much of it a panel or dock has already claimed, so the badge lands
above a taskbar instead of behind it.
*/
func (x *x11) workArea() rect {
	monitor, ok := x.primaryMonitor()
	if !ok {
		monitor = x.screen()
	}
	if free, ok := x.netWorkArea(); ok {
		return monitor.intersect(free)
	}
	return monitor
}

func (x *x11) primaryMonitor() (rect, bool) {
	if err := randr.Init(x.conn); err != nil {
		return rect{}, false
	}
	primary, err := randr.GetOutputPrimary(x.conn, x.root).Reply()
	if err != nil || primary == nil || primary.Output == 0 {
		return rect{}, false
	}
	info, err := randr.GetOutputInfo(x.conn, primary.Output, 0).Reply()
	if err != nil || info == nil || info.Crtc == 0 {
		return rect{}, false
	}
	crtc, err := randr.GetCrtcInfo(x.conn, info.Crtc, 0).Reply()
	if err != nil || crtc == nil || crtc.Width == 0 {
		return rect{}, false
	}
	return rect{int(crtc.X), int(crtc.Y), int(crtc.Width), int(crtc.Height)}, true
}

// netWorkArea is the desktop's usable area, panels and docks already
// subtracted. One rectangle is listed per virtual desktop; they only differ
// when a panel is pinned to one desktop, so the first is as good as any.
func (x *x11) netWorkArea() (rect, bool) {
	reply, err := xproto.GetProperty(x.conn, false, x.root,
		x.atom("_NET_WORKAREA"), xproto.GetPropertyTypeAny, 0, 4).Reply()
	if err != nil || reply == nil || len(reply.Value) < 16 {
		return rect{}, false
	}
	return rect{
		int(xgb.Get32(reply.Value[0:])),
		int(xgb.Get32(reply.Value[4:])),
		int(xgb.Get32(reply.Value[8:])),
		int(xgb.Get32(reply.Value[12:])),
	}, true
}

func (x *x11) screen() rect {
	screen := xproto.Setup(x.conn).DefaultScreen(x.conn)
	return rect{0, 0, int(screen.WidthInPixels), int(screen.HeightInPixels)}
}
