package server

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

/*
Live integration check against a running obs-deck with a real OBS behind it.

Skipped by default because it needs both processes up and it briefly changes
the program scene. Run it with:

	OBSDECK_LIVE=127.0.0.1:8080 go test ./internal/server/ -run TestLive -v
*/
func TestLiveSceneSwitch(t *testing.T) {
	addr := os.Getenv("OBSDECK_LIVE")
	if addr == "" {
		t.Skip("set OBSDECK_LIVE=host:port to run against a live server")
	}

	ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	hello := readMsg(t, ws)
	state := hello["state"].(map[string]any)
	if !state["connected"].(bool) {
		t.Fatal("server is not connected to OBS")
	}

	scenes := toStrings(state["sceneList"])
	if len(scenes) < 2 {
		t.Skipf("need at least 2 scenes to switch between, have %d", len(scenes))
	}

	original, _ := state["programScene"].(string)
	target := scenes[0]
	if target == original {
		target = scenes[1]
	}
	t.Logf("scenes=%v program=%q switching to %q", scenes, original, target)

	// Always put the operator's scene back, even if an assertion fails.
	defer func() {
		if original != "" {
			send(t, ws, map[string]any{
				"type":   "action",
				"action": map[string]any{"type": "scene", "scene": original},
			})
			time.Sleep(300 * time.Millisecond)
		}
	}()

	send(t, ws, map[string]any{
		"type":   "action",
		"action": map[string]any{"type": "scene", "scene": target},
	})

	if !waitForScene(t, ws, target) {
		t.Fatalf("OBS never reported %q as the program scene", target)
	}
	t.Logf("confirmed program scene changed to %q", target)
}

func waitForScene(t *testing.T, ws *websocket.Conn, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = ws.SetReadDeadline(deadline)
		m := readMsg(t, ws)
		if m["type"] != "state" {
			continue
		}
		st, _ := m["state"].(map[string]any)
		if st != nil && st["programScene"] == want {
			return true
		}
	}
	return false
}

func readMsg(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func send(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	if err := ws.WriteJSON(v); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, s := range arr {
		if str, ok := s.(string); ok {
			out = append(out, str)
		}
	}
	return out
}
