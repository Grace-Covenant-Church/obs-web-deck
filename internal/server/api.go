package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
)

// clientConfig is everything a phone needs to render itself.
type clientConfig struct {
	Deck       *config.Deck `json:"deck"`
	WhepURL    string       `json:"whepUrl"`
	VideoMode  string       `json:"videoMode"`
	Publishing bool         `json:"publishing"`
	PTZEnabled bool         `json:"ptzEnabled"`

	// Grid bounds the UI offers, and how many buttons the current width is
	// hiding, so the picker can warn rather than silently swallow them.
	MinCols int `json:"minCols"`
	MaxCols int `json:"maxCols"`
	OffGrid int `json:"offGrid"`
}

// whepURLFor derives the playback URL from the hostname the phone actually
// used, so one config works over raw IP, mDNS name, or a tunnel.
func (s *Server) whepURLFor(r *http.Request) string {
	v := s.store.Settings().Video

	switch v.Mode {
	case "none":
		return ""
	case "external":
		if v.ExternalWhepURL != "" {
			return v.ExternalWhepURL
		}
		host := r.Host
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		port := v.ExternalPort
		if port == 0 {
			port = 8889
		}
		return "http://" + host + ":" + strconv.Itoa(port) + "/" + v.StreamPath + "/whep"
	default:
		// Built in: same origin, same port, so no host juggling at all.
		return "/" + v.StreamPath + "/whep"
	}
}

func (s *Server) clientConfig(r *http.Request) clientConfig {
	set := s.store.Settings()
	deck := s.store.Deck()
	return clientConfig{
		Deck:       deck,
		MinCols:    config.MinCols,
		MaxCols:    config.MaxCols,
		OffGrid:    deck.OffGrid(),
		WhepURL:    s.whepURLFor(r),
		VideoMode:  set.Video.Mode,
		Publishing: s.media.Publishing(set.Video.StreamPath),
		PTZEnabled: s.ptz.Enabled(),
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.clientConfig(r))
}

// handleHealth is the first thing to check when the preview is black: it says
// whether OBS is connected, whether it is actually recording (which is what
// feeds the preview in mpegts mode), and whether any media has arrived.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	st := s.obs.State()
	set := s.store.Settings()
	writeJSON(w, map[string]any{
		"ok":         true,
		"obs":        st.Connected,
		"error":      st.Error,
		"streaming":  st.Streaming,
		"recording":  st.Recording,
		"videoMode":  set.Video.Mode,
		"publishing": s.media.Publishing(set.Video.StreamPath),
	})
}

func (s *Server) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	cat, err := s.obs.Catalog()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, cat)
}

// handleDeck persists a layout edited in the browser, then pushes it to every
// connected phone so a second device updates without a reload.
func (s *Server) handleDeck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var deck config.Deck
	if err := json.NewDecoder(r.Body).Decode(&deck); err != nil {
		http.Error(w, "bad deck: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SaveDeck(&deck); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// New feedback targets mean new scene items and inputs to watch.
	if err := s.obs.Refresh(); err != nil {
		log.Printf("[deck] refresh after save: %v", err)
	}
	s.BroadcastConfig(r)
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
