// ipc_lifecycle.go contains IPC handlers for pane lifecycle operations:
// stop, start, restart, add, and remove. These handlers create, replace,
// or destroy panes in the running TUI session.
//
// Separated from ipc.go (which owns the socket server, router, and
// message-oriented handlers) to reduce merge conflicts when lifecycle
// and messaging logic are edited concurrently.
package tui

import (
	"fmt"
	"net"
	"os"
	"regexp"
)

// agentNameRe mirrors config.roleNameRe: letters, digits, hyphens, underscores.
var agentNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const maxAgentNameLen = 64

func (t *TUI) handleIPCStop(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	var pane *Pane
	if !t.runOnMain(func() { pane = t.findPane(req.Target) }) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pane == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	// Wait for any in-flight send to finish before closing.
	pane.sendMu.Lock()
	defer pane.sendMu.Unlock()
	if !pane.IsAlive() {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: "already stopped"})
		return
	}
	pane.Close()
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAgentStopped,
		Pane:   req.Target,
		Detail: "Stopped " + req.Target,
	})
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCStart(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	// Find the pane pointer and index on main to avoid races on t.panes.
	var old *Pane
	var oldIdx int
	if !t.runOnMain(func() {
		for i, p := range t.panes {
			if p.Name() == req.Target {
				if lp, ok := p.(*Pane); ok {
					old = lp
					oldIdx = i
				}
				return
			}
		}
	}) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if old == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found. To create a new pane, use: initech add %s", req.Target, req.Target)})
		return
	}
	if old.IsAlive() {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: "already running"})
		return
	}
	// Create the new pane off-main (may fork/exec).
	cols, rows := old.emu.Width(), old.emu.Height()
	// Dead panes may report zero dimensions; use sensible defaults.
	if cols < 10 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}
	// Rebuild config from the project to pick up any initech.yaml changes.
	cfg := old.cfg
	if t.paneConfigBuilder != nil {
		if fresh, err := t.paneConfigBuilder(req.Target); err == nil {
			fresh.Env = append(fresh.Env,
				"INITECH_SOCKET="+t.sockPath,
				"INITECH_AGENT="+req.Target,
			)
			cfg = fresh
		}
	}
	np, err := NewPane(cfg, rows, cols)
	if err != nil {
		LogError("pane", "start failed", "name", req.Target, "err", err)
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("start failed: %v", err)})
		return
	}
	np.region = old.region
	np.eventCh = t.agentEvents
	np.safeGo = t.safeGo
	np.pinned = old.IsPinned()
	// Replace in t.panes on main; re-verify index is still valid.
	if !t.runOnMain(func() {
		if oldIdx < len(t.panes) && t.panes[oldIdx] == old {
			np.Start()
			t.panes[oldIdx] = np
			t.applyLayout()
		} else {
			np.Close() // Index shifted; discard new pane.
		}
	}) {
		np.Close() // TUI shutting down; clean up the new pane.
	}
	LogInfo("pane", "started", "name", req.Target)
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAgentStarted,
		Pane:   req.Target,
		Detail: "Started " + req.Target,
	})
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCRestart(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	// Find the pane pointer and index on main to avoid races on t.panes.
	var old *Pane
	var oldIdx int
	if !t.runOnMain(func() {
		for i, p := range t.panes {
			if p.Name() == req.Target {
				if lp, ok := p.(*Pane); ok {
					old = lp
					oldIdx = i
				}
				return
			}
		}
	}) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if old == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	// Close the old pane off-main; sendMu serialises concurrent sends.
	old.sendMu.Lock()
	cols, rows := old.emu.Width(), old.emu.Height()
	// Dead panes may report zero dimensions; use sensible defaults.
	if cols < 10 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}
	old.Close()
	old.sendMu.Unlock()
	// Rebuild config from the project to pick up any initech.yaml changes
	// (e.g. role_overrides.command added after initial spawn).
	cfg := old.cfg
	if t.paneConfigBuilder != nil {
		if fresh, err := t.paneConfigBuilder(req.Target); err == nil {
			fresh.Env = append(fresh.Env,
				"INITECH_SOCKET="+t.sockPath,
				"INITECH_AGENT="+req.Target,
			)
			cfg = fresh
		}
	}
	// Create new pane off-main (may fork/exec).
	np, err := NewPane(cfg, rows, cols)
	if err != nil {
		LogError("pane", "restart failed", "name", req.Target, "err", err)
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("restart failed: %v", err)})
		return
	}
	np.region = old.region
	np.eventCh = t.agentEvents
	np.safeGo = t.safeGo
	np.pinned = old.IsPinned()
	// Replace in t.panes on main; re-verify index is still valid.
	if !t.runOnMain(func() {
		if oldIdx < len(t.panes) && t.panes[oldIdx] == old {
			np.Start()
			t.panes[oldIdx] = np
			t.applyLayout()
		} else {
			np.Close() // Index shifted; discard new pane.
		}
	}) {
		np.Close() // TUI shutting down; clean up the new pane.
	}
	LogInfo("pane", "restarted", "name", req.Target)
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAgentRestarted,
		Pane:   req.Target,
		Detail: "Restarted " + req.Target,
	})
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCAdd(conn net.Conn, req IPCRequest) {
	if err := t.addPane(req.Target); err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCRemove(conn net.Conn, req IPCRequest) {
	if err := t.removePane(req.Target); err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true})
}

// addPane creates a new pane for the given role name and integrates it into
// the running TUI. The workspace directory must already exist on disk.
// Returns an error if the name is empty, already exists, or has no workspace.
func (t *TUI) addPane(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > maxAgentNameLen {
		return fmt.Errorf("agent name too long (max %d chars)", maxAgentNameLen)
	}
	if !agentNameRe.MatchString(name) {
		return fmt.Errorf("invalid agent name %q: must contain only letters, digits, hyphens, or underscores", name)
	}
	// Check name uniqueness on main (reads t.panes).
	var existsErr error
	t.runOnMain(func() {
		if t.findPaneByName(name) != nil {
			existsErr = fmt.Errorf("agent %q already exists", name)
		}
	})
	if existsErr != nil {
		return existsErr
	}

	if t.paneConfigBuilder == nil {
		return fmt.Errorf("add not available: no config builder (was TUI started via 'initech up'?)")
	}

	cfg, err := t.paneConfigBuilder(name)
	if err != nil {
		return fmt.Errorf("build config for %q: %w", name, err)
	}

	// Verify the workspace directory exists.
	if _, err := os.Stat(cfg.Dir); os.IsNotExist(err) {
		return fmt.Errorf("workspace %s/ not found. Create it first (e.g. mkdir -p %s && cp <agent>/CLAUDE.md %s/)", name, cfg.Dir, cfg.Dir)
	}

	// Inject runtime env vars the TUI manages.
	cfg.Env = append(cfg.Env,
		"INITECH_SOCKET="+t.sockPath,
		"INITECH_AGENT="+name,
	)

	// Temporary dimensions; applyLayout will resize to the correct region.
	rows, cols := 24, 80
	if t.screen != nil {
		w, h := t.screen.Size()
		cols, rows = w/2, h/2
		if cols < 10 {
			cols = 80
		}
		if rows < 4 {
			rows = 24
		}
	}

	// Create the pane off-main (may fork/exec).
	p, err := NewPane(cfg, rows, cols)
	if err != nil {
		LogError("pane", "hot-add launch failed", "name", name, "err", err)
		return fmt.Errorf("create pane %q: %w", name, err)
	}
	p.eventCh = t.agentEvents
	p.safeGo = t.safeGo

	// Append to t.panes on main; re-verify uniqueness in case of concurrent add.
	var finalErr error
	t.runOnMain(func() {
		if t.findPaneByName(name) != nil {
			p.Close()
			finalErr = fmt.Errorf("agent %q already exists (added concurrently)", name)
			return
		}
		p.Start()
		t.panes = append(t.panes, p)
		// Recalculate grid for the new visible pane count.
		t.recalcGrid(true)
		t.saveLayoutIfConfigured()
	})
	if finalErr != nil {
		return finalErr
	}
	LogInfo("pane", "added", "name", name)
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAgentAdded,
		Pane:   name,
		Detail: "Added " + name + " to session",
	})
	return nil
}

// removePane kills the named pane and removes it from the running TUI.
// The workspace directory is NOT deleted. Returns an error if the name is
// empty, not found, or is the last pane (at least one must remain).
func (t *TUI) removePane(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	var removeErr error
	t.runOnMain(func() {
		idx := -1
		for i, p := range t.panes {
			if paneKey(p) == name || p.Name() == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			removeErr = fmt.Errorf("agent %q not found", name)
			return
		}
		if len(t.panes) == 1 {
			removeErr = fmt.Errorf("cannot remove last agent")
			return
		}

		p := t.panes[idx]
		pk := paneKey(p)
		p.Close()

		// Remove from slice without leaving gaps.
		t.panes = append(t.panes[:idx], t.panes[idx+1:]...)

		// Clean up layout state references.
		if t.layoutState.Hidden != nil {
			delete(t.layoutState.Hidden, pk)
		}
		// If this was the focused pane, clear focus so applyLayout snaps to next visible.
		if t.layoutState.Focused == pk {
			t.layoutState.Focused = ""
		}

		t.recalcGrid(true)
		t.saveLayoutIfConfigured()
	})
	if removeErr != nil {
		return removeErr
	}
	LogInfo("pane", "removed", "name", name)
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAgentRemoved,
		Pane:   name,
		Detail: "Removed " + name + " from session",
	})
	return nil
}
