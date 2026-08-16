package media

import (
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/pion/webrtc/v4"
)

// handleWHIP accepts the publisher, which in practice is OBS's WHIP output.
//
// Each inbound track is mirrored onto a local track that viewers subscribe to.
// Packets are copied through untouched, so whatever OBS encoded is exactly
// what the phone decodes.
func (s *Server) handleWHIP(w http.ResponseWriter, r *http.Request, name string) {
	offer, err := readOffer(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rm := s.roomFor(name)

	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		local, err := webrtc.NewTrackLocalStaticRTP(
			remote.Codec().RTPCodecCapability, remote.Kind().String(), "obs-deck")
		if err != nil {
			log.Printf("[media] local track: %v", err)
			return
		}

		rm.mu.Lock()
		if remote.Kind() == webrtc.RTPCodecTypeVideo {
			rm.video = local
			rm.videoSSRC = remote.SSRC()
			rm.pub = pc
		} else {
			rm.audio = local
		}
		rm.mu.Unlock()

		log.Printf("[media] %s publishing %s (%s)", name, remote.Kind(), remote.Codec().MimeType)

		buf := make([]byte, 1500)
		for {
			n, _, err := remote.Read(buf)
			if err != nil {
				return
			}
			// ErrClosedPipe only means no viewers are attached right now,
			// which is normal and must not tear down the publisher.
			if _, err := local.Write(buf[:n]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				return
			}
		}
	})

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st != webrtc.PeerConnectionStateFailed && st != webrtc.PeerConnectionStateClosed {
			return
		}
		rm.mu.Lock()
		rm.video, rm.audio, rm.pub = nil, nil, nil
		rm.mu.Unlock()
		_ = pc.Close()
		log.Printf("[media] %s publisher disconnected", name)
	})

	sdp, err := answer(pc, offer)
	if err != nil {
		_ = pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeSDP(w, "/"+name+"/whip", sdp)
}
