// daemon_agent_ctrl.go implements the configure_agent / stop_agent /
// restart_agent control commands used by zero-config remote daemons.
// The local TUI is the source of truth for agent configuration; the daemon
// receives concrete instructions and manages process lifecycle.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ConfigureAgentCmd is sent by a client to push an agent configuration to a
// zero-config daemon. The daemon creates the workspace, writes CLAUDE.md
// files, creates a Pane, and starts the process.
type ConfigureAgentCmd struct {
	ID               string   `json:"id,omitempty"`
	Action           string   `json:"action"` // "configure_agent"
	Name             string   `json:"name"`
	Command          []string `json:"command"`
	Dir              string   `json:"dir"`
	Env              []string `json:"env,omitempty"`
	AgentType        string   `json:"agent_type,omitempty"`
	AutoApprove      bool     `json:"auto_approve,omitempty"`
	NoBracketedPaste bool     `json:"no_bracketed_paste,omitempty"`
	SubmitKey        string   `json:"submit_key,omitempty"`
	ClaudeMD         string   `json:"claude_md,omitempty"`      // Role-level CLAUDE.md content.
	RootClaudeMD     string   `json:"root_claude_md,omitempty"` // Project-root CLAUDE.md content.
}

// StopAgentCmd stops a previously-pushed agent. Workspace files are preserved.
type StopAgentCmd struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"` // "stop_agent"
	Name   string `json:"name"`
}

// RestartAgentCmd kills the existing process and starts a new one with the
// same config (command/dir/env/etc.).
type RestartAgentCmd struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"` // "restart_agent"
	Name   string `json:"name"`
}

// agentOwnership maps agent name to the peer name of the client that pushed
// it. Used to enforce that only the owning client can stop/restart an agent.
type agentOwnership struct {
	mu     sync.Mutex
	owners map[string]string  // agent name -> client peer name
	cfgs   map[string]PaneConfig // agent name -> last-pushed config (for restart)
}

func newAgentOwnership() *agentOwnership {
	return &agentOwnership{
		owners: make(map[string]string),
		cfgs:   make(map[string]PaneConfig),
	}
}

func (a *agentOwnership) claim(name, owner string, cfg PaneConfig) (existingOwner string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if prev, exists := a.owners[name]; exists {
		return prev, false
	}
	a.owners[name] = owner
	a.cfgs[name] = cfg
	return "", true
}

func (a *agentOwnership) release(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.owners, name)
	delete(a.cfgs, name)
}

func (a *agentOwnership) verify(name, owner string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev, exists := a.owners[name]
	if !exists {
		return "", false
	}
	return prev, prev == owner
}

func (a *agentOwnership) config(name string) (PaneConfig, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg, ok := a.cfgs[name]
	return cfg, ok
}

// handleConfigureAgent creates a workspace, writes CLAUDE.md files, and
// starts a new agent pane. Called by the control-stream dispatcher when
// the client sends action=configure_agent.
//
// Idempotent for same-owner re-pushes: if the agent already exists and
// is owned by the requesting client, refresh CLAUDE.md files (write only
// if content changed) and return OK without disrupting the running agent.
// Different-owner collisions are rejected.
func (d *Daemon) handleConfigureAgent(line []byte, owner string) ControlResp {
	var cmd ConfigureAgentCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid configure_agent payload: %v", err)}
	}
	if cmd.Name == "" {
		return ControlResp{ID: cmd.ID, Error: "name is required"}
	}

	// Idempotent path: if agent exists and is owned by this client, refresh
	// CLAUDE.md files and return OK. The agent process is not restarted —
	// it picks up the new CLAUDE.md on its next session start.
	if existing := d.findPane(cmd.Name); existing != nil {
		if prev, ok := d.ownership.verify(cmd.Name, owner); !ok {
			return ControlResp{
				ID:    cmd.ID,
				Error: fmt.Sprintf("agent %q already exists (owned by %q)", cmd.Name, prev),
			}
		}
		if err := refreshClaudeMD(cmd); err != nil {
			return ControlResp{ID: cmd.ID, Error: err.Error()}
		}
		return ControlResp{ID: cmd.ID, OK: true, Action: "configure_agent_ok", Target: cmd.Name}
	}

	// Create workspace and write CLAUDE.md files.
	if cmd.Dir != "" {
		if err := os.MkdirAll(cmd.Dir, 0o755); err != nil {
			return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("create workspace %s: %v", cmd.Dir, err)}
		}
		if cmd.ClaudeMD != "" {
			path := filepath.Join(cmd.Dir, "CLAUDE.md")
			if err := os.WriteFile(path, []byte(cmd.ClaudeMD), 0o644); err != nil {
				return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("write CLAUDE.md: %v", err)}
			}
		}
	}
	// Root CLAUDE.md goes in the parent of the agent dir (project root).
	if cmd.RootClaudeMD != "" && cmd.Dir != "" {
		rootPath := filepath.Join(filepath.Dir(cmd.Dir), "CLAUDE.md")
		if err := os.WriteFile(rootPath, []byte(cmd.RootClaudeMD), 0o644); err != nil {
			return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("write root CLAUDE.md: %v", err)}
		}
	}

	paneCfg := PaneConfig{
		Name:             cmd.Name,
		Command:          cmd.Command,
		Dir:              cmd.Dir,
		Env:              cmd.Env,
		AgentType:        cmd.AgentType,
		AutoApprove:      cmd.AutoApprove,
		NoBracketedPaste: cmd.NoBracketedPaste,
		SubmitKey:        cmd.SubmitKey,
	}

	if _, ok := d.ownership.claim(cmd.Name, owner, paneCfg); !ok {
		// Concurrent claim — another client raced us.
		prev, _ := d.ownership.verify(cmd.Name, owner)
		return ControlResp{
			ID:    cmd.ID,
			Error: fmt.Sprintf("agent %q already owned by %q", cmd.Name, prev),
		}
	}

	if err := d.startPushedPane(paneCfg); err != nil {
		d.ownership.release(cmd.Name)
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("start agent: %v", err)}
	}

	return ControlResp{ID: cmd.ID, OK: true, Action: "configure_agent_ok", Target: cmd.Name}
}

// handleStopAgent stops a previously-pushed agent. Verifies ownership.
func (d *Daemon) handleStopAgent(line []byte, owner string) ControlResp {
	var cmd StopAgentCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid stop_agent payload: %v", err)}
	}
	if cmd.Name == "" {
		return ControlResp{ID: cmd.ID, Error: "name is required"}
	}

	p := d.findPane(cmd.Name)
	if p == nil {
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("agent %q not found", cmd.Name)}
	}
	if prev, ok := d.ownership.verify(cmd.Name, owner); !ok {
		return ControlResp{
			ID:    cmd.ID,
			Error: fmt.Sprintf("agent %q is owned by %q, not %q", cmd.Name, prev, owner),
		}
	}

	d.removePane(cmd.Name)
	d.ownership.release(cmd.Name)
	return ControlResp{ID: cmd.ID, OK: true, Action: "stop_agent_ok", Target: cmd.Name}
}

// handleRestartAgent stops the existing process and creates a new one with
// the same config. Verifies ownership.
func (d *Daemon) handleRestartAgent(line []byte, owner string) ControlResp {
	var cmd RestartAgentCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid restart_agent payload: %v", err)}
	}
	if cmd.Name == "" {
		return ControlResp{ID: cmd.ID, Error: "name is required"}
	}

	if p := d.findPane(cmd.Name); p == nil {
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("agent %q not found", cmd.Name)}
	}
	if prev, ok := d.ownership.verify(cmd.Name, owner); !ok {
		return ControlResp{
			ID:    cmd.ID,
			Error: fmt.Sprintf("agent %q is owned by %q, not %q", cmd.Name, prev, owner),
		}
	}

	cfg, ok := d.ownership.config(cmd.Name)
	if !ok {
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("no saved config for %q", cmd.Name)}
	}

	d.removePane(cmd.Name)
	if err := d.startPushedPane(cfg); err != nil {
		d.ownership.release(cmd.Name)
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("restart agent: %v", err)}
	}

	return ControlResp{ID: cmd.ID, OK: true, Action: "restart_agent_ok", Target: cmd.Name}
}

// refreshClaudeMD writes CLAUDE.md and root CLAUDE.md only if the on-disk
// content differs from the new payload. Used by the idempotent
// configure_agent re-push path so unchanged content avoids a write.
func refreshClaudeMD(cmd ConfigureAgentCmd) error {
	if cmd.Dir == "" {
		return nil
	}
	if cmd.ClaudeMD != "" {
		path := filepath.Join(cmd.Dir, "CLAUDE.md")
		existing, _ := os.ReadFile(path)
		if string(existing) != cmd.ClaudeMD {
			if err := os.WriteFile(path, []byte(cmd.ClaudeMD), 0o644); err != nil {
				return fmt.Errorf("refresh CLAUDE.md: %w", err)
			}
		}
	}
	if cmd.RootClaudeMD != "" {
		rootPath := filepath.Join(filepath.Dir(cmd.Dir), "CLAUDE.md")
		existing, _ := os.ReadFile(rootPath)
		if string(existing) != cmd.RootClaudeMD {
			if err := os.WriteFile(rootPath, []byte(cmd.RootClaudeMD), 0o644); err != nil {
				return fmt.Errorf("refresh root CLAUDE.md: %w", err)
			}
		}
	}
	return nil
}

// startPushedPane creates a Pane, wires the per-agent ring buffer + multisink,
// and starts the process. Used by configure_agent and restart_agent.
func (d *Daemon) startPushedPane(cfg PaneConfig) error {
	p, err := NewPane(cfg, 24, 80)
	if err != nil {
		return err
	}
	d.panesMu.Lock()
	if d.ringBufs == nil {
		d.ringBufs = make(map[string]*RingBuf)
	}
	if d.multiSinks == nil {
		d.multiSinks = make(map[string]*MultiSink)
	}
	rb := NewRingBuf(DefaultRingBufSize)
	d.ringBufs[cfg.Name] = rb
	ms := NewMultiSink()
	ms.Add(rb)
	d.multiSinks[cfg.Name] = ms
	p.SetNetworkSink(ms)
	d.panes = append(d.panes, p)
	d.panesMu.Unlock()
	p.Start()
	return nil
}

// removePane stops the named pane, removes it from d.panes, and tears down
// its ring buffer and multisink. No-op if the pane does not exist.
func (d *Daemon) removePane(name string) {
	d.panesMu.Lock()
	idx := -1
	var p *Pane
	for i, pp := range d.panes {
		if pp.Name() == name {
			idx = i
			p = pp
			break
		}
	}
	if idx >= 0 {
		d.panes = append(d.panes[:idx], d.panes[idx+1:]...)
	}
	delete(d.ringBufs, name)
	delete(d.multiSinks, name)
	d.panesMu.Unlock()
	if p != nil {
		p.Close()
	}
}
