package obsws

import (
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
)

// Catalog is the live OBS inventory the button editor offers as choices.
type Catalog struct {
	Scenes  []string            `json:"scenes"`
	Sources map[string][]string `json:"sources"` // scene -> source names
	Inputs  []string            `json:"inputs"`
	Hotkeys []string            `json:"hotkeys"`
}

// Catalog gathers everything the editor can bind a button to, including the
// complete hotkey list, which is what puts every bindable OBS action within
// reach of a tile.
func (c *Client) Catalog() (*Catalog, error) {
	obs := c.client()
	if obs == nil {
		return nil, errNotConnected
	}
	out := &Catalog{Sources: map[string][]string{}}

	sceneList, err := obs.Scenes.GetSceneList()
	if err != nil {
		return nil, err
	}
	// Same display order the deck tiles use, so the editor's dropdown and the
	// grid do not disagree about which scene is which.
	for _, name := range sceneNames(sceneList.Scenes) {
		out.Scenes = append(out.Scenes, name)

		items, err := obs.SceneItems.GetSceneItemList(
			sceneitems.NewGetSceneItemListParams().WithSceneName(name))
		if err != nil {
			continue
		}
		srcs := make([]string, 0, len(items.SceneItems))
		for _, it := range items.SceneItems {
			srcs = append(srcs, it.SourceName)
		}
		out.Sources[name] = srcs
	}

	if inputList, err := obs.Inputs.GetInputList(inputs.NewGetInputListParams()); err == nil {
		for _, in := range inputList.Inputs {
			out.Inputs = append(out.Inputs, in.InputName)
		}
	}

	if hk, err := obs.General.GetHotkeyList(); err == nil {
		out.Hotkeys = hk.Hotkeys
	}

	return out, nil
}
