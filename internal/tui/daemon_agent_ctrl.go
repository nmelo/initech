// daemon_agent_ctrl.go implements the configure_agent / stop_agent /
// restart_agent control commands used by zero-config remote daemons.
// The local TUI is the source of truth for agent configuration; the daemon
// receives concrete instructions and manages process lifecycle.
package tui

import (
	"github.com/nmelo/initech/internal/config"
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
	owners map[string]string     // agent name -> client peer name
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
	// the workspace files and return OK. The agent process is not restarted —
	// it picks up the new CLAUDE.md on its next session start.
	if existing := d.findPane(cmd.Name); existing != nil {
		if prev, ok := d.ownership.verify(cmd.Name, owner); !ok {
			return ControlResp{
				ID:    cmd.ID,
				Error: fmt.Sprintf("agent %q already exists (owned by %q)", cmd.Name, prev),
			}
		}
		if err := writeWorkspace(cmd); err != nil {
			return ControlResp{ID: cmd.ID, Error: err.Error()}
		}
		return ControlResp{ID: cmd.ID, OK: true, Action: "configure_agent_ok", Target: cmd.Name}
	}

	// New pane: create the workspace tree and write CLAUDE.md files.
	if err := writeWorkspace(cmd); err != nil {
		return ControlResp{ID: cmd.ID, Error: err.Error()}
	}

	paneCfg := PaneConfig{
		Name:             cmd.Name,
		Command:          cmd.Command,
		Dir:              cmd.Dir,
		Env:              cmd.Env,
		AgentType:        cmd.AgentType,
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

	// Stream-on-create: open a yamux stream on the owner's session and
	// announce it to the client via stream_added. Best-effort — failures
	// here don't roll back the configure (the agent is running; the client
	// will discover it on next reconnect via hello_ok).
	if err := d.allocateStreamForPushedPane(cmd.Name, owner); err != nil {
		LogWarn("daemon", "stream-on-create failed", "agent", cmd.Name, "owner", owner, "err", err)
	}

	return ControlResp{ID: cmd.ID, OK: true, Action: "configure_agent_ok", Target: cmd.Name}
}

// allocateStreamForPushedPane opens a yamux stream on the named owner's
// session, wires it to the new pane's MultiSink, and sends a stream_added
// control message so the client can attach a RemotePane.
//
// Returns nil if the owner is not currently connected (reconnect path will
// handle it via hello_ok).
func (d *Daemon) allocateStreamForPushedPane(agentName, owner string) error {
	d.sessionsMu.Lock()
	session := d.clientSessions[owner]
	ctrl := d.clients[owner]
	ctrlMu := d.clientCtrlMu[owner]
	d.sessionsMu.Unlock()

	if session == nil || ctrl == nil {
		return nil // Owner disconnected; not an error.
	}

	stream, err := session.Open()
	if err != nil {
		return fmt.Errorf("open yamux stream: %w", err)
	}
	ys, ok := stream.(yamuxStreamLike)
	if !ok {
		stream.Close()
		return fmt.Errorf("unexpected stream type")
	}
	streamID := ys.StreamID()

	// Wire the stream to the pane's multisink for downstream PTY bytes.
	d.panesMu.Lock()
	ms := d.multiSinks[agentName]
	d.panesMu.Unlock()
	if ms != nil {
		ms.Add(stream)
	}

	// Send stream_added on the control channel so the client opens a RemotePane.
	msg := StreamAddedMsg{Action: "stream_added", StreamID: streamID, Name: agentName}
	if ctrlMu != nil {
		ctrlMu.Lock()
		err = writeJSON(ctrl, msg)
		ctrlMu.Unlock()
	} else {
		err = writeJSON(ctrl, msg)
	}
	if err != nil {
		return fmt.Errorf("send stream_added: %w", err)
	}
	return nil
}

// yamuxStreamLike is the subset of *yamux.Stream we depend on. Defined as
// an interface to keep this file from importing yamux directly (the package
// already imports it elsewhere; this type assertion stays type-safe via
// the runtime check above).
type yamuxStreamLike interface {
	StreamID() uint32
}

// handleStopAgent stops a previously-pushed agent. Verifies ownership.

// handleReloadAgents re-reads the project config and spawns any roles that are
// configured but not running — the bounce-free path for roster growth
// (ini-ap3i). Deliberately spawn-only: a role REMOVED from config is reported
// but not killed, because reload means "catch up", and killing is a decision
// the stop verb already owns. Each spawned agent is announced to every
// connected client via the existing stream_added flow, so it appears in their
// windows within seconds.
func (d *Daemon) handleReloadAgents(id, peerName string) ControlResp {
	if d.buildAgent == nil {
		return ControlResp{ID: id, Error: "reload_agents: this daemon runs zero-config (no initech.yaml to reload)"}
	}
	cfgPath, err := config.Discover(d.project.Root)
	if err != nil {
		return ControlResp{ID: id, Error: fmt.Sprintf("reload_agents: discover config: %v", err)}
	}
	proj, err := config.Load(cfgPath)
	if err != nil {
		return ControlResp{ID: id, Error: fmt.Sprintf("reload_agents: reload config: %v", err)}
	}

	running := map[string]bool{}
	d.panesMu.Lock()
	for _, p := range d.panes {
		running[p.Name()] = true
	}
	d.panesMu.Unlock()

	var added, failed, extra []string
	configured := map[string]bool{}
	for _, role := range proj.Roles {
		configured[role] = true
		if running[role] {
			continue
		}
		pcfg, err := d.buildAgent(role, proj)
		if err != nil {
			LogWarn("daemon", "reload: build agent failed", "role", role, "err", err)
			failed = append(failed, role)
			continue
		}
		if d.sockPath != "" {
			pcfg.Env = append(pcfg.Env, "INITECH_SOCKET="+d.sockPath)
		}
		pcfg.Env = append(pcfg.Env, "INITECH_AGENT="+pcfg.Name)
		if err := d.startPushedPane(pcfg); err != nil {
			LogWarn("daemon", "reload: spawn failed", "role", role, "err", err)
			failed = append(failed, role)
			continue
		}
		// Announce to every connected client (stream_added), best-effort.
		d.sessionsMu.Lock()
		owners := make([]string, 0, len(d.clients))
		for o := range d.clients {
			owners = append(owners, o)
		}
		d.sessionsMu.Unlock()
		for _, o := range owners {
			if err := d.allocateStreamForPushedPane(role, o); err != nil {
				LogWarn("daemon", "reload: announce failed", "role", role, "client", o, "err", err)
			}
		}
		added = append(added, role)
		LogInfo("daemon", "agent spawned by reload", "name", role, "requested_by", peerName)
	}
	for name := range running {
		if !configured[name] {
			extra = append(extra, name)
		}
	}
	if len(failed) > 0 {
		return ControlResp{ID: id, Error: fmt.Sprintf(
			"reload_agents: spawned %v but FAILED %v (see daemon log); running-but-deconfigured (not touched): %v", added, failed, extra)}
	}
	return ControlResp{ID: id, OK: true, Action: "reload_agents_ok",
		Target: fmt.Sprintf("spawned %v; deconfigured-but-running (not touched): %v", added, extra)}
}

// lifecycleAuthority resolves whether "owner" may lifecycle agent "name" and
// which config a respawn should use. Two regimes, deliberately different
// (ini-ap3i, operator-verified session 2026-08-15):
//
//   - PUSHED agents (ownership record exists): only the pushing client may
//     touch them — the existing rule, unchanged. Its config is the pushed one.
//   - SELF-STARTED agents (no ownership record — spawned from the daemon's own
//     initech.yaml at boot): any TOKEN-AUTHENTICATED peer may stop/restart
//     them. The token is the fleet's authority credential; a hub that may read
//     the agent's screen and type into it but not respawn it after a config
//     fix is an inconsistent trust boundary — tonight's PATH incident needed a
//     full daemon bounce for exactly this gap. Respawn config comes from the
//     live pane itself, and NO ownership record is created: a restart does not
//     convert a self-started agent into a pushed one.
func (d *Daemon) lifecycleAuthority(name, owner string) (PaneConfig, *ControlResp) {
	if prev, exists := func() (string, bool) {
		d.ownership.mu.Lock()
		defer d.ownership.mu.Unlock()
		pr, ok := d.ownership.owners[name]
		return pr, ok
	}(); exists {
		if prev != owner {
			return PaneConfig{}, &ControlResp{Error: fmt.Sprintf("agent %q is owned by %q, not %q", name, prev, owner)}
		}
		cfg, ok := d.ownership.config(name)
		if !ok {
			return PaneConfig{}, &ControlResp{Error: fmt.Sprintf("no saved config for %q", name)}
		}
		return cfg, nil
	}
	p := d.findPane(name)
	if p == nil {
		return PaneConfig{}, &ControlResp{Error: fmt.Sprintf("agent %q not found", name)}
	}
	return p.cfg, nil
}

func (d *Daemon) handleStopAgent(line []byte, owner string) ControlResp {
	var cmd StopAgentCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid stop_agent payload: %v", err)}
	}
	if cmd.Name == "" {
		return ControlResp{ID: cmd.ID, Error: "name is required"}
	}

	if p := d.findPane(cmd.Name); p == nil {
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("agent %q not found", cmd.Name)}
	}
	if _, refusal := d.lifecycleAuthority(cmd.Name, owner); refusal != nil {
		refusal.ID = cmd.ID
		return *refusal
	}

	// Two stop semantics, matching the two ownership regimes (found LIVE in
	// the ini-ap3i verification: stop-then-restart on a self-started agent
	// said "not found" — stop had DELETED the pane, leaving the agent
	// remotely unrecoverable since start_agent does not exist on this wire):
	//   - PUSHED agents: stop = decommission (remove pane + release), the
	//     zero-config contract, unchanged.
	//   - SELF-STARTED agents: stop = stop the PROCESS, keep the pane — the
	//     same meaning stop has locally — so restart can bring it back.
	if _, pushed := d.ownership.config(cmd.Name); pushed {
		d.removePane(cmd.Name)
		d.ownership.release(cmd.Name)
	} else if p := d.findPane(cmd.Name); p != nil {
		p.Close()
	}
	LogInfo("daemon", "agent stopped by peer", "name", cmd.Name, "peer", owner)
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
	cfg, refusal := d.lifecycleAuthority(cmd.Name, owner)
	if refusal != nil {
		refusal.ID = cmd.ID
		return *refusal
	}

	LogInfo("daemon", "agent restarting by peer", "name", cmd.Name, "peer", owner)
	// Preserve the multisink across the remove/start pair: removePane deletes
	// it (correct for decommission), but a RESTART must keep attached viewer
	// streams alive — see startPushedPane's reuse comment.
	d.panesMu.Lock()
	keepSink := d.multiSinks[cmd.Name]
	d.panesMu.Unlock()
	d.removePane(cmd.Name)
	if keepSink != nil {
		d.panesMu.Lock()
		d.multiSinks[cmd.Name] = keepSink
		d.panesMu.Unlock()
	}
	if err := d.startPushedPane(cfg); err != nil {
		d.ownership.release(cmd.Name)
		return ControlResp{ID: cmd.ID, Error: fmt.Sprintf("restart agent: %v", err)}
	}

	return ControlResp{ID: cmd.ID, OK: true, Action: "restart_agent_ok", Target: cmd.Name}
}

// writeWorkspace creates the agent workspace tree and writes CLAUDE.md
// content from the configure_agent payload. Idempotent: directory creation
// is MkdirAll and file writes only fire when content has changed (so
// repeated pushes don't churn mtime).
//
// Layout:
//
//	<dir>/                  (0755)
//	<dir>/.claude/          (0755)
//	<dir>/CLAUDE.md         (0644, role-level)
//	<filepath.Dir(dir)>/CLAUDE.md  (0644, project-root)
//
// A no-op when cmd.Dir is empty (some control flows pass nothing).
func writeWorkspace(cmd ConfigureAgentCmd) error {
	if cmd.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(cmd.Dir, 0o755); err != nil {
		return fmt.Errorf("create workspace %s: %w", cmd.Dir, err)
	}
	claudeDir := filepath.Join(cmd.Dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("create .claude dir %s: %w", claudeDir, err)
	}
	if cmd.ClaudeMD != "" {
		path := filepath.Join(cmd.Dir, "CLAUDE.md")
		if err := writeIfChanged(path, cmd.ClaudeMD, 0o644); err != nil {
			return fmt.Errorf("write CLAUDE.md: %w", err)
		}
	}
	if cmd.RootClaudeMD != "" {
		rootPath := filepath.Join(filepath.Dir(cmd.Dir), "CLAUDE.md")
		if err := writeIfChanged(rootPath, cmd.RootClaudeMD, 0o644); err != nil {
			return fmt.Errorf("write root CLAUDE.md: %w", err)
		}
	}
	return nil
}

// writeIfChanged writes content to path only if the existing file differs.
// Avoids spurious mtime updates when push payloads are unchanged.
func writeIfChanged(path, content string, mode os.FileMode) error {
	existing, _ := os.ReadFile(path)
	if string(existing) == content {
		return nil
	}
	return os.WriteFile(path, []byte(content), mode)
}

// startPushedPane creates a Pane, wires the per-agent ring buffer + multisink,
// and starts the process. Used by configure_agent and restart_agent.
func (d *Daemon) startPushedPane(cfg PaneConfig) error {
	// Spawn at the last size a viewer applied, not the 24x80 default: a
	// remote RESTART otherwise brings the new process up in a tiny terminal
	// while every attached viewer renders a large pane — the agent paints a
	// 24-row screen into their region and the display reads as scattered
	// fragments (ini-ap3i, observed live 2026-08-15). Using the recorded
	// size also makes the fresh process's first full paint match the
	// viewers' geometry immediately.
	rows, cols := 24, 80
	d.panesMu.Lock()
	if sz, ok := d.lastSizes[cfg.Name]; ok {
		rows, cols = sz[0], sz[1]
	}
	d.panesMu.Unlock()
	p, err := NewPane(cfg, rows, cols)
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
	// REUSE an existing multisink: connected clients' live streams are Add'd
	// to it dynamically, and replacing the sink on restart ORPHANED every one
	// of them — the hub's view froze at the last frame while the agent worked
	// on invisibly, and the operator typed blind into a screen that never
	// updated (ini-ap3i, observed live 2026-08-15). The ring buffer is
	// swapped fresh (replay should show the NEW process's screen, not the
	// old one's tail) but the sink — and with it every attached viewer —
	// carries over.
	rb := NewRingBuf(DefaultRingBufSize)
	ms, existing := d.multiSinks[cfg.Name]
	if existing {
		if oldRB := d.ringBufs[cfg.Name]; oldRB != nil {
			ms.Remove(oldRB)
		}
		ms.Add(rb)
	} else {
		ms = NewMultiSink()
		ms.Add(rb)
		d.multiSinks[cfg.Name] = ms
	}
	d.ringBufs[cfg.Name] = rb
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
