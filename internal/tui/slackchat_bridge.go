package tui

import (
	"fmt"
	"strings"

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

// tuiPanePeeker adapts the TUI to the slackchat.PanePeeker interface.
// Reads the bottom N lines from an agent's VT emulator.
type tuiPanePeeker struct {
	t *TUI
}

var _ slackchat.PanePeeker = (*tuiPanePeeker)(nil)

func (p *tuiPanePeeker) PeekOutput(agentName string, lines int) (string, error) {
	var result string
	var err error
	p.t.runOnMain(func() {
		for _, pv := range p.t.panes {
			if pv.Name() != agentName {
				continue
			}
			emu := pv.Emulator()
			h := emu.Height()
			w := emu.Width()
			start := 0
			if h > lines {
				start = h - lines
			}
			rows := make([]string, 0, h-start)
			for row := start; row < h; row++ {
				var sb strings.Builder
				for col := 0; col < w; col++ {
					cell := emu.CellAt(col, row)
					if cell != nil && cell.Content != "" {
						sb.WriteString(cell.Content)
					} else {
						sb.WriteByte(' ')
					}
				}
				rows = append(rows, strings.TrimRight(sb.String(), " "))
			}
			result = strings.Join(rows, "\n")
			// Trim trailing empty lines.
			result = strings.TrimRight(result, "\n ")
			return
		}
		err = fmt.Errorf("agent %q not found", agentName)
	})
	return result, err
}
