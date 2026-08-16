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

Two variants share one page. The default is a full-screen card meant for a
browser window on the operator's machine. `?overlay=1` strips the chrome and
makes the background transparent so the same URL can be dropped straight into
an OBS Browser Source and parked in a corner of a scene.
*/
func (s *Server) handleQRPage(w http.ResponseWriter, r *http.Request) {
	url, err := s.JoinURL()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	overlay := r.URL.Query().Get("overlay") != ""

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, qrPage, cssFor(overlay), url, url)
}

func cssFor(overlay bool) string {
	if overlay {
		return overlayCSS
	}
	return windowCSS
}

const windowCSS = `
  body {
    background: #08090b;
    color: #e8ecf1;
    display: grid;
    place-items: center;
    min-height: 100dvh;
  }
  .card {
    background: #101317;
    border: 1px solid #262c35;
    border-radius: 18px;
    padding: 28px 32px 24px;
    text-align: center;
  }
  h1 { font-size: 15px; letter-spacing: .12em; text-transform: uppercase; color: #7c8794; }
  .hint { color: #7c8794; font-size: 12px; margin-top: 14px; max-width: 320px; }
`

const overlayCSS = `
  /* Transparent so an OBS Browser Source composites cleanly over a scene. */
  body { background: transparent; color: #fff; }
  .card {
    background: rgba(0,0,0,.55);
    border-radius: 14px;
    padding: 14px;
    text-align: center;
    display: inline-block;
  }
  h1 { font-size: 11px; letter-spacing: .12em; text-transform: uppercase; opacity: .8; }
  img { width: 190px; height: 190px; }
  .hint { display: none; }
`

const qrPage = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OBS Deck — Scan to connect</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font: 500 14px/1.4 ui-sans-serif, system-ui, -apple-system, sans-serif; }
  img {
    width: 260px; height: 260px; display: block; margin: 14px auto 0;
    background: #fff; border-radius: 10px; padding: 10px;
    /* Nearest-neighbour keeps the modules crisp when scaled. */
    image-rendering: pixelated;
  }
  .url {
    margin-top: 14px;
    font: 700 15px/1 ui-monospace, SFMono-Regular, Menlo, monospace;
    color: #22c55e;
  }
%s
</style>
</head><body>
  <div class="card">
    <h1>Scan to open the deck</h1>
    <img src="/qr.png?size=512" alt="QR code for %s">
    <div class="url">%s</div>
    <p class="hint">Phone must be on the same network. If it will not load,
      check that the phone is on the same subnet as this machine.</p>
  </div>
</body></html>
`
