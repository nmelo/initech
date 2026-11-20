package tui

// ipc_watch.go is the IPC side of initech watch (set, list, cancel): the
// wire shapes the command sends and reads. The request is JSON in
// IPCRequest.Text, as cron's is; the owner is never in the request, it is
// worked out from the connection.

import (
	"encoding/json"
	"net"
	"strconv"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// WatchSetRequest is initech watch set's request (action watch_set): one
// condition of a fixed kind with its params, the reason line, the deadline as
// an absolute time, and whether a fired line waits for a sleeping owner.
type WatchSetRequest struct {
	Kind         watch.Kind        `json:"kind"`
	Params       map[string]string `json:"params,omitempty"`
	Why          string            `json:"why"`
	Deadline     time.Time         `json:"deadline"`
	HoldIfAsleep bool              `json:"hold_if_asleep,omitempty"`
}

// WatchListRequest is initech watch list's request (action watch_list): the
// asker's own watches, or with All every watch in the rig.
type WatchListRequest struct {
	All bool `json:"all,omitempty"`
}

// WatchListing is one row of initech watch list: the watch, the other owners
// whose watches share its check, and whether its line will wake a sleeping
// owner (its owner is suspended and it was not set to hold until it wakes).
type WatchListing struct {
	watch.Watch
	Sharers []string `json:"sharers,omitempty"`
	Wakes   bool     `json:"wakes,omitempty"`
}

// watchIdentify is the agent behind conn, worked out from the connection as
// initech cron and post are, never from what the request says. "" means the
// command ran outside any agent's pane. Tests stand in for it.
var watchIdentify = func(t *TUI, conn net.Conn) string {
	id, err := t.identifyConnection(conn)
	if err != nil {
		return ""
	}
	return id.agent
}

// watchValidatorFor is the condition check the handler runs at set time:
// 2B's watchValidate on the window's bead poll. Tests stand in for it.
var watchValidatorFor = func(t *TUI) watchValidator { return t.watchValidate }

// watchHandlerNow is the clock the watch handler stamps with; tests set it.
var watchHandlerNow = time.Now

// handleIPCWatch answers watch_set, watch_list and watch_cancel. The store
// lives in window 1 and is only touched on the main goroutine; a second
// window and the serve daemon keep none, and say so.
func (t *TUI) handleIPCWatch(conn net.Conn, req IPCRequest) {
	owner := watchIdentify(t, conn)
	now := watchHandlerNow()
	var set WatchSetRequest
	if req.Action == "watch_set" {
		// Read and checked here, on the IPC goroutine: 2B's check reads the
		// bead poll itself and must not run on the main loop.
		if err := json.Unmarshal([]byte(req.Text), &set); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: "unreadable watch: " + err.Error()})
			return
		}
		if err := watchCheck(watchValidatorFor(t), owner, set); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: err.Error()})
			return
		}
	}
	var resp IPCResponse
	ran := t.runOnMain(func() {
		if t.watches == nil {
			resp = IPCResponse{Error: watchNoStoreLine}
			return
		}
		switch req.Action {
		case "watch_set":
			id, err := watchAdd(t.watches, owner, set, now)
			if err == nil {
				t.armWatchTimer(now) // 2B: a deadline nearer than the pass's next one
				t.publishWatchedReceipts(now)
			}
			resp = watchReply(id, err)
		case "watch_list":
			var list WatchListRequest
			if req.Text != "" {
				if err := json.Unmarshal([]byte(req.Text), &list); err != nil {
					resp = IPCResponse{Error: "unreadable list request: " + err.Error()}
					return
				}
			}
			rows, err := watchList(t.watches, owner, list, now, watchKey, t.watchWakes)
			if err != nil {
				resp = IPCResponse{Error: err.Error()}
				return
			}
			data, _ := json.Marshal(rows)
			resp = IPCResponse{OK: true, Data: string(data)}
		case "watch_cancel":
			out, err := watchCancel(t.watches, owner, req.Target, now)
			resp = watchReply(out, err)
		case "watch_count":
			// Read-only: how many waiting watches req.Target holds, for the
			// "kept N watches" line of initech clear and restart.
			resp = IPCResponse{OK: true, Data: strconv.Itoa(t.watches.WaitingFor(req.Target))}
		}
	})
	if !ran {
		resp = IPCResponse{Error: "initech is shutting down; nothing was changed"}
	}
	writeIPCResponse(conn, resp)
}

func watchReply(data string, err error) IPCResponse {
	if err != nil {
		return IPCResponse{Error: err.Error()}
	}
	return IPCResponse{OK: true, Data: data}
}

// watchWakes says whether a waiting watch's line will wake its owner: the
// owner's pane is suspended and the watch was not set to hold until it wakes.
// Main goroutine.
func (t *TUI) watchWakes(w watch.Watch) bool {
	if w.HoldIfAsleep {
		return false
	}
	for _, pv := range t.panes {
		if p, ok := pv.(*Pane); ok && p.name == w.Owner {
			return p.IsSuspended()
		}
	}
	return false
}

// watchNoStoreLine is the refusal of a host that keeps no watches: a second
// window, the serve daemon, or window 1 before its store loads. Restarting
// would not help, so it is never the older-initech answer.
const watchNoStoreLine = "this initech keeps no watches: window 1 of a rig does (not a second window or initech serve)"

// isWatchAction says whether action is one of initech watch's.
func isWatchAction(action string) bool {
	switch action {
	case "watch_set", "watch_list", "watch_cancel", "watch_count":
		return true
	}
	return false
}
