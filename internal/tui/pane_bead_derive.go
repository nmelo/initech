package tui

// pane_bead_derive.go derives each pane's bead from bd (ini-wra.1): the bead
// whose assignee is the pane's role and whose status is in_progress. initech
// bead <id> is a manual override that wins until initech bead --clear, which
// returns the pane to the derived bead at once. Every reader (the ribbon,
// initech status and patrol, status --json, the second windows' broadcast)
// reads the one effective value, Pane.effectiveBeadsLocked.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/lifecycle"
	"github.com/nmelo/initech/internal/progress"
)

// beadPollInterval is how often window 1 asks bd for the in_progress beads,
// and beadPollTimeout the bound on one call. A failed or slow call keeps every
// pane's last derived bead and marks it stale.
const (
	beadPollInterval = 10 * time.Second
	beadPollTimeout  = 5 * time.Second
)

// BeadStaleNote is what status, patrol and the ribbon show for a pane whose
// derived bead could not be refreshed because bd did not answer.
const BeadStaleNote = "bead? bd unreachable"

// bdBead is one bead from bd list --json, the fields the derivation reads.
type bdBead struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Assignee  string    `json:"assignee"`
	UpdatedAt time.Time `json:"updated_at"`
	ClosedAt  time.Time `json:"closed_at"`
	// CreatedAt is when the bead was filed; the rig numbers count the hour's.
	CreatedAt time.Time `json:"created_at"`
	// CommentCount is bd's comment count. A comment does not move updated_at,
	// so the pace reads a rise in this count as the bead's last move.
	CommentCount int `json:"comment_count"`
	// Priority and Labels order and filter the rig panel's moved box
	// (ini-9z0b): efforts by priority then id, archived beads never shown.
	Priority int      `json:"priority"`
	Labels   []string `json:"labels,omitempty"`
	// Dependencies and Metadata carry a waiting bead's wait (pace_waits.go).
	Dependencies []progress.Dependency `json:"dependencies,omitempty"`
	Metadata     json.RawMessage       `json:"metadata,omitempty"`
}

// bdListArgs asks bd for every bead, closed ones included (bd lists 50 open
// ones by default).
var bdListArgs = []string{"list", "--all", "--limit", "0", "--json"}

// bdListBeads runs bd list --all --limit 0 --json in dir, bounded by ctx:
// every bead, so one call serves both the derived bead (the in_progress ones)
// and the pane header's pace (closed of total, ini-y0n.2). A package variable
// so tests stand in for bd.
var bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) {
	cmd := exec.CommandContext(ctx, "bd", bdListArgs...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var beads []bdBead
	if err := json.Unmarshal(out, &beads); err != nil {
		return nil, err
	}
	return beads, nil
}

// projectHasBeads reports whether dir is a bd project this machine can read:
// a .beads directory there and bd on PATH. Without both there is nothing to
// derive from, so the pane shows only what initech bead sets, and no stale
// mark (a project that does not use bd is not one whose bd is down).
var projectHasBeads = func(dir string) bool {
	if dir == "" {
		return false
	}
	if fi, err := os.Stat(filepath.Join(dir, ".beads")); err != nil || !fi.IsDir() {
		return false
	}
	_, err := exec.LookPath("bd")
	return err == nil
}

// deriveBead picks role's bead from bd's in_progress beads: the one bead
// assigned to role, or with two or more, the one the pane last named with
// initech bead, else the newest updated. ok is false when role holds none.
func deriveBead(role string, beads []bdBead, lastNamed string) (b bdBead, ok bool) {
	for _, c := range beads {
		if c.Assignee != role || c.Status != "in_progress" {
			continue
		}
		switch {
		case !ok:
			b, ok = c, true
		case b.ID == lastNamed:
			// The named bead already holds the place.
		case c.ID == lastNamed || c.UpdatedAt.After(b.UpdatedAt):
			b = c
		}
	}
	return b, ok
}

// askBdBeads asks bd once for the in_progress beads, bounded by
// beadPollTimeout. answered is false when the call failed or ran past the
// bound; the caller then keeps each pane's last bead and marks it stale.
func askBdBeads(dir string) (beads []bdBead, answered bool) {
	ctx, cancel := context.WithTimeout(context.Background(), beadPollTimeout)
	defer cancel()
	beads, err := bdListBeads(ctx, dir)
	if err != nil {
		LogWarn("beads", "bd list failed; panes keep their last bead, marked stale", "err", err)
		return nil, false
	}
	return beads, true
}

// bdStatusCustom reads the rig's status.custom, bounded by ctx. A package
// variable so tests stand in for bd.
var bdStatusCustom = func(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "bd", "config", "get", "status.custom")
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// readPaceChain reads the rig's working chain once, off the main loop, until
// bd answers: the only bd call the pace makes beyond the poll's own, never per
// tick once answered.
func (t *TUI) readPaceChain() {
	if t.paceChainRead.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), beadPollTimeout)
	defer cancel()
	raw, err := bdStatusCustom(ctx, t.projectRoot)
	if err != nil {
		LogWarn("beads", "bd status.custom not read; only in_progress beads can read stuck", "err", err)
		return
	}
	t.paceChainRead.Store(true)
	working := lifecycle.Parse(raw).Working()
	t.runOnMain(func() { t.paceChain = working })
}

// pollBeadsOnce asks bd once, off the main loop, and applies the answer to
// every local pane on it.
func (t *TUI) pollBeadsOnce(now time.Time) {
	t.readPaceChain()
	beads, answered := askBdBeads(t.projectRoot)
	var due []paceCandidate
	t.runOnMain(func() {
		if answered {
			t.notePaceComments(beads, now)
			due = t.paceCrossings(beads, now)
		}
		t.notePaceAnswer(beads, answered)
		for _, pv := range t.panes {
			if p, ok := pv.(*Pane); ok {
				p.applyDerivedBeads(beads, answered, now)
			}
		}
	})
	t.readPaceCrossings(due)
	if answered {
		t.judgePaceWaits(beads, now)
		t.pollKinds(beads, now)
	}
}

// startBeadPoller polls bd for the panes' beads: once at once, then every
// beadPollInterval, until quit closes. Only the fleet authority (window 1)
// polls; a second window shows what window 1 broadcasts. A project without bd
// starts nothing.
func (t *TUI) startBeadPoller(quit <-chan struct{}) {
	if !t.isFleetAuthority() || !projectHasBeads(t.projectRoot) {
		return
	}
	t.startPanelKinds()
	t.safeGo(func() {
		t.pollBeadsOnce(time.Now())
		tick := time.NewTicker(beadPollInterval)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case now := <-tick.C:
				t.pollBeadsOnce(now)
			}
		}
	})
}

// runBeadPoller is the daemon's bead poll: the same call and the same rule as
// window 1's, over the panes this daemon hosts, until stop closes. A project
// without bd polls nothing.
func (d *Daemon) runBeadPoller(stop <-chan struct{}) {
	if d.project == nil || !projectHasBeads(d.project.Root) {
		return
	}
	poll := func(now time.Time) {
		beads, answered := askBdBeads(d.project.Root)
		for _, p := range d.snapshotPanes() {
			p.applyDerivedBeads(beads, answered, now)
		}
	}
	poll(time.Now())
	tick := time.NewTicker(beadPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-tick.C:
			poll(now)
		}
	}
}
