package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/obsws"
)

type conn struct {
	ws   *websocket.Conn
	send chan []byte
	once sync.Once
}

// inbound is the union of every message a phone can send. PTZ axes arrive as
// one message because obs-ptz moves pan, tilt and zoom in a single call.
type inbound struct {
	Type   string         `json:"type"`
	ID     int            `json:"id,omitempty"`
	Action *config.Action `json:"action,omitempty"`

	Pan    float64 `json:"pan,omitempty"`
	Tilt   float64 `json:"tilt,omitempty"`
	Zoom   float64 `json:"zoom,omitempty"`
	Preset int     `json:"preset,omitempty"`
	Save   bool    `json:"save,omitempty"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &conn{ws: ws, send: make(chan []byte, 32)}

	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()

	go c.writeLoop()

	c.push(map[string]any{
		"type":   "hello",
		"config": s.clientConfig(r),
		"state":  s.obs.State(),
	})

	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		remaining := len(s.clients)
		s.mu.Unlock()
		c.close()
		// The last phone leaving must not strand the camera mid-slew.
		if remaining == 0 {
			_ = s.ptz.Stop()
		}
	}()

	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var msg inbound
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		s.dispatch(c, msg)
	}
}

func (s *Server) dispatch(c *conn, msg inbound) {
	var err error

	switch msg.Type {
	case "ptz":
		err = s.ptz.Move(msg.Pan, msg.Tilt, msg.Zoom)
	case "ptzKeepalive":
		s.ptz.Keepalive()
		return
	case "ptzStop":
		err = s.ptz.Stop()
	case "ptzPreset":
		if msg.Save {
			err = s.ptz.PresetSave(msg.Preset)
		} else {
			err = s.ptz.PresetRecall(msg.Preset)
		}
	case "action":
		if msg.Action == nil {
			return
		}
		err = s.runAction(*msg.Action)
	default:
		return
	}

	if err != nil {
		c.push(map[string]any{"type": "error", "id": msg.ID, "message": err.Error()})
	}
}

// ---------- broadcast ----------

func (s *Server) BroadcastState(st obsws.State) {
	s.broadcast(map[string]any{"type": "state", "state": st})
}

func (s *Server) BroadcastConfig(r *http.Request) {
	s.broadcast(map[string]any{"type": "config", "config": s.clientConfig(r)})
}

func (s *Server) broadcast(v any) {
	blob, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		select {
		case c.send <- blob:
		default:
			// A phone too slow to drain its queue is dropped rather than
			// allowed to stall updates for every other client.
			c.close()
		}
	}
}

func (c *conn) push(v any) {
	blob, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- blob:
	default:
	}
}

func (c *conn) writeLoop() {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case blob, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.TextMessage, blob); err != nil {
				return
			}
		case <-ping.C:
			// Keeps NAT and phone power management from quietly dropping an
			// idle socket, which would otherwise look like a frozen deck.
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.send)
		_ = c.ws.Close()
	})
}
