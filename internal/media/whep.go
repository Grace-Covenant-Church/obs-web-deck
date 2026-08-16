package media

import (
	"log"
	"net/http"

	"github.com/pion/webrtc/v4"
)

// handleWHEP attaches a viewer to whatever the publisher is currently sending.
//
// A 404 here is a normal state, not a failure: it means OBS has not started
// its WHIP output yet, and the client polls on it rather than surfacing an
// error to the operator.
func (s *Server) handleWHEP(w http.ResponseWriter, r *http.Request, name string) {
	offer, err := readOffer(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rm := s.roomFor(name)
	rm.mu.RLock()
	video, audio := rm.video, rm.audio
	rm.mu.RUnlock()

	if video == nil {
		http.Error(w, "no publisher on this path yet", http.StatusNotFound)
		return
	}

	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := pc.AddTrack(video); err != nil {
		_ = pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if audio != nil {
		if _, err := pc.AddTrack(audio); err != nil {
			// Video without audio is still a usable preview.
			log.Printf("[media] add audio track: %v", err)
		}
	}

	rm.mu.Lock()
	rm.viewers++
	count := rm.viewers
	rm.mu.Unlock()
	log.Printf("[media] %s viewer connected (%d watching)", name, count)

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateConnected:
			// A viewer joining mid-GOP would otherwise wait for OBS's next
			// scheduled keyframe, which even at a 1s interval is a visible
			// stall on a control surface.
			rm.requestKeyframe()

		case webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			rm.mu.Lock()
			if rm.viewers > 0 {
				rm.viewers--
			}
			left := rm.viewers
			rm.mu.Unlock()
			log.Printf("[media] %s viewer left (%d watching)", name, left)
			_ = pc.Close()
		}
	})

	sdp, err := answer(pc, offer)
	if err != nil {
		_ = pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeSDP(w, "/"+name+"/whep", sdp)
}
