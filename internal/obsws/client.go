// Package obsws owns the single connection to OBS.
//
// Phones never speak obs-websocket directly. They talk to this process, which
// keeps the OBS password on the host machine and guarantees every connected
// phone observes identical state.
package obsws

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/general"

	appcfg "github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
)

// errNotConnected is returned rather than panicking when a button is pressed
// during a reconnect window; the UI surfaces it as a transient toast.
var errNotConnected = errors.New("OBS not connected")

type Client struct {
	store   *appcfg.Store
	onState func(State)

	mu      sync.RWMutex
	obs     *goobs.Client
	state   State
	itemIDs map[string]int // "scene\x00source" -> sceneItemId

	// fadeHeld is true while a partial crossfade sits on air. It is not part of
	// State: obs-websocket neither reports the T-bar nor announces it moving, so
	// this is a local memory of a request we sent rather than anything observed,
	// and nothing on a phone can act on it. See resetFade.
	fadeHeld bool

	// transitionWaiters are released when OBS reports a transition has ended.
	// A program change in studio mode is a transition, and what it does to the
	// preview scene is only settled once that finishes. See awaitTransition.
	transitionWaiters []chan struct{}

	stop chan struct{}
	once sync.Once
}

func New(store *appcfg.Store, onState func(State)) *Client {
	return &Client{
		store:   store,
		onState: onState,
		state:   State{Sources: map[string]bool{}, Inputs: map[string]bool{}},
		itemIDs: map[string]int{},
		stop:    make(chan struct{}),
	}
}

// Run maintains the connection, reconnecting until Close is called.
func (c *Client) Run() {
	for {
		select {
		case <-c.stop:
			return
		default:
		}

		set := c.store.Settings()
		obs, err := goobs.New(set.OBS.Host, goobs.WithPassword(set.OBS.Password))
		if err != nil {
			c.mu.Lock()
			c.state.Connected = false
			c.state.Error = err.Error()
			c.mu.Unlock()
			c.push()

			if !c.sleep(set.ReconnectDelay()) {
				return
			}
			continue
		}

		c.mu.Lock()
		c.obs = obs
		c.state.Connected = true
		c.state.Error = ""
		c.mu.Unlock()

		if err := c.Refresh(); err != nil {
			log.Printf("[obs] initial refresh: %v", err)
		}
		c.push()
		log.Printf("[obs] connected to %s", set.OBS.Host)

		c.pump(obs) // blocks until the connection drops

		c.mu.Lock()
		c.obs = nil
		c.state.Connected = false
		c.mu.Unlock()
		c.push()
		log.Printf("[obs] disconnected, retrying in %s", set.ReconnectDelay())

		if !c.sleep(set.ReconnectDelay()) {
			return
		}
	}
}

// sleep waits out the reconnect delay, reporting false if we are shutting down.
func (c *Client) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-c.stop:
		return false
	}
}

func (c *Client) Close() {
	c.once.Do(func() { close(c.stop) })
	c.mu.Lock()
	obs := c.obs
	c.mu.Unlock()
	if obs != nil {
		_ = obs.Disconnect()
	}
}

func (c *Client) client() *goobs.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.obs
}

// CallVendor satisfies ptz.VendorCaller, routing plugin requests such as
// obs-ptz's over the same connection.
func (c *Client) CallVendor(vendor, request string, data map[string]any) error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	_, err := obs.General.CallVendorRequest(&general.CallVendorRequestParams{
		VendorName:  &vendor,
		RequestType: &request,
		RequestData: data,
	})
	return err
}
