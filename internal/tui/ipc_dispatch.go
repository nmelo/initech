// ipc_dispatch.go provides a shared IPC action dispatch used by both the TUI
// and headless daemon. Adding a new IPC action requires one change here instead
// of maintaining parallel switch statements.
package tui

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/nmelo/initech/internal/webhook"
)

// PaneInfo is the JSON structure returned by the "list" IPC action.
type PaneInfo struct {
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	Activity string `json:"activity"`
	Alive    bool   `json:"alive"`
	Visible  bool   `json:"visible"`
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

	// Timers returns the timer store, or nil if timers are not available.
	Timers() *TimerStore

	// NotifyConfig returns the webhook URL and project name for posting
	// notifications. Returns empty strings if webhook is not configured.
	NotifyConfig() (webhookURL, project string)

	// HandleExtended processes runtime-specific actions not covered by
	// the shared dispatch (e.g. TUI lifecycle commands). Returns true if
	// the action was handled, false if it should fall through to "unknown".
	HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool
}

// dispatchIPC routes an IPC request to the appropriate handler. Shared actions
// (peek, list, schedule, list_timers, cancel_timer) are handled here. Send is
// delegated to h.HandleSend. Anything else goes to h.HandleExtended.
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

	case "schedule":
		dispatchSchedule(h, conn, rawJSON)
	case "list_timers":
		dispatchListTimers(h, conn)
	case "cancel_timer":
		dispatchCancelTimer(h, conn, req)

	case "notify":
		dispatchNotify(h, conn, req)

	default:
		if !h.HandleExtended(conn, req, rawJSON) {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
		}
	}
}

func dispatchSchedule(h IPCHost, conn net.Conn, rawJSON []byte) {
	var req struct {
		Target string `json:"target"`
		Host   string `json:"host"`
		Text   string `json:"text"`
		Enter  bool   `json:"enter"`
		FireAt string `json:"fire_at"`
	}
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		writeIPCResponse(conn, IPCResponse{Error: "invalid schedule request"})
		return
	}
	fireAt, err := time.Parse(time.RFC3339, req.FireAt)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("invalid fire_at: %v", err)})
		return
	}
	ts := h.Timers()
	if ts == nil {
		writeIPCResponse(conn, IPCResponse{Error: "timer store not initialized"})
		return
	}
	timer, err := ts.Add(req.Target, req.Host, req.Text, req.Enter, fireAt)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Data: timer.ID})
}

func dispatchListTimers(h IPCHost, conn net.Conn) {
	ts := h.Timers()
	if ts == nil {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: "[]"})
		return
	}
	timers := ts.List()
	data, _ := json.Marshal(timers)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

func dispatchCancelTimer(h IPCHost, conn net.Conn, req IPCRequest) {
	ts := h.Timers()
	if ts == nil {
		writeIPCResponse(conn, IPCResponse{Error: "timer store not initialized"})
		return
	}
	timer, err := ts.Cancel(req.Text)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	target := timer.Target
	if timer.Host != "" {
		target = timer.Host + ":" + target
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Data: fmt.Sprintf("%s (%s at %s)", timer.ID, target, timer.FireAt.Local().Format("15:04"))})
}

// dispatchNotify handles the "notify" IPC action. It reads the message from
// req.Text and optional kind from req.Target (overloaded), then POSTs directly
// to the configured webhook URL.
func dispatchNotify(h IPCHost, conn net.Conn, req IPCRequest) {
	if req.Text == "" {
		writeIPCResponse(conn, IPCResponse{Error: "message required"})
		return
	}
	webhookURL, project := h.NotifyConfig()
	if webhookURL == "" {
		writeIPCResponse(conn, IPCResponse{Error: "no webhook_url configured"})
		return
	}
	kind := req.Target // overload Target as kind
	if kind == "" {
		kind = "custom"
	}
	agent := req.Host // overload Host as agent name
	if err := webhook.PostNotification(webhookURL, kind, agent, req.Text, project); err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Data: "sent"})
}
