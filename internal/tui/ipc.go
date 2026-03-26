package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// IPCRequest is the JSON structure sent by CLI commands to the TUI socket.
type IPCRequest struct {
	Action string `json:"action"` // "send", "peek", "list"
	Target string `json:"target"` // Role name (for send/peek).
	Text   string `json:"text"`   // Text to inject (for send).
	Lines  int    `json:"lines"`  // Number of lines to return (for peek, 0 = all).
	Enter  bool   `json:"enter"`  // Append Enter after text (for send).
}

// IPCResponse is the JSON structure returned by the TUI socket.
type IPCResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  string `json:"data,omitempty"` // Pane content for peek, pane list for list.
}

// SocketPath returns the socket path for a project. The socket is placed
// inside the project's .initech/ directory (not /tmp/) so it is scoped to the
// project and not world-visible.
func SocketPath(projectRoot, projectName string) string {
	if projectRoot == "" {
		// Fallback for callers that don't have a project root (e.g. tests).
		return fmt.Sprintf("/tmp/initech-%s.sock", projectName)
	}
	return filepath.Join(projectRoot, ".initech", "initech.sock")
}

// startIPC launches the Unix domain socket server in a goroutine.
// Returns a cleanup function that closes the listener and removes the socket.
func (t *TUI) startIPC(socketPath string) (cleanup func(), err error) {
	// Check for an existing active instance. Only dial if the socket file
	// exists (avoids 500ms timeout on clean starts).
	if _, statErr := os.Stat(socketPath); statErr == nil {
		conn, dialErr := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("session already running (socket %s is active). Use 'initech down' to stop it first", socketPath)
		}
		// Stale socket from a crashed instance; safe to remove.
		os.Remove(socketPath)
	}

	// Ensure the directory exists before binding the socket.
	os.MkdirAll(filepath.Dir(socketPath), 0700)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	// Restrict socket to owner-only. All agents run as the same user.
	os.Chmod(socketPath, 0700)

	t.safeGo(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // Listener closed.
			}
			t.safeGo(func() { t.handleIPCConn(conn) })
		}
	})

	cleanup = func() {
		ln.Close()
		os.Remove(socketPath)
	}
	return cleanup, nil
}

func (t *TUI) handleIPCConn(conn net.Conn) {
	defer conn.Close()

	// Prevent goroutine leak from clients that connect but never send data.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		LogWarn("ipc", "invalid JSON from client", "err", err)
		writeIPCResponse(conn, IPCResponse{Error: "invalid JSON"})
		return
	}
	LogDebug("ipc", "request", "action", req.Action, "target", req.Target)

	switch req.Action {
	case "send":
		// injectText holds sendMu for the duration of text delivery; clear the
		// read deadline so it doesn't race with the initial 5s timeout.
		// Other actions respond in microseconds and don't need the deadline cleared.
		conn.SetReadDeadline(time.Time{})
		t.handleIPCSend(conn, req)
	case "peek":
		t.handleIPCPeek(conn, req)
	case "list":
		t.handleIPCList(conn)
	case "stop":
		t.handleIPCStop(conn, req)
	case "start":
		t.handleIPCStart(conn, req)
	case "restart":
		t.handleIPCRestart(conn, req)
	case "bead":
		t.handleIPCBead(conn, req)
	case "patrol":
		t.handleIPCPatrol(conn, req)
	case "add":
		t.handleIPCAdd(conn, req)
	case "remove":
		t.handleIPCRemove(conn, req)
	case "quit":
		t.handleIPCQuit(conn)
	default:
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
	}
}

func (t *TUI) handleIPCSend(conn net.Conn, req IPCRequest) {
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

	t.injectText(pane, req.Text, req.Enter)
	writeIPCResponse(conn, IPCResponse{OK: true})
}

// injectText sends text into a pane's PTY via keystroke injection. Acquires
// sendMu to serialize against concurrent sends. If enter is true, a single
// newline is appended. Safe to call from any goroutine.
func (t *TUI) injectText(pane *Pane, text string, enter bool) {
	// Serialize sends to this pane so concurrent callers don't interleave keystrokes.
	pane.sendMu.Lock()
	defer pane.sendMu.Unlock()

	// Stash any partially typed input before injecting so that the incoming
	// message doesn't corrupt text the user was composing (ini-gd0).
	// Ctrl+S in Claude Code stashes the current input line and restores it
	// after the next response. This was removed in ini-a1e.20 but that removal
	// was incorrect — the stash/restore is essential to preserve pending input.
	pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: 's', Mod: uv.ModCtrl}))
	time.Sleep(75 * time.Millisecond)

	// Send each character as a key event through the emulator,
	// same path as real keypresses from the TUI.
	for _, r := range text {
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: r, Text: string(r)}))
	}

	if enter {
		// Send exactly one Enter. If Claude Code's paste dialog absorbs it,
		// the user can press Enter manually. One missed Enter is better than
		// multiple unwanted ones that would submit restored pending input (ini-bd2).
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
	}
}

func (t *TUI) handleIPCPeek(conn net.Conn, req IPCRequest) {
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
	writeIPCResponse(conn, IPCResponse{OK: true, Data: peekContent(pane, req.Lines)})
}

func (t *TUI) handleIPCPatrol(conn net.Conn, req IPCRequest) {
	lines := req.Lines
	if lines <= 0 {
		lines = 20
	}
	type patrolEntry struct {
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Bead     string `json:"bead,omitempty"`
		Alive    bool   `json:"alive"`
		Visible  bool   `json:"visible"`
		Content  string `json:"content"`
	}
	var result []patrolEntry
	t.runOnMain(func() {
		result = make([]patrolEntry, len(t.panes))
		for i, p := range t.panes {
			result[i] = patrolEntry{
				Name:     p.name,
				Activity: p.Activity().String(),
				Bead:     p.BeadID(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[p.name],
				Content:  peekContent(p, lines),
			}
		}
	})
	data, _ := json.Marshal(result)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

// peekContent extracts the last N lines of terminal content from a pane's
// emulator. Returns the content as a string with newline-separated lines.
// If lines <= 0, returns all non-blank content.
func peekContent(p *Pane, lines int) string {
	cols := p.emu.Width()
	emuRows := p.emu.Height()

	allLines := make([]string, emuRows)
	for row := 0; row < emuRows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := p.emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		allLines[row] = strings.TrimRight(line.String(), " ")
	}

	// Strip trailing blank lines.
	contentEnd := emuRows
	for contentEnd > 0 && allLines[contentEnd-1] == "" {
		contentEnd--
	}
	allLines = allLines[:contentEnd]

	// Return last N lines.
	if lines > 0 && lines < len(allLines) {
		allLines = allLines[len(allLines)-lines:]
	}

	var buf strings.Builder
	for _, line := range allLines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.String()
}

func (t *TUI) handleIPCList(conn net.Conn) {
	type paneInfo struct {
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Alive    bool   `json:"alive"`
		Visible  bool   `json:"visible"`
	}
	var panes []paneInfo
	t.runOnMain(func() {
		panes = make([]paneInfo, len(t.panes))
		for i, p := range t.panes {
			panes[i] = paneInfo{
				Name:     p.name,
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[p.name],
			}
		}
	})
	data, _ := json.Marshal(panes)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

func (t *TUI) handleIPCBead(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required (set INITECH_AGENT or use --agent)"})
		return
	}
	// Validate bead ID text before touching TUI state.
	if len(req.Text) > 64 {
		writeIPCResponse(conn, IPCResponse{Error: "bead ID too long (max 64 chars)"})
		return
	}
	for _, ch := range req.Text {
		if ch < 0x20 || ch == 0x7F {
			writeIPCResponse(conn, IPCResponse{Error: "bead ID contains control characters"})
			return
		}
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
	pane.SetBead(req.Text, "")
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) findPane(name string) *Pane {
	for _, p := range t.panes {
		if p.name == name {
			return p
		}
	}
	return nil
}

// pasteIndicatorRe matches Claude Code's paste reference placeholder.
var pasteIndicatorRe = regexp.MustCompile(`\[Pasted text #\d+[^\]]*\]`)

// hasStuckInput reads the pane's emulator cells to detect whether the sent
// message is stuck in the input box. Two cases:
//
//  1. Paste indicator: "[Pasted text #N]" visible near the prompt.
//  2. Text at prompt: the last line containing the ❯ prompt character
//     has non-whitespace content after it.
//
// Returns false when no prompt is visible (Claude is generating) or
// the prompt is empty (message submitted successfully).
func hasStuckInput(p *Pane) bool {
	cols := p.emu.Width()
	rows := p.emu.Height()

	var lastPromptContent string
	promptFound := false

	for row := 0; row < rows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := p.emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		text := line.String()

		// Case 1: paste indicator anywhere.
		if pasteIndicatorRe.MatchString(text) {
			return true
		}

		// Track the last line with a ❯ prompt character.
		if idx := strings.LastIndex(text, "\u276f"); idx >= 0 {
			lastPromptContent = strings.TrimSpace(text[idx+len("\u276f"):])
			promptFound = true
		}
	}

	if !promptFound {
		return false // No prompt visible, Claude is generating.
	}
	return lastPromptContent != ""
}

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
	t.runOnMain(func() {
		for i, p := range t.panes {
			if p.name == req.Target {
				old = p
				oldIdx = i
				return
			}
		}
	})
	if old == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	if old.IsAlive() {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: "already running"})
		return
	}
	// Create the new pane off-main (may fork/exec).
	cols, rows := old.emu.Width(), old.emu.Height()
	np, err := NewPane(old.cfg, rows, cols)
	if err != nil {
		LogError("pane", "start failed", "name", req.Target, "err", err)
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("start failed: %v", err)})
		return
	}
	np.region = old.region
	np.eventCh = t.agentEvents
	np.safeGo = t.safeGo
	// Replace in t.panes on main; re-verify index is still valid.
	t.runOnMain(func() {
		if oldIdx < len(t.panes) && t.panes[oldIdx] == old {
			np.Start()
			t.panes[oldIdx] = np
			t.applyLayout()
		} else {
			np.Close() // Index shifted; discard new pane.
		}
	})
	LogInfo("pane", "started", "name", req.Target)
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
	t.runOnMain(func() {
		for i, p := range t.panes {
			if p.name == req.Target {
				old = p
				oldIdx = i
				return
			}
		}
	})
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
	// Create new pane off-main (may fork/exec).
	np, err := NewPane(old.cfg, rows, cols)
	if err != nil {
		LogError("pane", "restart failed", "name", req.Target, "err", err)
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("restart failed: %v", err)})
		return
	}
	np.region = old.region
	np.eventCh = t.agentEvents
	np.safeGo = t.safeGo
	// Replace in t.panes on main; re-verify index is still valid.
	t.runOnMain(func() {
		if oldIdx < len(t.panes) && t.panes[oldIdx] == old {
			np.Start()
			t.panes[oldIdx] = np
			t.applyLayout()
		} else {
			np.Close() // Index shifted; discard new pane.
		}
	})
	LogInfo("pane", "restarted", "name", req.Target)
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
	// Check name uniqueness on main (reads t.panes).
	var existsErr error
	t.runOnMain(func() {
		if t.findPane(name) != nil {
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
		if t.findPane(name) != nil {
			p.Close()
			finalErr = fmt.Errorf("agent %q already exists (added concurrently)", name)
			return
		}
		p.Start()
		t.panes = append(t.panes, p)
		// Recalculate grid for the new visible pane count.
		t.recalcGrid()
		t.applyLayout()
		t.saveLayoutIfConfigured()
	})
	if finalErr != nil {
		return finalErr
	}
	LogInfo("pane", "added", "name", name)
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
			if p.name == name {
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
		p.Close()

		// Remove from slice without leaving gaps.
		t.panes = append(t.panes[:idx], t.panes[idx+1:]...)

		// Clean up layout state references.
		if t.layoutState.Hidden != nil {
			delete(t.layoutState.Hidden, name)
		}
		// If this was the focused pane, clear focus so applyLayout snaps to next visible.
		if t.layoutState.Focused == name {
			t.layoutState.Focused = ""
		}

		t.recalcGrid()
		t.applyLayout()
		t.saveLayoutIfConfigured()
	})
	if removeErr != nil {
		return removeErr
	}
	LogInfo("pane", "removed", "name", name)
	return nil
}

// recalcGrid recomputes GridCols/GridRows from the current visible pane count
// and switches to LayoutGrid mode. Called after add or remove.
func (t *TUI) recalcGrid() {
	visCount := 0
	for _, p := range t.panes {
		if t.layoutState.Hidden == nil || !t.layoutState.Hidden[p.name] {
			visCount++
		}
	}
	cols, rows := autoGrid(visCount)
	t.layoutState.GridCols = cols
	t.layoutState.GridRows = rows
	t.layoutState.Mode = LayoutGrid
}

func (t *TUI) handleIPCQuit(conn net.Conn) {
	writeIPCResponse(conn, IPCResponse{OK: true})
	t.quitOnce.Do(func() { close(t.quitCh) })
}

// ipcAction is a closure dispatched to the TUI main event loop for safe
// access to unsynchronised TUI state (primarily t.panes). IPC goroutines
// must not read or write t.panes directly; all such access goes through
// runOnMain to serialise with the render loop.
type ipcAction struct {
	fn   func()
	done chan struct{}
}

// runOnMain dispatches fn to the TUI main event loop and blocks until it
// executes. Returns false if the TUI is shutting down (quitCh was closed).
// When ipcCh is nil (test contexts without a running event loop) fn is
// executed directly on the calling goroutine.
//
// Two-phase select: first, race the send against quit; second, race the
// completion signal against quit. This ensures quitCh always wins even when
// ipcCh has buffer space.
func (t *TUI) runOnMain(fn func()) bool {
	if t.ipcCh == nil {
		fn()
		return true
	}
	op := ipcAction{fn: fn, done: make(chan struct{})}
	select {
	case t.ipcCh <- op:
		// Op sent; wait for main loop to execute it.
		select {
		case <-op.done:
			return true
		case <-t.quitCh:
			return false
		}
	case <-t.quitCh:
		return false
	}
}

func writeIPCResponse(conn net.Conn, resp IPCResponse) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
	conn.Write([]byte("\n"))
}
