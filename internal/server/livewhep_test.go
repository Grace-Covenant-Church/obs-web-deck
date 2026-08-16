package server

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

/*
Subscribes to a running obs-web-deck over WHEP and waits for real RTP.

This is the end-to-end check for the MPEG-TS path: OBS must be recording to
the UDP endpoint, and the frames must come back out as WebRTC. A browser is a
poor instrument for this, because a headless one may lack H264 entirely and a
black pane tells you nothing about where it broke.

	OBSDECK_LIVE=127.0.0.1:8080 go test ./internal/server/ -run TestLiveWhep -v
*/
func TestLiveWhepReceivesMedia(t *testing.T) {
	addr := os.Getenv("OBSDECK_LIVE")
	if addr == "" {
		t.Skip("set OBSDECK_LIVE=host:port to run against a live server")
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("peer connection: %v", err)
	}
	defer pc.Close()

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("video transceiver: %v", err)
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("audio transceiver: %v", err)
	}

	got := make(chan string, 4)
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		buf := make([]byte, 1500)
		if _, _, err := remote.Read(buf); err == nil {
			select {
			case got <- remote.Codec().MimeType:
			default:
			}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	gathering := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local: %v", err)
	}
	<-gathering

	res, err := http.Post("http://"+addr+"/preview/whep", "application/sdp",
		bytes.NewReader([]byte(pc.LocalDescription().SDP)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode == http.StatusNotFound {
		t.Fatalf("no publisher: OBS is not sending to the ingest endpoint")
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("WHEP returned %d: %s", res.StatusCode, body)
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: string(body),
	}); err != nil {
		t.Fatalf("set remote: %v", err)
	}

	select {
	case mime := <-got:
		t.Logf("received media: %s", mime)
	case <-time.After(15 * time.Second):
		t.Fatal("negotiated but no RTP arrived within 15s")
	}
}
