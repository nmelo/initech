package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
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
func (t *TUI) startIPC(socketPath string) error {
	// Clean up stale socket.
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	// Make socket world-writable so all agents can connect.
	os.Chmod(socketPath, 0777)

	go func() {
		defer ln.Close()
		defer os.Remove(socketPath)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // Listener closed.
			}
			go t.handleIPCConn(conn)
		}
	}()

	return nil
}

func (t *TUI) handleIPCConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeIPCResponse(conn, IPCResponse{Error: "invalid JSON"})
		return
	}

	switch req.Action {
	case "send":
		t.handleIPCSend(conn, req)
	case "peek":
		t.handleIPCPeek(conn, req)
	case "list":
		t.handleIPCList(conn)
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

	text := req.Text
	if req.Enter {
		text += "\n"
	}

	_, err := pane.ptmx.Write([]byte(text))
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("write failed: %v", err)})
		return
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

	cols, rows := pane.region.InnerSize()
	if req.Lines > 0 && req.Lines < rows {
		rows = req.Lines
	}

	var buf strings.Builder
	emuRows := pane.emu.Height()
	for row := 0; row < rows && row < emuRows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := pane.emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		buf.WriteString(strings.TrimRight(line.String(), " "))
		buf.WriteByte('\n')
	}

	writeIPCResponse(conn, IPCResponse{OK: true, Data: buf.String()})
}

func (t *TUI) handleIPCList(conn net.Conn) {
	type paneInfo struct {
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Alive    bool   `json:"alive"`
	}
	panes := make([]paneInfo, len(t.panes))
	for i, p := range t.panes {
		panes[i] = paneInfo{
			Name:     p.name,
			Activity: p.Activity().String(),
			Alive:    p.IsAlive(),
		}
	}
	data, _ := json.Marshal(panes)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

func (t *TUI) findPane(name string) *Pane {
	for _, p := range t.panes {
		if p.name == name {
			return p
		}
	}
	return nil
}

func writeIPCResponse(conn net.Conn, resp IPCResponse) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
	conn.Write([]byte("\n"))
}
