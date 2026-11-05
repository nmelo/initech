package tui

// pace_crossing.go is the pace's one read beyond the bead poll. The poll knows
// a bead's updated_at and when its comment count rose; it does not know a
// comment made before this window started, or a commit. So when what the poll
// knows says a bead has crossed the rig's stuck-after value, the bead gets one
// read of its comments and the commits that name it, once per crossing and
// cached until the bead moves again. Every other tick is the one bd list.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// paceRead is what one crossing read found: the last move the poll knew when
// the read ran, and the true last move it found.
type paceRead struct {
	known time.Time
	at    time.Time
	by    string // who made that move: "status", a comment's author, or "commit"
}

// paceCandidate is a bead due a crossing read.
type paceCandidate struct {
	bead  progress.Bead
	known time.Time
}

// paceReadBudget bounds the crossing reads of one poll; what it does not reach
// stays uncounted and is read at the next poll.
const paceReadBudget = beadPollInterval

// bdBeadComments reads one bead's comments, bounded by ctx. A package variable
// so tests stand in for bd.
var bdBeadComments = func(ctx context.Context, dir, id string) ([]progress.Comment, error) {
	cmd := exec.CommandContext(ctx, "bd", "comments", id, "--json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var comments []progress.Comment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// gitBeadCommits reads the commits on the clone's origin/main that mention id,
// bounded by ctx, without fetching: progress.LastMove keeps those whose subject
// names the bead. A package variable so tests stand in for git.
var gitBeadCommits = func(ctx context.Context, clone, id string) ([]progress.Commit, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", clone, "log", "origin/main", "--fixed-strings", "--grep="+id, "-n", "20", "--format=%cI%x09%s")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var commits []progress.Commit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		when, subject, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if at, err := time.Parse(time.RFC3339, when); err == nil {
			commits = append(commits, progress.Commit{When: at, Subject: subject})
		}
	}
	return commits, nil
}

// paceClone is the clone the crossing read asks about commits: the first role's
// src checkout. "" when the project has no repo or no checkout, and then a
// crossing read makes no git call.
func (t *TUI) paceClone() string {
	if t.project == nil || len(t.project.Repos) == 0 {
		return ""
	}
	for _, role := range t.project.Roles {
		dir := filepath.Join(t.project.Root, role, "src")
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return ""
}

// paceBead is a polled bead as the pace counts it.
func (t *TUI) paceBead(b bdBead, now time.Time) progress.Bead {
	return progress.Bead{ID: b.ID, Title: b.Title, Status: b.Status, Assignee: b.Assignee, UpdatedAt: b.UpdatedAt, ClosedAt: b.ClosedAt,
		Moved: t.paceMoved(b.ID, now), Wait: t.paceWaits[b.ID], OwnerIdle: t.paceIdleFor[b.Assignee]}
}

// panePaceStates maps each pane to its state by name.
func (t *TUI) panePaceStates() map[string]string {
	panes := make(map[string]string, len(t.panes))
	for _, pv := range t.panes {
		panes[pv.Name()] = pv.Activity().String()
	}
	return panes
}

// paceCrossings marks and returns the beads due a read: those that read stuck
// or waiting by what the poll knows, and whose last known move has not been
// read yet. A bead read at this known move is not read again until it moves or
// its read's own last move crosses the value. Marked beads are not counted
// until their read lands. Main loop only.
func (t *TUI) paceCrossings(beads []bdBead, now time.Time) []paceCandidate {
	t.paceUnread = map[string]bool{}
	if !projectHasBeads(t.projectRoot) {
		return nil
	}
	rule := t.paceRule()
	panes := t.panePaceStates()
	var due []paceCandidate
	for _, b := range beads {
		pb := t.paceBead(b, now)
		if !progress.Stuck(pb, panes[pb.Assignee], rule, now) && !progress.Waiting(pb, panes[pb.Assignee], rule, now) {
			continue
		}
		known := pb.LastMoved()
		if r, ok := t.paceReads[b.ID]; ok && r.known.Equal(known) {
			continue
		}
		t.paceUnread[b.ID] = true
		due = append(due, paceCandidate{bead: pb, known: known})
	}
	return due
}

// readPaceCrossings runs the reads off the main loop: at most one bd call and
// one git call per bead. A failed read still counts as the read, so a bead
// never costs a call per tick, and the bead reads by what the poll knows.
func (t *TUI) readPaceCrossings(due []paceCandidate) {
	if len(due) == 0 {
		return
	}
	clone := t.paceClone()
	deadline := time.Now().Add(paceReadBudget)
	reads := make(map[string]paceRead, len(due))
	for _, c := range due {
		if time.Now().After(deadline) {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), beadPollTimeout)
		comments, err := bdBeadComments(ctx, t.projectRoot, c.bead.ID)
		if err != nil {
			LogWarn("beads", "crossing read: bd comments failed; the bead reads by the poll", "bead", c.bead.ID, "err", err)
		}
		var commits []progress.Commit
		if clone != "" {
			if commits, err = gitBeadCommits(ctx, clone, c.bead.ID); err != nil {
				LogWarn("beads", "crossing read: git log failed; the bead reads without commits", "bead", c.bead.ID, "err", err)
			}
		}
		cancel()
		at, by := progress.LastMove(c.bead, comments, commits)
		reads[c.bead.ID] = paceRead{known: c.known, at: at, by: by}
	}
	t.runOnMain(func() {
		if t.paceReads == nil {
			t.paceReads = map[string]paceRead{}
		}
		for id, r := range reads {
			t.paceReads[id] = r
			delete(t.paceUnread, id)
		}
		t.paceAt = time.Time{} // the lines show the answer at the next refresh
	})
}
