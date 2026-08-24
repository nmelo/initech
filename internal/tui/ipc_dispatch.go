// ipc_dispatch.go provides a shared IPC action dispatch used by both the TUI
// and headless daemon. Adding a new IPC action requires one change here instead
// of maintaining parallel switch statements.
package tui

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// PaneInfo is the JSON structure returned by the "list" IPC action.
type PaneInfo struct {
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	Activity string `json:"activity"`
	Alive    bool   `json:"alive"`
	Visible  bool   `json:"visible"`

	// Modal-latch visibility (ini-gbqc): omitted entirely when no latch is up.
	ModalLatched bool `json:"modal_latched,omitempty"`
	ModalAgeSec  int  `json:"modal_age_sec,omitempty"`
	QueuedCount  int  `json:"queued_count,omitempty"`
}

// IPCHost provides the runtime-specific capabilities needed by the shared
// IPC dispatch. Both TUI and Daemon implement this interface.
type IPCHost interface {
	// FindPaneView looks up a pane by name. Returns (nil, true) if not found.
	// Returns (nil, false) if the host is shutting down and the lookup could
	// not be performed.
	FindPaneView(name string) (PaneView, bool)

	// AllPanes returns status info for all managed panes. The bool is false
	// if the host is shutting down.
	AllPanes() ([]PaneInfo, bool)

	// HandleSend processes the "send" action. Each runtime has different
	// send semantics (TUI: suspended pane resume, remote forwarding;
	// Daemon: client routing, auto-forward).
	HandleSend(conn net.Conn, req IPCRequest)

	// HandleExtended processes runtime-specific actions not covered by
	// the shared dispatch (e.g. TUI lifecycle commands). Returns true if
	// the action was handled, false if it should fall through to "unknown".
	HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool
}

// dispatchIPC routes an IPC request to the appropriate handler. Shared actions
// (peek, list) are handled here. Send is delegated to h.HandleSend. Anything
// else goes to h.HandleExtended.
func dispatchIPC(h IPCHost, conn net.Conn, req IPCRequest, rawJSON []byte) {
	switch req.Action {
	case "send":
		conn.SetReadDeadline(time.Time{})
		h.HandleSend(conn, req)

	case "peek":
		if req.Target == "" {
			writeIPCResponse(conn, IPCResponse{Error: "target is required"})
			return
		}
		pv, ok := h.FindPaneView(req.Target)
		if !ok {
			writeIPCResponse(conn, IPCResponse{Error: "shutting down"})
			return
		}
		if pv == nil {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
			return
		}
		writeIPCResponse(conn, IPCResponse{OK: true, Data: peekContent(pv, req.Lines)})

	case "list":
		panes, ok := h.AllPanes()
		if !ok {
			writeIPCResponse(conn, IPCResponse{Error: "shutting down"})
			return
		}
		data, _ := json.Marshal(panes)
		writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})

	default:
		if !h.HandleExtended(conn, req, rawJSON) {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
		}
	}
}
