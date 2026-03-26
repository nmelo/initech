package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
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

// SocketPath returns the socket path for a project.
func SocketPath(projectName string) string {
	return fmt.Sprintf("/tmp/initech-%s.sock", projectName)
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

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	// Make socket world-writable so all agents can connect.
	os.Chmod(socketPath, 0777)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // Listener closed.
			}
			go t.handleIPCConn(conn)
		}
	}()

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
		writeIPCResponse(conn, IPCResponse{Error: "invalid JSON"})
		return
	}

	// Clear the read deadline so handlers that sleep (e.g., handleIPCSend
	// polling for stuck input) don't hit the original 5s timeout.
	conn.SetReadDeadline(time.Time{})

	switch req.Action {
	case "send":
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

	pane := t.findPane(req.Target)
	if pane == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}

	// Serialize sends to this pane so concurrent callers don't interleave keystrokes.
	pane.sendMu.Lock()
	defer pane.sendMu.Unlock()

	// Stash any pending user input with Ctrl+S before injecting text.
	// This prevents corruption when the agent has a partially typed message.
	pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: 's', Mod: uv.ModCtrl}))
	time.Sleep(75 * time.Millisecond)

	// Send each character as a key event through the emulator,
	// same path as real keypresses from the TUI.
	for _, r := range req.Text {
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: r, Text: string(r)}))
	}

	if req.Enter {
		// Brief pause to let text settle before sending Enter.
		time.Sleep(50 * time.Millisecond)
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))

		// Poll for stuck input (paste dialog or text still at prompt).
		// Claude Code's paste detection fires for fast input, so the first
		// Enter may confirm the paste reference rather than submitting.
		// Check immediately, then retry every 100ms for up to 1s.
		// Bail early if the pane is killed (e.g., by a concurrent stop).
		for range 10 {
			time.Sleep(100 * time.Millisecond)
			if !pane.IsAlive() || !hasStuckInput(pane) {
				break
			}
			pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
		}
	}

	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCPeek(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}

	pane := t.findPane(req.Target)
	if pane == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}

	// Use emulator dimensions, not region.InnerSize(), because hidden
	// panes have stale regions that return (1,0).
	cols := pane.emu.Width()
	emuRows := pane.emu.Height()

	// Extract all rows from the emulator as text.
	lines := make([]string, emuRows)
	for row := 0; row < emuRows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := pane.emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		lines[row] = strings.TrimRight(line.String(), " ")
	}

	// Strip trailing blank lines to find actual content end.
	// In non-alt-screen mode, content grows from the top and the bottom
	// of the buffer is blank. In alt-screen mode (vim, less), the full
	// buffer is typically populated.
	contentEnd := emuRows
	for contentEnd > 0 && lines[contentEnd-1] == "" {
		contentEnd--
	}
	lines = lines[:contentEnd]

	// If caller requested N lines, return the last N.
	if req.Lines > 0 && req.Lines < len(lines) {
		lines = lines[len(lines)-req.Lines:]
	}

	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	writeIPCResponse(conn, IPCResponse{OK: true, Data: buf.String()})
}

func (t *TUI) handleIPCList(conn net.Conn) {
	type paneInfo struct {
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Alive    bool   `json:"alive"`
		Visible  bool   `json:"visible"`
	}
	panes := make([]paneInfo, len(t.panes))
	for i, p := range t.panes {
		panes[i] = paneInfo{
			Name:     p.name,
			Activity: p.Activity().String(),
			Alive:    p.IsAlive(),
			Visible:  !t.layoutState.Hidden[p.name],
		}
	}
	data, _ := json.Marshal(panes)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

func (t *TUI) handleIPCBead(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required (set INITECH_AGENT or use --agent)"})
		return
	}
	pane := t.findPane(req.Target)
	if pane == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	// req.Text = bead ID (empty string to clear).
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
	pane := t.findPane(req.Target)
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
	idx := -1
	for i, p := range t.panes {
		if p.name == req.Target {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	old := t.panes[idx]
	if old.IsAlive() {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: "already running"})
		return
	}
	cols, rows := old.emu.Width(), old.emu.Height()
	np, err := NewPane(old.cfg, rows, cols)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("start failed: %v", err)})
		return
	}
	np.region = old.region
	t.panes[idx] = np
	t.applyLayout()
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCRestart(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	idx := -1
	for i, p := range t.panes {
		if p.name == req.Target {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	old := t.panes[idx]
	// Wait for any in-flight send to finish before closing.
	old.sendMu.Lock()
	cols, rows := old.emu.Width(), old.emu.Height()
	old.Close()
	old.sendMu.Unlock()
	np, err := NewPane(old.cfg, rows, cols)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("restart failed: %v", err)})
		return
	}
	np.region = old.region
	t.panes[idx] = np
	t.applyLayout()
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCQuit(conn net.Conn) {
	writeIPCResponse(conn, IPCResponse{OK: true})
	if t.quitCh != nil {
		close(t.quitCh)
	}
}

func writeIPCResponse(conn net.Conn, resp IPCResponse) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
	conn.Write([]byte("\n"))
}
