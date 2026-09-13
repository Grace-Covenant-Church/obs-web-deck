package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/qr"
)

// JoinURL is the address handed out to phones: this machine's LAN address on
// the port we serve from.
func (s *Server) JoinURL() (string, error) {
	return qr.URL(s.store.Settings().Server.Port)
}

func (s *Server) handleQRPNG(w http.ResponseWriter, r *http.Request) {
	url, err := s.JoinURL()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	size := 512
	if v, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && v >= 64 && v <= 2048 {
		size = v
	}

	png, err := qr.PNG(url, size)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	// The LAN address can change with DHCP, so this must not be cached.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

/*
handleQRPage serves the join screen.

Two variants share one page, and neither carries any text: a QR that has to be
read by a human is a failed QR, and both of these live on top of something the
operator is actually looking at. The default fills its window edge to edge, for
the badge obs-deck parks in the corner of the desktop. `?overlay=1` keeps the
code on a white plate over a transparent background, so the same URL can be
dropped into an OBS Browser Source and composited into a scene.
*/
func (s *Server) handleQRPage(w http.ResponseWriter, r *http.Request) {
	// Rendering a page whose only content is a QR of an address we cannot
	// work out would just show a broken image.
	if _, err := s.JoinURL(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	overlay := r.URL.Query().Get("overlay") != ""

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if overlay {
		fmt.Fprintf(w, qrPage, qr.WindowTitle, overlayCSS, overlaySize)
		return
	}
	fmt.Fprintf(w, qrPage, qr.WindowTitle, windowCSS, qr.BadgeSize)
}

// The badge asks for the PNG at its own size so the modules land on whole
// pixels. The overlay is scaled by whatever the OBS source is sized to, so it
// asks for something larger to scale up from.
const overlaySize = 512

const windowCSS = `
  body { background: #fff; }
  img { width: 100%; height: 100%; }
`

const overlayCSS = `
  /* Transparent so an OBS Browser Source composites cleanly over a scene,
     with the code itself on a plate so it still scans over a dark shot. */
  body { background: transparent; }
  img {
    width: 100%; height: 100%;
    background: #fff; border-radius: 14px; padding: 3%;
  }
`

const qrPage = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
  html, body { margin: 0; height: 100%%; overflow: hidden; }
  /* Nothing here is meant to be clicked, dragged or selected. */
  body { pointer-events: none; user-select: none; -webkit-user-select: none; }
  img {
    display: block; box-sizing: border-box; object-fit: contain;
    -webkit-user-drag: none;
    /* Nearest-neighbour keeps the modules crisp when scaled. */
    image-rendering: pixelated;
  }
%s
</style>
</head><body oncontextmenu="return false">
  <img src="/qr.png?size=%d" alt="">
</body></html>
`
