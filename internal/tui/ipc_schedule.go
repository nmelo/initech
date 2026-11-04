package tui

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/nmelo/initech/internal/schedule"
)

// CronAddRequest is the job initech cron add asks window 1 to keep (ini-6ib).
// It travels as JSON in the request's text; the owner is the request's From.
type CronAddRequest struct {
	To      string `json:"to"`
	At      string `json:"at,omitempty"`
	Every   string `json:"every,omitempty"`
	Message string `json:"message,omitempty"`
	Command string `json:"command,omitempty"`
}

// CronListing is one row of initech cron list: the job and its next run.
type CronListing struct {
	schedule.Job
	Next time.Time `json:"next"`
}

// cronCaller is the agent behind conn: worked out from the connection, the way
// initech post is attributed, never from what the request says about itself.
func (t *TUI) cronCaller(conn net.Conn) (string, error) {
	if t.sched.identify != nil {
		return t.sched.identify(conn)
	}
	id, err := t.identifyPostConnection(conn)
	if err != nil {
		return "", err
	}
	return id.agent, nil
}

// handleIPCCron answers cron_list, cron_add and cron_remove. The schedule lives
// in window 1, so any other host answers that it keeps none. A job's owner, and
// who may remove it, is the agent whose pane made the request.
func (t *TUI) handleIPCCron(conn net.Conn, req IPCRequest) {
	if t.sched == nil {
		writeIPCResponse(conn, IPCResponse{Error: "this window keeps no schedule: window 1 of a rig does"})
		return
	}
	switch req.Action {
	case "cron_list":
		data, _ := json.Marshal(t.sched.snapshot())
		writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
	case "cron_add":
		var add CronAddRequest
		if err := json.Unmarshal([]byte(req.Text), &add); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: "unreadable job: " + err.Error()})
			return
		}
		owner, err := t.cronCaller(conn)
		if err != nil {
			writeIPCResponse(conn, IPCResponse{Error: "a job is owned by the agent that adds it: " + err.Error()})
			return
		}
		if t.sched.isAgent != nil && !t.sched.isAgent(owner) {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("%q is not an agent of this rig, so it cannot own a job", owner)})
			return
		}
		job := schedule.Job{Owner: owner, To: add.To, At: add.At, Every: add.Every,
			Message: add.Message, Command: add.Command, Created: t.sched.now()}
		var id string
		err = t.sched.change(func(f *schedule.File) error {
			var e error
			id, e = f.Add(job)
			return e
		})
		if err != nil {
			writeIPCResponse(conn, IPCResponse{Error: err.Error()})
			return
		}
		writeIPCResponse(conn, IPCResponse{OK: true, Data: fmt.Sprintf("added %s, owned by %s", id, owner)})
	case "cron_remove":
		by, err := t.cronCaller(conn)
		if err != nil {
			writeIPCResponse(conn, IPCResponse{Error: "removing a job needs to know which agent asks: " + err.Error()})
			return
		}
		var gone schedule.Job
		err = t.sched.change(func(f *schedule.File) error {
			var e error
			gone, e = f.Remove(req.Target, by)
			return e
		})
		if err != nil {
			writeIPCResponse(conn, IPCResponse{Error: err.Error()})
			return
		}
		writeIPCResponse(conn, IPCResponse{OK: true, Data: fmt.Sprintf("removed %s (owner %s, to %s)", gone.ID, gone.Owner, gone.To)})
	}
}
