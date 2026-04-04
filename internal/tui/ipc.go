package tui

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/nmelo/initech/internal/config"
)

const (
	rawSubmitDelay            = 200 * time.Millisecond
	bracketedPasteSubmitDelay = 500 * time.Millisecond
	codexBracketedSubmitDelay = 200 * time.Millisecond
)

// IPCRequest is the JSON structure sent by CLI commands to the TUI socket.
type IPCRequest struct {
	Action string `json:"action"` // "send", "peek", "list", "peers_query"
	Target string `json:"target"` // Role name (for send/peek).
	Host   string `json:"host"`   // Remote peer name (for cross-machine send). Empty = local.
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
		// Include random suffix to prevent socket-squatting attacks.
		var b [8]byte
		rand.Read(b[:])
		return fmt.Sprintf("/tmp/initech-%s-%s.sock", projectName, hex.EncodeToString(b[:]))
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

	scanner := NewIPCScanner(conn)
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

	dispatchIPC(t, conn, req, scanner.Bytes())
}

// ── IPCHost implementation ─────────────────────────────────────────

func (t *TUI) FindPaneView(name string) (PaneView, bool) {
	var pv PaneView
	ok := t.runOnMain(func() { pv = t.findPaneByName(name) })
	return pv, ok
}

func (t *TUI) AllPanes() ([]PaneInfo, bool) {
	var panes []PaneInfo
	ok := t.runOnMain(func() {
		panes = make([]PaneInfo, len(t.panes))
		for i, p := range t.panes {
			panes[i] = PaneInfo{
				Name:     p.Name(),
				Host:     p.Host(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[paneKey(p)],
			}
		}
	})
	return panes, ok
}

func (t *TUI) HandleSend(conn net.Conn, req IPCRequest) {
	t.handleIPCSend(conn, req)
}

func (t *TUI) Timers() *TimerStore {
	return t.timers
}

func (t *TUI) NotifyConfig() (webhookURL, project string) {
	return t.webhookURL, t.projectName
}

func (t *TUI) HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool {
	switch req.Action {
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
	case "peers_query":
		t.handleIPCPeers(conn)
	case "quit":
		t.handleIPCQuit(conn)
	default:
		return false
	}
	return true
}

// maxSendTextLen is the maximum size of text accepted by the send IPC action.
// Prevents a misbehaving client from injecting megabytes of keystroke data
// that would freeze the TUI while processing rune-by-rune under sendMu.
const maxSendTextLen = 64 * 1024 // 64 KB

// IPCScanBufSize is the buffer limit for all IPC and control-stream scanners.
// Must exceed maxSendTextLen plus JSON framing overhead so that a legal
// near-limit send is tokenized successfully before the explicit size check
// runs. 256 KB gives ~4x headroom over the 64 KB text limit (ini-piyb.2).
const IPCScanBufSize = 256 * 1024

// NewIPCScanner creates a bufio.Scanner with a buffer large enough to handle
// the largest supported IPC/control message (maxSendTextLen + JSON framing).
func NewIPCScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, IPCScanBufSize), IPCScanBufSize)
	return s
}

func (t *TUI) handleIPCSend(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	if len(req.Text) > maxSendTextLen {
		writeIPCResponse(conn, IPCResponse{Error: "text too large (max 64KB)"})
		return
	}

	// Cross-machine routing: if Host is set, look up by host:agent.
	// Empty host or host matching our own peer name resolves locally.
	if req.Host != "" {
		localPeerName := ""
		if t.project != nil {
			localPeerName = t.project.PeerName
		}
		if req.Host != localPeerName {
			t.forwardSendToRemote(conn, req)
			return
		}
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

// injectText sends text into a pane's PTY. Two modes based on the pane's
// noBracketedPaste config:
//
// Bracketed paste (default, for Claude Code): wraps text in ESC[200~/ESC[201~
// markers and writes directly to the PTY. Claude Code's parser handles this
// natively, collapsing large pastes (>800 chars) to references.
//
// Raw PTY write (noBracketedPaste=true, for OpenCode and generic agents):
// writes the message body directly to the PTY without bracketed paste markers.
//
// Codex is handled specially: current Codex enables bracketed paste in the TUI,
// while its non-bracketed paste-burst detector intentionally turns fast
// Enter-after-text sequences into a newline instead of submit. For Codex-like
// panes we therefore inject the body as direct bracketed paste bytes on the PTY
// and then submit separately.
//
// Safe to call from any goroutine.
func sendPaneTextLocked(pane *Pane, text string, enter bool) {
	if pane.ptmx == nil {
		return
	}
	useCodexBracketedPaste := pane.noBracketedPaste && pane.AgentType() == config.AgentTypeCodex
	codexQueueSubmit := pane.AgentType() == config.AgentTypeCodex && pane.Activity() == StateRunning
	ready := true
	if config.IsCodexLikeAgentType(pane.AgentType()) && !codexQueueSubmit {
		ready = pane.waitForCodexReady(codexReadyTimeout)
		LogDebug("inject", "codex ready wait", "pane", pane.Name(), "ready", ready, "timeout_ms", codexReadyTimeout.Milliseconds())
	} else if codexQueueSubmit {
		LogDebug("inject", "codex queueing while running", "pane", pane.Name())
	}
	mode := "bracketed"
	if pane.noBracketedPaste {
		mode = "raw"
	}
	if useCodexBracketedPaste {
		mode = "codex-bracketed"
	}
	LogDebug("inject", "send start", "pane", pane.Name(), "agent_type", pane.AgentType(), "mode", mode, "bytes", len(text), "enter", enter)
	// Stash any partially typed input before injecting so that the incoming
	// message doesn't corrupt text the user was composing (ini-gd0). Codex/raw
	// panes skip this because they inject directly to the PTY instead of through
	// the emulator keystroke path.
	stashed := false
	if !pane.noBracketedPaste {
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: 's', Mod: uv.ModCtrl}))
		time.Sleep(75 * time.Millisecond)
		stashed = true
	}

	if useCodexBracketedPaste {
		var buf []byte
		buf = append(buf, "\x1b[200~"...)
		buf = append(buf, text...)
		buf = append(buf, "\x1b[201~"...)
		n, err := pane.ptmx.Write(buf)
		LogDebug("inject", "body written", "pane", pane.Name(), "mode", mode, "bytes", n, "err", err)
	} else if pane.noBracketedPaste {
		// Direct PTY write for text bytes, but route Enter through the emulator.
		// The raw text path avoids the emulator's key-to-byte translation for the
		// message body, which Codex can misinterpret. For the final submit key we
		// deliberately use the emulator path so Enter encoding stays aligned with
		// the PTY/terminal mode the emulator is already managing.
		var buf []byte
		buf = append(buf, text...)
		n, err := pane.ptmx.Write(buf)
		LogDebug("inject", "body written", "pane", pane.Name(), "mode", mode, "bytes", n, "err", err)
	} else {
		// Bracketed paste: ESC[200~ + text + ESC[201~ directly to PTY.
		var buf []byte
		buf = append(buf, "\x1b[200~"...)
		buf = append(buf, text...)
		buf = append(buf, "\x1b[201~"...)
		n, err := pane.ptmx.Write(buf)
		LogDebug("inject", "body written", "pane", pane.Name(), "mode", mode, "bytes", n, "err", err)
	}

	if !enter {
		return
	}

	if useCodexBracketedPaste {
		time.Sleep(codexBracketedSubmitDelay)
		method := sendCodexSubmit(pane, codexQueueSubmit)
		LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", method, "delay_ms", codexBracketedSubmitDelay.Milliseconds())
		if pane.IsAlive() && !stashed {
			// Retry only when no stash was active. After a stash, Claude Code
			// auto-restores the stashed text into the prompt, making
			// promptHasContent a false positive (ini-vxw).
			time.Sleep(bracketedPasteSubmitDelay)
			if promptHasContent(pane) {
				method = sendCodexSubmit(pane, codexQueueSubmit)
				LogDebug("inject", "submit retry", "pane", pane.Name(), "mode", mode, "method", method)
			}
		}
		return
	}

	if pane.noBracketedPaste {
		// Let Codex's non-bracketed paste-burst detection expire before sending
		// Enter. The 8ms parser window observed in source was not enough in the
		// full PTY/TUI pipeline; use a larger margin here.
		time.Sleep(rawSubmitDelay)
		LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", "pty-enter", "delay_ms", rawSubmitDelay.Milliseconds())
		if config.IsCodexLikeAgentType(pane.AgentType()) && pane.submitKey != "ctrl+enter" {
			_, _ = pane.ptmx.Write([]byte("\r"))
			return
		}
		sendSubmitKey(pane.emu, pane.submitKey)
		return
	}

	// Bracketed paste mode: wait for Claude Code's paste completion
	// (100ms PASTE_COMPLETION_TIMEOUT_MS) plus async rendering time.
	time.Sleep(bracketedPasteSubmitDelay)
	LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", "emulator", "delay_ms", bracketedPasteSubmitDelay.Milliseconds())
	sendSubmitKey(pane.emu, pane.submitKey)

	// Single retry for large pastes where Enter was swallowed. Skip when a
	// stash was active because Claude Code auto-restores stashed text into the
	// prompt after submit, making promptHasContent a false positive that would
	// submit the operator's unfinished text (ini-vxw).
	if pane.IsAlive() && !stashed {
		time.Sleep(bracketedPasteSubmitDelay)
		if promptHasContent(pane) {
			LogDebug("inject", "submit retry", "pane", pane.Name(), "mode", mode, "method", "emulator")
			sendSubmitKey(pane.emu, pane.submitKey)
		}
	}
}

func (t *TUI) injectText(pane *Pane, text string, enter bool) {
	pane.sendMu.Lock()
	defer pane.sendMu.Unlock()
	sendPaneTextLocked(pane, text, enter)
}

// promptHasContent checks if the last ❯ prompt line has non-whitespace content.
// Used as a lightweight retry signal: if content remains after Enter, it was
// likely swallowed during paste processing.
func promptHasContent(p *Pane) bool {
	cols := p.emu.Width()
	rows := p.emu.Height()
	for row := rows - 1; row >= 0; row-- {
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
		for _, prompt := range []string{"\u276f", "\u203a", ">"} {
			if idx := strings.LastIndex(text, prompt); idx >= 0 {
				return strings.TrimSpace(text[idx+len(prompt):]) != ""
			}
		}
	}
	return false
}

func sendCodexSubmit(pane *Pane, queue bool) string {
	if queue {
		_, _ = pane.ptmx.Write([]byte("\t"))
		return "pty-tab"
	}
	if pane.submitKey != "ctrl+enter" {
		_, _ = pane.ptmx.Write([]byte("\r"))
		return "pty-enter"
	}
	sendSubmitKey(pane.emu, pane.submitKey)
	return "emulator-ctrl+enter"
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
				Visible:  !t.layoutState.Hidden[paneKey(p)],
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
		LogInfo("runOnMain", "ipcCh is nil, executing directly on caller goroutine")
		fn()
		return true
	}
	op := ipcAction{fn: fn, done: make(chan struct{})}
	LogInfo("runOnMain", "queued op", "pending", len(t.ipcCh), "cap", cap(t.ipcCh))
	select {
	case t.ipcCh <- op:
		LogInfo("runOnMain", "op sent, waiting for execution")
		select {
		case <-op.done:
			LogInfo("runOnMain", "op executed")
			return true
		case <-t.quitCh:
			LogInfo("runOnMain", "quit while waiting")
			return false
		}
	case <-t.quitCh:
		LogInfo("runOnMain", "quit while sending")
		return false
	}
}

// forwardSendToRemote routes a send request to a remote peer by finding the
// RemotePane with matching Host() and calling SendText. The RemotePane's
// SendText sends the command over the yamux control stream to the daemon.
func (t *TUI) forwardSendToRemote(conn net.Conn, req IPCRequest) {
	var target PaneView
	t.runOnMain(func() {
		for _, p := range t.panes {
			if p.Host() == req.Host && p.Name() == req.Target {
				target = p
				return
			}
		}
	})
	if target == nil {
		writeIPCResponse(conn, IPCResponse{
			Error: fmt.Sprintf("agent %q not found on peer %q. Run 'initech peers' to see available targets.", req.Target, req.Host),
		})
		return
	}
	target.SendText(req.Text, req.Enter)
	writeIPCResponse(conn, IPCResponse{OK: true})
}

// PeerInfo describes a peer and its agents for the peers_query response.
type PeerInfo struct {
	Name   string   `json:"name"`
	Agents []string `json:"agents"`
}

// handleIPCPeers builds a peer table from local + remote panes and responds
// with a JSON list of PeerInfo.
func (t *TUI) handleIPCPeers(conn net.Conn) {
	var peers []PeerInfo
	t.runOnMain(func() {
		// Group panes by host.
		groups := make(map[string][]string)
		localName := "local"
		if t.project != nil && t.project.PeerName != "" {
			localName = t.project.PeerName
		}
		for _, p := range t.panes {
			host := p.Host()
			if host == "" {
				host = localName
			}
			groups[host] = append(groups[host], p.Name())
		}
		// Local peer first, then remotes in alphabetical order.
		if agents, ok := groups[localName]; ok {
			peers = append(peers, PeerInfo{Name: localName, Agents: agents})
			delete(groups, localName)
		}
		for name, agents := range groups {
			peers = append(peers, PeerInfo{Name: name, Agents: agents})
		}
	})
	data, _ := json.Marshal(peers)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

func writeIPCResponse(conn net.Conn, resp IPCResponse) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
	conn.Write([]byte("\n"))
}
