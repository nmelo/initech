package tui

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/rignumbers"
)

// rigNumbersEvery is how often window 1 reads the repo and computes the rig's
// numbers. gh and git never run more often; status and the board read the
// cached result.
const rigNumbersEvery = time.Minute

// rigRun runs the repo reads (gh) for the rig numbers. A package variable so
// tests stand in for gh and count its calls.
var rigRun rignumbers.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// rigNumbersState is window 1's rig numbers: the last result, the crossing
// state of each threshold, and the once-a-minute gate.
type rigNumbersState struct {
	last time.Time   // main loop only: when the last gather started
	busy atomic.Bool // a gather is running; a second never starts beside it

	mu      sync.Mutex
	numbers *rignumbers.Numbers

	// The gather goroutine only; busy keeps it to one at a time.
	alarm  rignumbers.Alarm
	ghDown bool // the last repo read failed and was logged
}

// rigSlug is the owner/repo of the rig's first repo, or "" when it has none
// on GitHub.
func rigSlug(p *config.Project) string {
	if p == nil || len(p.Repos) == 0 {
		return ""
	}
	slug, _ := rignumbers.Slug(p.Repos[0].URL)
	return slug
}

// rigInput is what the main loop knows now: the beads of the last answered
// poll (no bd call of its own) and the panes' states.
func (t *TUI) rigInput(now time.Time) rignumbers.Input {
	in := rignumbers.Input{Now: now, Panes: t.panePaceStates()}
	if t.paceAnswered {
		in.BeadsKnown = true
		for _, b := range t.paceBeads {
			in.Beads = append(in.Beads, rignumbers.Bead{
				ID: b.ID, Status: b.Status, Assignee: b.Assignee,
				CreatedAt: b.CreatedAt, ClosedAt: b.ClosedAt,
				LastMoved: t.paceBead(b, now).LastMoved(),
			})
		}
	}
	return in
}

// refreshRigNumbers runs from the main loop's tick. In window 1, at most once
// a minute and never beside a gather still running, it reads the repo on its
// own goroutine, computes the numbers, keeps them for status and the board,
// and sends super one line for each threshold crossed from inside to outside.
func (t *TUI) refreshRigNumbers(now time.Time) {
	if !t.isFleetAuthority() || t.project == nil {
		return
	}
	r := &t.rig
	if !r.last.IsZero() && now.Sub(r.last) < rigNumbersEvery {
		return
	}
	if !r.busy.CompareAndSwap(false, true) {
		return
	}
	r.last = now
	in := t.rigInput(now)
	slug := rigSlug(t.project)
	th := t.project.Numbers.Thresholds()
	t.safeGo(func() {
		defer r.busy.Store(false)
		err := rignumbers.GatherRepo(context.Background(), rigRun, slug, &in)
		if err != nil && !r.ghDown {
			LogWarn("rig-numbers", "repo numbers unknown; the gate and merge numbers print as ?", "err", err)
		}
		r.ghDown = err != nil
		n := rignumbers.Compute(in)
		r.mu.Lock()
		r.numbers = &n
		r.mu.Unlock()
		for _, line := range r.alarm.Check(n, th) {
			t.sendInitechLine(line)
		}
	})
}

// RigNumbersSnapshot is window 1's last numbers, or nil before the first
// gather finishes. Safe from any goroutine.
func (t *TUI) RigNumbersSnapshot() *rignumbers.Numbers {
	t.rig.mu.Lock()
	defer t.rig.mu.Unlock()
	if t.rig.numbers == nil {
		return nil
	}
	n := *t.rig.numbers
	return &n
}

// rigRowPrefix starts the board's rig numbers row.
const rigRowPrefix = "rig: "

// boardRows are the board's rows under its columns: the effort rows, then
// the rig numbers as the last row once window 1 has them.
func (t *TUI) boardRows() []string {
	n := t.RigNumbersSnapshot()
	if n == nil {
		return t.paceRows
	}
	rows := make([]string, 0, len(t.paceRows)+1)
	rows = append(rows, t.paceRows...)
	return append(rows, rigRowPrefix+n.Line())
}
