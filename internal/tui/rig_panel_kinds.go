package tui

// rig_panel_kinds.go fills the moved box's change kinds (ini-u2xf): the
// beadkind reader (the beads journal, or bd history once the journal misses
// a move), plus the two kinds bd cannot give, a commit from the crossing
// read and a comment from the poll's count. The box reads the newest.

import (
	"context"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/nmelo/initech/internal/beadkind"
)

// kindsRunner runs the reader's bd calls in dir. A package variable so tests
// stand in for bd.
var kindsRunner = func(dir string) beadkind.Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		return c.Output()
	}
}

// panelKindsState is the reader and its one-at-a-time poll.
type panelKindsState struct {
	reader *beadkind.Reader
	busy   atomic.Bool
	spawn  func(func()) // nil runs on safeGo; tests run it inline
}

// startPanelKinds makes the reader and sets it behind the moved box. Window
// 1 only, beside the bead poller; main loop.
func (t *TUI) startPanelKinds() {
	if t.kinds.reader != nil {
		return
	}
	t.kinds.reader = beadkind.NewReader(beadkind.Options{
		Run:       kindsRunner(t.projectRoot),
		Log:       func(line string) { LogInfo("beads", line) },
		TellSuper: t.sendInitechLine,
	})
	t.panelKinds = &panelKindSource{t: t}
}

// pollKinds hands one answered bead poll to the reader, off the poll's own
// goroutine and never beside a poll still running: a journal catch-up or 20
// history reads may take seconds, and the bead poll must not wait on them.
func (t *TUI) pollKinds(beads []bdBead, now time.Time) {
	r := t.kinds.reader
	if r == nil || !t.kinds.busy.CompareAndSwap(false, true) {
		return
	}
	in := make([]beadkind.Bead, 0, len(beads))
	for _, b := range beads {
		in = append(in, beadkind.Bead{ID: b.ID, Status: b.Status, Assignee: b.Assignee, UpdatedAt: b.UpdatedAt, CommentCount: b.CommentCount})
	}
	run := t.kinds.spawn
	if run == nil {
		run = t.safeGo
	}
	run(func() {
		defer t.kinds.busy.Store(false)
		r.Poll(context.Background(), in, now)
	})
}

// panelKindSource is the moved box's beadKinds: the newest of the reader's
// kind, a commit the crossing read found, and a comment the poll saw.
type panelKindSource struct{ t *TUI }

// Kind is id's newest known change. Main loop only: it reads the pace's maps.
func (k *panelKindSource) Kind(id string) (string, bool) {
	t := k.t
	kind, at, known := "", time.Time{}, false
	if r := t.kinds.reader; r != nil {
		if rk, rat, ok := r.KindAt(id); ok {
			// A blank the reader recorded is known: the newest change maps
			// to no kind, and the box shows blank, not the status kind.
			kind, at, known = string(rk), rat, true
		}
	}
	if pr, ok := t.paceReads[id]; ok && pr.by == "commit" && pr.at.After(at) {
		kind, at, known = string(beadkind.Commit), pr.at, true
	}
	if c := t.paceCommentAt[id]; !c.IsZero() && c.After(at) {
		kind, known = string(beadkind.Comment), true
	}
	return kind, known
}
