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

// maxSendTextLen is the maximum size of text accepted by the send IPC action.
// Prevents a misbehaving client from injecting megabytes of keystroke data
// that would freeze the TUI while processing rune-by-rune under sendMu.
const maxSendTextLen = 64 * 1024 // 64 KB

func (t *TUI) handleIPCSend(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	if len(req.Text) > maxSendTextLen {
		writeIPCResponse(conn, IPCResponse{Error: "text too large (max 64KB)"})
		return
	}

	// Look up the pane. The suspended-pane path needs *Pane for EnqueueMessage
	// and resumePane; the normal path uses PaneView.SendText.
	var pv PaneView
	var concretePane *Pane
	var queued bool
	if !t.runOnMain(func() {
		pv = t.findPaneByName(req.Target)
		if pv == nil {
			return
		}
		if lp, ok := pv.(*Pane); ok && lp.suspended {
			concretePane = lp
			dropped := lp.EnqueueMessage(req.Text, req.Enter)
			if dropped {
				t.notifications = append(t.notifications, notification{
					event: AgentEvent{
						Type:   EventAgentStalled,
						Pane:   lp.name,
						Detail: "Message queue full, oldest message dropped.",
						Time:   time.Now(),
					},
					expires: time.Now().Add(notificationTTL),
				})
			}
			queued = true
		}
	}) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	if queued {
		// Trigger resume-on-message: respawn the agent and drain the queue.
		// This blocks until the agent initializes and messages are delivered.
		if err := t.resumePane(concretePane, "incoming message"); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("resume failed: %v", err)})
			return
		}
		writeIPCResponse(conn, IPCResponse{OK: true, Data: `"resumed and delivered"`})
		return
	}

	// Normal path: deliver via PaneView.SendText (works for local and remote).
	pv.SendText(req.Text, req.Enter)

	// Log the send event (no toast, too frequent).
	preview := req.Text
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	t.runOnMain(func() {
		t.logEvent(AgentEvent{
			Type:   EventMessageSent,
			Pane:   req.Target,
			Detail: "Message sent to " + req.Target + ": " + preview,
		})
	})
	writeIPCResponse(conn, IPCResponse{OK: true})
}

// injectText sends text into a pane's PTY via keystroke injection. Acquires
// sendMu to serialize against concurrent sends. If enter is true, sends Enter
// with a smart single-retry that only fires when the prompt still holds our
// injected content or a paste reference (ini-f0d). Safe to call from any
// goroutine.
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
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))

		// Smart single-retry for paste dialog (ini-f0d): wait for the async
		// display pipeline to reflect the result, then check if input is still
		// stuck. 2s accounts for the round-trip: Claude Code detects the paste,
		// renders the dialog, sends escape sequences back, and readLoop writes
		// them to the emulator. If the stuck content looks like what we injected
		// (raw text or "[Pasted text #N ...]"), send one more Enter. If the
		// stuck content is something else (user's restored stashed input), do
		// NOT retry. Skip entirely for dead panes.
		if pane.IsAlive() {
			time.Sleep(2 * time.Second)
		}
		if pane.IsAlive() && hasStuckInput(pane) {
			content := getPromptContent(pane)
			if looksLikeInjectedText(content, text) {
				pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
			}
		}
	}
}

// getPromptContent extracts the text visible after the last ❯ prompt character
// in the pane's emulator. Returns the trimmed content, or empty string if no
// prompt is visible.
func getPromptContent(p *Pane) string {
	cols := p.emu.Width()
	rows := p.emu.Height()
	var lastPromptContent string

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
		if idx := strings.LastIndex(text, "\u276f"); idx >= 0 {
			lastPromptContent = strings.TrimSpace(text[idx+len("\u276f"):])
		}
	}
	return lastPromptContent
}

// looksLikeInjectedText returns true if the prompt content matches the text we
// just injected, either as a paste reference ("[Pasted text #N ...]"), an exact
// match (short messages), or a prefix match (medium messages truncated by the
// terminal width). Returns false for unrelated content such as the user's
// restored stashed input, preventing an unwanted retry Enter from submitting it.
func looksLikeInjectedText(promptContent, injectedText string) bool {
	if promptContent == "" {
		return false
	}
	// Paste reference: "[Pasted text #5 +1 lines]"
	if pasteIndicatorRe.MatchString(promptContent) {
		return true
	}
	// Exact match: short messages that fit entirely on the prompt line.
	if promptContent == injectedText {
		return true
	}
	// Prefix match: for messages longer than the terminal width, the prompt
	// row shows only the first N characters. Check that the injected text
	// starts with the visible prompt content.
	if strings.HasPrefix(injectedText, promptContent) {
		return true
	}
	return false
}

func (t *TUI) handleIPCPeek(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	var pv PaneView
	if !t.runOnMain(func() { pv = t.findPaneByName(req.Target) }) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Data: peekContent(pv, req.Lines)})
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
				Name:     p.Name(),
				Activity: p.Activity().String(),
				Bead:     p.BeadID(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[p.Name()],
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
func peekContent(p PaneView, lines int) string {
	cols := p.Emulator().Width()
	emuRows := p.Emulator().Height()

	allLines := make([]string, emuRows)
	for row := 0; row < emuRows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := p.Emulator().CellAt(col, row)
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
				Name:     p.Name(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[p.Name()],
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
	var pv PaneView
	if !t.runOnMain(func() { pv = t.findPaneByName(req.Target) }) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	pv.SetBead(req.Text, "")
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) findPane(name string) *Pane {
	for _, p := range t.panes {
		if p.Name() == name {
			if lp, ok := p.(*Pane); ok {
				return lp
			}
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
