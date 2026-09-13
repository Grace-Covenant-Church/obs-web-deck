package obsws

import (
	"log"
	"strings"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/events"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/typedefs"
)

// State is the snapshot broadcast to every phone whenever anything changes.
type State struct {
	Connected    bool            `json:"connected"`
	Error        string          `json:"error,omitempty"`
	StudioMode   bool            `json:"studioMode"`
	ProgramScene string          `json:"programScene"`
	PreviewScene string          `json:"previewScene"`
	Streaming    bool            `json:"streaming"`
	Recording    bool            `json:"recording"`
	RecordPaused bool            `json:"recordPaused"`
	Sources      map[string]bool `json:"sources"` // "scene\x00source" -> visible
	Inputs       map[string]bool `json:"inputs"`  // input name -> muted

	// SceneList is every scene in the current collection, in the order OBS
	// displays them. Pages marked autoScenes render straight from this, so a
	// scene added or renamed in OBS shows up without editing the deck.
	SceneList []string `json:"sceneList"`
}

// itemKey joins a scene and source with a NUL, which cannot collide with any
// character OBS permits in a name. The client builds the same key.
func itemKey(scene, source string) string { return scene + "\x00" + source }

func (c *Client) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.clone()
}

func (s State) clone() State {
	out := s
	out.Sources = make(map[string]bool, len(s.Sources))
	for k, v := range s.Sources {
		out.Sources[k] = v
	}
	out.Inputs = make(map[string]bool, len(s.Inputs))
	for k, v := range s.Inputs {
		out.Inputs[k] = v
	}
	out.SceneList = append([]string(nil), s.SceneList...)
	return out
}

// sceneNames flattens OBS's scene array into display order. OBS returns it
// bottom-up, which is the reverse of how the scene list reads on screen.
func sceneNames(scenes []*typedefs.Scene) []string {
	out := make([]string, 0, len(scenes))
	for i := len(scenes) - 1; i >= 0; i-- {
		if scenes[i] != nil {
			out = append(out, scenes[i].SceneName)
		}
	}
	return out
}

func (c *Client) push() {
	if c.onState != nil {
		c.onState(c.State())
	}
}

// pump drains the event channel until it closes, which is how goobs signals a
// dropped connection.
func (c *Client) pump(obs *goobs.Client) {
	for ev := range obs.IncomingEvents {
		changed, resync := c.handle(ev)
		if resync {
			if err := c.refreshSceneItems(); err != nil {
				log.Printf("[obs] scene item resync: %v", err)
			}
		}
		if changed {
			c.push()
		}
	}
}

// handle applies one event under the lock and reports whether subscribers need
// telling, and whether per-scene item state has to be re-read.
func (c *Client) handle(ev any) (changed, resyncItems bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch e := ev.(type) {
	case *events.CurrentProgramSceneChanged:
		c.state.ProgramScene = e.SceneName
		// Item visibility is per-scene, so a scene change invalidates it.
		return true, true

	case *events.SceneListChanged:
		c.state.SceneList = sceneNames(e.Scenes)

	case *events.CurrentPreviewSceneChanged:
		c.state.PreviewScene = e.SceneName

	case *events.SceneTransitionEnded:
		// Changes nothing a phone can see; it is what a program change waits
		// on before putting the staged scene back. See awaitTransition.
		c.transitionDone()
		return false, false

	case *events.StudioModeStateChanged:
		c.state.StudioMode = e.StudioModeEnabled

	case *events.StreamStateChanged:
		c.state.Streaming = e.OutputActive

	case *events.RecordStateChanged:
		c.state.Recording = e.OutputActive
		// This event carries no paused bool; the state string is the only
		// signal that a recording is held rather than stopped.
		if e.OutputState != "" {
			c.state.RecordPaused = strings.Contains(e.OutputState, "PAUSED")
		}

	case *events.SceneItemEnableStateChanged:
		prefix := e.SceneName + "\x00"
		for k, id := range c.itemIDs {
			if id == e.SceneItemId && strings.HasPrefix(k, prefix) {
				c.state.Sources[k] = e.SceneItemEnabled
			}
		}

	case *events.InputMuteStateChanged:
		c.state.Inputs[e.InputName] = e.InputMuted

	default:
		return false, false
	}
	return true, false
}

// Refresh performs a full state resync. Runs on connect and on deck reload.
func (c *Client) Refresh() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}

	if resp, err := obs.Scenes.GetSceneList(); err == nil {
		c.mu.Lock()
		c.state.ProgramScene = resp.CurrentProgramSceneName
		c.state.PreviewScene = resp.CurrentPreviewSceneName
		c.state.SceneList = sceneNames(resp.Scenes)
		c.mu.Unlock()
	}
	if resp, err := obs.Stream.GetStreamStatus(); err == nil {
		c.mu.Lock()
		c.state.Streaming = resp.OutputActive
		c.mu.Unlock()
	}
	if resp, err := obs.Record.GetRecordStatus(); err == nil {
		c.mu.Lock()
		c.state.Recording = resp.OutputActive
		c.state.RecordPaused = resp.OutputPaused
		c.mu.Unlock()
	}
	if resp, err := obs.Ui.GetStudioModeEnabled(); err == nil {
		c.mu.Lock()
		c.state.StudioMode = resp.StudioModeEnabled
		c.mu.Unlock()
	}

	if err := c.refreshSceneItems(); err != nil {
		return err
	}
	return c.refreshInputs()
}

func (c *Client) refreshSceneItems() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}

	ids := map[string]int{}
	vis := map[string]bool{}

	for _, pair := range c.store.Deck().SceneItemTargets() {
		scene, source := pair[0], pair[1]

		idResp, err := obs.SceneItems.GetSceneItemId(
			sceneitems.NewGetSceneItemIdParams().WithSceneName(scene).WithSourceName(source))
		if err != nil {
			// The deck names a source this OBS profile does not have. Leave it
			// out; the tile renders as unavailable rather than breaking sync.
			continue
		}

		key := itemKey(scene, source)
		ids[key] = int(idResp.SceneItemId)

		enResp, err := obs.SceneItems.GetSceneItemEnabled(
			sceneitems.NewGetSceneItemEnabledParams().
				WithSceneName(scene).WithSceneItemId(idResp.SceneItemId))
		if err == nil {
			vis[key] = enResp.SceneItemEnabled
		}
	}

	c.mu.Lock()
	c.itemIDs = ids
	c.state.Sources = vis
	c.mu.Unlock()
	return nil
}

func (c *Client) refreshInputs() error {
	obs := c.client()
	if obs == nil {
		return errNotConnected
	}
	muted := map[string]bool{}
	for _, name := range c.store.Deck().InputTargets() {
		resp, err := obs.Inputs.GetInputMute(inputs.NewGetInputMuteParams().WithInputName(name))
		if err != nil {
			continue
		}
		muted[name] = resp.InputMuted
	}
	c.mu.Lock()
	c.state.Inputs = muted
	c.mu.Unlock()
	return nil
}
