// Package server exposes the deck UI, its control WebSocket, and the
// WHIP/WHEP media endpoints on a single HTTP listener, so a phone needs to
// know exactly one address and port.
package server

import (
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/media"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/obsws"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/ptz"
)

type Server struct {
	store *config.Store
	obs   *obsws.Client
	ptz   *ptz.Controller
	media *media.Server
	web   fs.FS

	upgrader websocket.Upgrader

	mu      sync.RWMutex
	clients map[*conn]struct{}
}

func New(store *config.Store, obs *obsws.Client, p *ptz.Controller, m *media.Server, web fs.FS) *Server {
	return &Server{
		store: store, obs: obs, ptz: p, media: m, web: web,
		// The deck is a LAN tool served over plain HTTP. An origin check would
		// only break phones reaching it by IP rather than by mDNS name.
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		clients:  map[*conn]struct{}{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/catalog", s.handleCatalog)
	mux.HandleFunc("/api/deck", s.handleDeck)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/qr", s.handleQRPage)
	mux.HandleFunc("/qr.png", s.handleQRPNG)
	mux.HandleFunc("/ws", s.handleWS)

	static := http.FileServer(http.FS(s.web))
	mediaHandler := s.media.Handler()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// WHIP/WHEP live at "/{path}/whip" and "/{path}/whep"; everything else
		// is a static asset baked into the binary.
		p := strings.TrimRight(r.URL.Path, "/")
		if strings.HasSuffix(p, "/whip") || strings.HasSuffix(p, "/whep") {
			mediaHandler.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})

	return mux
}
