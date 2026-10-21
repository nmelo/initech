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

	// Beads is what the pane's ribbon shows, always sent ([] when empty) so a
	// client can tell an empty ribbon from an older TUI that sends no field.
	// assign reads it to add one bead to a ribbon or take one off it without
	// dropping the others.
	Beads []string `json:"beads"`

	// NoBead is the no-bead marker's words when the pane is working without a
	// bead (Beads is then empty); omitted otherwise. initech status shows it
	// where a bead id would go.
	NoBead string `json:"no_bead,omitempty"`

	// Modal-latch visibility (ini-gbqc): omitted entirely when no latch is up.
	ModalLatched bool `json:"modal_latched,omitempty"`
	ModalAgeSec  int  `json:"modal_age_sec,omitempty"`
	QueuedCount  int  `json:"queued_count,omitempty"`
}

// ribbonBeads is the pane's bead set for the list reply, never nil, so it
// encodes as [] rather than null when the ribbon is empty.
func ribbonBeads(p PaneView) []string {
	ids := p.BeadIDs()
	if ids == nil {
		return []string{}
	}
	return ids
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
		// IPC handler goroutine: may wait on the pane (ini-oxnl), so a burst
		// returns the screen rather than "unreadable".
		writeIPCResponse(conn, IPCResponse{OK: true, Data: peekContentBlocking(pv, req.Lines)})

	case "list":
		panes, ok := h.AllPanes()
		if !ok {
			writeIPCResponse(conn, IPCResponse{Error: "shutting down"})
			return
		}
		data, _ := json.Marshal(panes)
		response := IPCResponse{OK: true, Data: string(data)}
		if source, ok := h.(interface{ WindowPortSnapshot() *WindowPortStatus }); ok {
			response.WindowPort = source.WindowPortSnapshot()
		}
		if source, ok := h.(interface{ ProjectSnapshot() *IPCProject }); ok {
			response.Project = source.ProjectSnapshot()
		}
		writeIPCResponse(conn, response)

	default:
		if !h.HandleExtended(conn, req, rawJSON) {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
		}
	}
}
