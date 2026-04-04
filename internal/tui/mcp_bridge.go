package tui

import (
	"fmt"
	"time"

	"github.com/nmelo/initech/internal/mcp"
)

// tuiMCPHost adapts the TUI to the mcp.PaneHost interface. It uses runOnMain
// for pane lookup and lifecycle operations that touch t.panes.
type tuiMCPHost struct {
	t *TUI
}

var _ mcp.PaneHost = (*tuiMCPHost)(nil)

func (h *tuiMCPHost) FindPane(name string) (mcp.PaneHandle, bool) {
	var handle mcp.PaneHandle
	ok := h.t.runOnMain(func() {
		pv := h.t.findPaneByName(name)
		if pv == nil {
			return
		}
		if lp, isLocal := pv.(*Pane); isLocal {
			handle = &mcpPaneHandle{p: lp, t: h.t}
		}
	})
	if !ok {
		return nil, false
	}
	return handle, true
}

func (h *tuiMCPHost) AllPanes() ([]mcp.PaneHandle, bool) {
	var handles []mcp.PaneHandle
	ok := h.t.runOnMain(func() {
		handles = make([]mcp.PaneHandle, 0, len(h.t.panes))
		for _, pv := range h.t.panes {
			if lp, isLocal := pv.(*Pane); isLocal {
				handles = append(handles, &mcpPaneHandle{p: lp, t: h.t})
			}
		}
	})
	if !ok {
		return nil, false
	}
	return handles, true
}

func (h *tuiMCPHost) RestartAgent(name string) error {
	var err error
	ok := h.t.runOnMain(func() {
		err = h.t.restartByName(name)
	})
	if !ok {
		return fmt.Errorf("TUI shutting down")
	}
	return err
}

func (h *tuiMCPHost) StopAgent(name string) error {
	var pane *Pane
	ok := h.t.runOnMain(func() {
		pane = h.t.findPane(name)
	})
	if !ok {
		return fmt.Errorf("TUI shutting down")
	}
	if pane == nil {
		return fmt.Errorf("agent %q not found", name)
	}
	if !pane.IsAlive() {
		return nil // already stopped
	}
	pane.Close()
	return nil
}

func (h *tuiMCPHost) StartAgent(name string) error {
	var startErr error
	ok := h.t.runOnMain(func() {
		// Reuse the IPC start path: find pane, rebuild if dead.
		for i, p := range h.t.panes {
			if p.Name() != name {
				continue
			}
			lp, isLocal := p.(*Pane)
			if !isLocal {
				startErr = fmt.Errorf("start not supported for remote panes")
				return
			}
			if lp.IsAlive() {
				return // already running
			}
			cols := lp.emu.Width()
			rows := lp.emu.Height()
			if cols < 10 {
				cols = 80
			}
			if rows < 5 {
				rows = 24
			}
			np, err := NewPane(lp.cfg, rows, cols)
			if err != nil {
				startErr = err
				return
			}
			np.region = lp.region
			np.eventCh = h.t.agentEvents
			np.safeGo = h.t.safeGo
			np.Start()
			h.t.panes[i] = np
			return
		}
		startErr = fmt.Errorf("agent %q not found", name)
	})
	if !ok {
		return fmt.Errorf("TUI shutting down")
	}
	return startErr
}

func (h *tuiMCPHost) AddAgent(name string) error {
	var err error
	ok := h.t.runOnMain(func() {
		err = h.t.addPane(name)
	})
	if !ok {
		return fmt.Errorf("TUI shutting down")
	}
	return err
}

func (h *tuiMCPHost) RemoveAgent(name string) error {
	var err error
	ok := h.t.runOnMain(func() {
		err = h.t.removePane(name)
	})
	if !ok {
		return fmt.Errorf("TUI shutting down")
	}
	return err
}

func (h *tuiMCPHost) NotifyConfig() (string, string) {
	return h.t.webhookURL, h.t.projectName
}

func (h *tuiMCPHost) ScheduleSend(agent, message, delay string) (string, error) {
	dur, err := time.ParseDuration(delay)
	if err != nil {
		return "", fmt.Errorf("invalid delay %q: %w", delay, err)
	}
	fireAt := time.Now().Add(dur)
	timer, err := h.t.timers.Add(agent, "", message, true, fireAt)
	if err != nil {
		return "", err
	}
	return timer.ID, nil
}

// mcpPaneHandle wraps a *Pane to implement mcp.PaneHandle.
type mcpPaneHandle struct {
	p *Pane
	t *TUI
}

var _ mcp.PaneHandle = (*mcpPaneHandle)(nil)

func (h *mcpPaneHandle) Name() string              { return h.p.Name() }
func (h *mcpPaneHandle) PeekContent(lines int) string { return peekContent(h.p, lines) }
func (h *mcpPaneHandle) SendText(text string, enter bool) { h.p.SendText(text, enter) }
func (h *mcpPaneHandle) Activity() string           { return h.p.Activity().String() }
func (h *mcpPaneHandle) IsAlive() bool              { return h.p.IsAlive() }
func (h *mcpPaneHandle) BeadID() string             { return h.p.BeadID() }
func (h *mcpPaneHandle) MemoryRSSKB() int64         { return h.p.MemoryRSS() }

func (h *mcpPaneHandle) IsVisible() bool {
	var visible bool
	h.t.runOnMain(func() {
		visible = !h.t.layoutState.Hidden[paneKey(h.p)]
	})
	return visible
}
