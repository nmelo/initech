package tui

import (
	"fmt"

	"github.com/nmelo/initech/internal/slackchat"
)

// tuiSlackHost adapts the TUI to the slackchat.AgentHost interface.
// It uses runOnMain for thread-safe pane access.
type tuiSlackHost struct {
	t *TUI
}

var _ slackchat.AgentHost = (*tuiSlackHost)(nil)

func (h *tuiSlackHost) FindAgent(name string) (slackchat.AgentInfo, bool) {
	var info slackchat.AgentInfo
	var found bool
	h.t.runOnMain(func() {
		for _, pv := range h.t.panes {
			if pv.Name() == name {
				info = slackchat.AgentInfo{
					Name:     pv.Name(),
					Alive:    pv.IsAlive(),
					Activity: pv.Activity().String(),
				}
				found = true
				return
			}
		}
	})
	return info, found
}

func (h *tuiSlackHost) AllAgents() []slackchat.AgentInfo {
	var agents []slackchat.AgentInfo
	h.t.runOnMain(func() {
		agents = make([]slackchat.AgentInfo, len(h.t.panes))
		for i, pv := range h.t.panes {
			agents[i] = slackchat.AgentInfo{
				Name:     pv.Name(),
				Alive:    pv.IsAlive(),
				Activity: pv.Activity().String(),
			}
		}
	})
	return agents
}

func (h *tuiSlackHost) SendToAgent(name, text string) error {
	var err error
	h.t.runOnMain(func() {
		for _, pv := range h.t.panes {
			if pv.Name() == name {
				pv.SendText(text, true)
				return
			}
		}
		err = fmt.Errorf("agent %q not found", name)
	})
	return err
}
