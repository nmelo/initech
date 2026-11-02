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
	// AgentType is the agent type of a pane this process runs ("claude-code",
	// "codex", "opencode", "generic"); omitted for a pane it does not run, so
	// a client reads it as unknown. initech compact refuses non-Claude agents
	// by it (ini-sf5.1).
	AgentType string `json:"agent_type,omitempty"`

	// Beads is what the pane's ribbon shows, always sent ([] when empty) so a
	// client can tell an empty ribbon from an older TUI that sends no field.
	// assign reads it to add one bead to a ribbon or take one off it without
	// dropping the others.
	Beads []string `json:"beads"`

	// NoBead is the no-bead marker's words when the pane is working without a
	// bead (Beads is then empty); omitted otherwise. initech status shows it
	// where a bead id would go.
	NoBead string `json:"no_bead,omitempty"`
	// BeadStale is true while the derived bead shown is the last one bd gave:
	// bd did not answer the last poll (ini-wra.1). Omitted when false.
	BeadStale bool `json:"bead_stale,omitempty"`
	// Unattributed is true when the pane has worked fifteen minutes with no
	// bead, derived or set (ini-wra.3). Omitted when false.
	Unattributed bool `json:"unattributed,omitempty"`

	// Modal-latch visibility (ini-gbqc): omitted entirely when no latch is up.
	ModalLatched bool `json:"modal_latched,omitempty"`
	ModalAgeSec  int  `json:"modal_age_sec,omitempty"`
	QueuedCount  int  `json:"queued_count,omitempty"`

	// Do-not-disturb (ini-x432): DND is sent true or false for every pane
	// this process runs and omitted for one it does not, so a client never
	// reads a guess; DNDAgeSec only while it is on.
	DND       *bool `json:"dnd,omitempty"`
	DNDAgeSec *int  `json:"dnd_age_sec,omitempty"`

	// Read from the pane's screen only when the list request asks for detail
	// (initech status --json, ini-l6om), and only for a live pane. Each is
	// omitted when it was not read, so a client reads it as null; an older
	// client ignores them and an older TUI never sends them.
	ContextPercent *int            `json:"context_percent,omitempty"`
	Background     *PaneBackground `json:"background,omitempty"`
	Banner         string          `json:"banner,omitempty"`
	Account        *PaneAccount    `json:"account,omitempty"`

	// Busy is the background work the pane's footer showed at the last tick,
	// sent only while there is some (ini-b4l0).
	Busy *PaneBusy `json:"busy,omitempty"`

	// ContextSoon is the pane's context figure, sent only while it is at or
	// above ContextFlagAt (ini-sf5.2), so initech status flags it without
	// asking for detail.
	ContextSoon *int `json:"context_soon,omitempty"`

	// SessionFile is the pane's newest Claude Code session transcript, the
	// file its journal reads, sent with detail only (initech app-serve
	// renders it as the conversation). Empty for a pane with none.
	SessionFile string `json:"session_file,omitempty"`
}

// PaneBusy is a pane's background work as the maintenance tick last read it
// from the footer, and how long it has been seen.
type PaneBusy struct {
	Shells   int `json:"shells"`
	Monitors int `json:"monitors"`
	Tasks    int `json:"tasks"`
	SinceSec int `json:"since_sec"`
	// SeenFor marks SinceSec as how long the footer has shown the work, not
	// a running process's age: the process tree was not read (ini-d2sx).
	SeenFor bool `json:"seen_for,omitempty"`
}

// PaneBackground is the background work Claude Code's footer shows. A nil
// count was not read.
type PaneBackground struct {
	Shells   *int `json:"shells,omitempty"`
	Monitors *int `json:"monitors,omitempty"`
	Tasks    *int `json:"tasks,omitempty"`
}

// PaneAccount is the account and model the operator's status line shows. A
// nil field was not read.
type PaneAccount struct {
	User  *string `json:"user,omitempty"`
	Model *string `json:"model,omitempty"`
}

// fillPaneDetail reads a live pane's screen into its list entry: footer
// values and the dialog kind, and any pane's newest transcript file. Context and account the footer could not read
// come from the pane's transcript and cla's active account (paneContext). It
// waits on the emulator's lock, so it runs on the IPC goroutine, never the
// main loop. A pane that is not alive shows a screen from before it stopped,
// so it is not read.
func fillPaneDetail(info *PaneInfo, pv PaneView) {
	if p, ok := pv.(*Pane); ok && p != nil && p.jsonlDir != "" {
		// A stopped pane's transcript is still its conversation.
		info.SessionFile = p.newestTranscript()
	}
	if pv == nil || !info.Alive {
		return
	}
	emu := pv.Emulator()
	if emu == nil {
		return
	}
	rows := emuRowsBlocking(emu)
	f := parseFooter(rows, emu.Width())
	p, _ := pv.(*Pane)
	ctx, user, model := paneContext(f, p)
	info.ContextPercent = ctx
	if f.Shells != nil || f.Monitors != nil || f.Tasks != nil {
		info.Background = &PaneBackground{Shells: f.Shells, Monitors: f.Monitors, Tasks: f.Tasks}
	}
	if user != nil || model != nil {
		info.Account = &PaneAccount{User: user, Model: model}
	}
	info.Banner = bannerKind(rows, info.ModalLatched)
}

// beadStaleOf reports pv's stale-bead mark; false for a pane without one.
func beadStaleOf(pv PaneView) bool {
	if p, ok := pv.(*Pane); ok {
		return p.BeadStale()
	}
	return false
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
		r := peekReadBlocking(pv, req.Lines)
		resp := IPCResponse{OK: true, Data: r.text}
		if notice := peekNotice(r, req.Lines); notice != "" {
			resp.Notices = []string{notice}
		}
		writeIPCResponse(conn, resp)

	case "list":
		var panes []PaneInfo
		var ok bool
		detail, canDetail := h.(interface {
			allPanesWithViews() ([]PaneInfo, []PaneView, bool)
		})
		if req.Detail && canDetail {
			var views []PaneView
			panes, views, ok = detail.allPanesWithViews()
			for i := range panes {
				fillPaneDetail(&panes[i], views[i])
			}
		} else {
			panes, ok = h.AllPanes()
		}
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

	case "dnd":
		handleIPCDND(h, conn, req)

	case "keystroke":
		handleIPCKeystroke(h, conn, req)

	default:
		if !h.HandleExtended(conn, req, rawJSON) {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
		}
	}
}
