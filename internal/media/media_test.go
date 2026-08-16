package media

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	pmedia "github.com/pion/webrtc/v4/pkg/media"
)

// TestWhipToWhep drives the full media path the phone depends on: a publisher
// hands frames in over WHIP, and a separate subscriber must receive them over
// WHEP. It covers the forwarding logic that would otherwise only be verified
// by pointing real OBS at the binary.
func TestWhipToWhep(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// ---- publisher ----

	pub, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("publisher pc: %v", err)
	}
	defer pub.Close()

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video", "probe")
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	if _, err := pub.AddTrack(track); err != nil {
		t.Fatalf("add track: %v", err)
	}

	if err := negotiate(t, pub, ts.URL+"/test/whip"); err != nil {
		t.Fatalf("whip negotiate: %v", err)
	}

	// Feed frames continuously; the server only learns the track exists once
	// media actually arrives.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(30 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				_ = track.WriteSample(pmedia.Sample{
					Data:     bytes.Repeat([]byte{0x90, 0x80, 0x00}, 40),
					Duration: 30 * time.Millisecond,
				})
			}
		}
	}()

	if !waitFor(5*time.Second, func() bool { return srv.Publishing("test") }) {
		t.Fatal("server never registered the publisher")
	}

	// ---- subscriber ----

	sub, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("subscriber pc: %v", err)
	}
	defer sub.Close()

	if _, err := sub.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("add transceiver: %v", err)
	}

	got := make(chan struct{})
	sub.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		buf := make([]byte, 1500)
		if _, _, err := remote.Read(buf); err == nil {
			select {
			case got <- struct{}{}:
			default:
			}
		}
	})

	if err := negotiate(t, sub, ts.URL+"/test/whep"); err != nil {
		t.Fatalf("whep negotiate: %v", err)
	}

	select {
	case <-got:
		// Media crossed from publisher to subscriber.
	case <-time.After(10 * time.Second):
		t.Fatal("subscriber received no RTP within 10s")
	}
}

// TestWhepWithoutPublisher pins the behaviour the client relies on to decide
// whether to poll or surface an error.
func TestWhepWithoutPublisher(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/idle/whep", "application/sdp", bytes.NewReader([]byte("v=0")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("want 404 with no publisher, got %d", res.StatusCode)
	}
}

// negotiate performs the offer/answer exchange a WHIP or WHEP client would.
func negotiate(t *testing.T, pc *webrtc.PeerConnection, url string) error {
	t.Helper()

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	gathering := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	<-gathering

	res, err := http.Post(url, "application/sdp",
		bytes.NewReader([]byte(pc.LocalDescription().SDP)))
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("%s returned %d: %s", url, res.StatusCode, body)
	}

	return pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: string(body),
	})
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
