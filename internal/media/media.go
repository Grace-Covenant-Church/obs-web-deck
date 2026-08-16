// Package media embeds a minimal WHIP/WHEP server so OBS can publish directly
// into this process and phones can play from it, with no MediaMTX alongside.
//
// It deliberately does not transcode. RTP arriving from OBS is forwarded
// verbatim to viewers, so the cost is a buffer copy rather than a decode and
// re-encode round trip.
package media

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

type Server struct {
	api *webrtc.API

	mu    sync.RWMutex
	rooms map[string]*room
}

// room is one publish path and the viewers attached to it. The tracks are
// interfaces rather than a concrete type because the two ingest routes build
// them differently: WHIP forwards RTP verbatim, MPEG-TS hands over frames and
// lets Pion packetise.
type room struct {
	mu sync.RWMutex

	video webrtc.TrackLocal
	audio webrtc.TrackLocal

	// pub is set only for WHIP, where there is a peer to ask for a keyframe.
	// An MPEG-TS feed has no back channel, so keyframes arrive only as often
	// as the encoder's interval.
	pub       *webrtc.PeerConnection
	videoSSRC webrtc.SSRC

	viewers int
}

func NewServer() (*Server, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}

	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		return nil, err
	}

	se := webrtc.SettingEngine{}
	// On a LAN there is no NAT to traverse, so long ICE timeouts only delay
	// the point at which a dead peer is noticed.
	se.SetICETimeouts(5*time.Second, 5*time.Second, time.Second)

	return &Server{
		api: webrtc.NewAPI(
			webrtc.WithMediaEngine(m),
			webrtc.WithInterceptorRegistry(i),
			webrtc.WithSettingEngine(se),
		),
		rooms: map[string]*room{},
	}, nil
}

func (s *Server) roomFor(name string) *room {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[name]
	if !ok {
		r = &room{}
		s.rooms[name] = r
	}
	return r
}

// Publishing reports whether OBS is currently feeding the given path.
func (s *Server) Publishing(name string) bool {
	s.mu.RLock()
	r, ok := s.rooms[name]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.video != nil
}

// Handler routes "/{path}/whip" and "/{path}/whep", matching the layout
// MediaMTX uses so an external server can be swapped in without the client
// changing anything.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		name, verb := parts[0], parts[1]

		// Negotiation is a plain SDP POST; OPTIONS is the CORS preflight some
		// players send first.
		if r.Method == http.MethodOptions {
			setCORS(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		switch verb {
		case "whip":
			s.handleWHIP(w, r, name)
		case "whep":
			s.handleWHEP(w, r, name)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func setCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS, DELETE")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func readOffer(r *http.Request) (string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", errors.New("empty SDP offer")
	}
	return string(body), nil
}

// answer completes negotiation and blocks until ICE gathering finishes, so the
// SDP we return already carries every candidate. On a LAN that costs
// milliseconds and spares us implementing a trickle endpoint.
func answer(pc *webrtc.PeerConnection, offer string) (string, error) {
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: offer,
	}); err != nil {
		return "", err
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	gathering := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		return "", err
	}
	<-gathering
	return pc.LocalDescription().SDP, nil
}

// writeSDP sends the 201 + SDP body that both WHIP and WHEP clients expect.
func writeSDP(w http.ResponseWriter, location, sdp string) {
	setCORS(w)
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(sdp))
}

// requestKeyframe asks the publisher for an immediate IDR via RTCP PLI.
func (r *room) requestKeyframe() {
	r.mu.RLock()
	pub, ssrc := r.pub, r.videoSSRC
	r.mu.RUnlock()
	if pub == nil {
		return
	}
	_ = pub.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)},
	})
}
