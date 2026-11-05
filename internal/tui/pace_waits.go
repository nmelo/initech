package tui

// pace_waits.go judges each waiting bead's wait inside the bead poll window 1
// already runs: a wait on another bead or a date from the poll's own list and
// the clock, a tag with git ls-remote and a run with gh run view, each asked at
// most once a minute per target and only for a bead in waiting. When a wait is
// over, super hears one line, once per bead and target.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// paceWaitEvery is the least time between two questions about one target.
const paceWaitEvery = time.Minute

// gitTagExists reports whether the repo at url has refs/tags/<name>, bounded
// by ctx. A package variable so tests stand in for git.
var gitTagExists = func(ctx context.Context, url, name string) (bool, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--tags", url, "refs/tags/"+name).Output()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "refs/tags/"+name), nil
}

// ghRunStatus is GitHub's status for run id in slug, bounded by ctx; exists is
// false for a run GitHub does not know. A package variable so tests stand in.
var ghRunStatus = func(ctx context.Context, slug, id string) (status string, exists bool, err error) {
	cmd := exec.CommandContext(ctx, "gh", "run", "view", id, "--repo", slug, "--json", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if m := strings.ToLower(string(out)); strings.Contains(m, "not found") || strings.Contains(m, "could not find") {
			return "", false, nil
		}
		return "", true, err
	}
	var v struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", true, err
	}
	return v.Status, true, nil
}

// paceAnswer is the last answer about one target and when it was asked.
type paceAnswer struct {
	at     time.Time
	done   bool // the tag exists, or the run completed: final, never asked again
	exists bool // false: the run is unknown to GitHub
}

// paceWorld is progress.External for the TUI: answers come from the cache
// when the last question is under a minute old or final.
type paceWorld struct {
	t    *TUI
	now  time.Time
	url  string
	slug string
}

func (w paceWorld) ask(key string, fn func(context.Context) (done, exists bool, err error)) (bool, bool, error) {
	if a, ok := w.t.paceAnswers[key]; ok && (a.done || !a.exists || w.now.Sub(a.at) < paceWaitEvery) {
		return a.done, a.exists, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), beadPollTimeout)
	defer cancel()
	done, exists, err := fn(ctx)
	if err != nil {
		LogWarn("waits", "wait not judged; it stays open", "target", key, "err", err)
		w.t.paceAnswers[key] = paceAnswer{at: w.now, exists: true}
		return false, true, err
	}
	if !exists {
		LogWarn("waits", "the run named by wait_on does not exist", "target", key)
	}
	w.t.paceAnswers[key] = paceAnswer{at: w.now, done: done, exists: exists}
	return done, exists, nil
}

func (w paceWorld) TagExists(name string) (bool, error) {
	if w.url == "" {
		return false, fmt.Errorf("no repo in initech.yaml")
	}
	done, _, err := w.ask("tag:"+name, func(ctx context.Context) (bool, bool, error) {
		ok, err := gitTagExists(ctx, w.url, name)
		return ok, true, err
	})
	return done, err
}

func (w paceWorld) RunCompleted(id string) (bool, bool, error) {
	if w.slug == "" {
		return false, true, fmt.Errorf("no GitHub repo in initech.yaml")
	}
	return w.ask("run:"+id, func(ctx context.Context) (bool, bool, error) {
		st, exists, err := ghRunStatus(ctx, w.slug, id)
		return st == "completed", exists, err
	})
}

// paceRepo is the rig's first repo: its clone URL and GitHub owner/repo.
func (t *TUI) paceRepo() (url, slug string) {
	if t.project == nil || len(t.project.Repos) == 0 {
		return "", ""
	}
	url = t.project.Repos[0].URL
	u := strings.TrimSuffix(strings.TrimSpace(url), ".git")
	for _, pre := range []string{"https://", "http://", "ssh://", "git@"} {
		u = strings.TrimPrefix(u, pre)
	}
	if i := strings.IndexAny(u, ":/"); i >= 0 {
		if parts := strings.Split(strings.Trim(u[i+1:], "/"), "/"); len(parts) >= 2 {
			slug = parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
	}
	return url, slug
}

// judgePaceWaits judges every waiting bead of one poll, on the poll's own
// goroutine (the answer cache is that goroutine's alone), then records the
// answers on the main loop and sends super one line for each wait newly over,
// once per bead and target. A bead not in waiting is never judged.
func (t *TUI) judgePaceWaits(beads []bdBead, now time.Time) {
	var waiting []progress.Bead
	idx := progress.Index{Status: make(map[string]string, len(beads)), ClosedAt: make(map[string]time.Time, len(beads))}
	for _, b := range beads {
		idx.Status[b.ID] = b.Status
		if !b.ClosedAt.IsZero() {
			idx.ClosedAt[b.ID] = b.ClosedAt
		}
		if b.Status == "waiting" {
			waiting = append(waiting, progress.Bead{ID: b.ID, Status: b.Status, Dependencies: b.Dependencies, Metadata: b.Metadata})
		}
	}
	if t.paceAnswers == nil {
		t.paceAnswers = map[string]paceAnswer{}
	}
	url, slug := t.paceRepo()
	world := paceWorld{t: t, now: now, url: url, slug: slug}
	judged := make(map[string]progress.WaitState, len(waiting))
	records := make(map[string]string, len(waiting))
	for _, b := range waiting {
		records[b.ID] = progress.WaitOverSent(b)
		cs, fresh := t.waitComments(b.ID, now)
		if !fresh {
			// The comments could not be read: the last judgement stands, and
			// a bead never judged is not over (it might have a stated wait).
			if last, ok := t.paceWaitReads.last[b.ID]; ok {
				judged[b.ID] = last
			} else {
				judged[b.ID] = progress.WaitState{Target: progress.WaitTarget{Kind: progress.WaitOnUnknown}}
			}
			continue
		}
		b.WaitComments = cs
		judged[b.ID] = progress.JudgeIndexed(b, idx, now, world)
		t.paceWaitReads.last[b.ID] = judged[b.ID]
	}
	t.runOnMain(func() {
		t.paceWaits = judged
		if t.paceOverSent == nil {
			t.paceOverSent = map[string]bool{}
		}
		if t.paceOverWrote == nil {
			t.paceOverWrote = map[string]string{}
		}
		for _, b := range waiting {
			w := judged[b.ID]
			target := w.Target.String()
			// The bead's own record says which target its line went out for,
			// so a restarted process does not send it again. This process's
			// own write stands in for it until bd's list shows it.
			record := records[b.ID]
			if wrote, ok := t.paceOverWrote[b.ID]; ok {
				if wrote == record {
					delete(t.paceOverWrote, b.ID)
				}
				record = wrote
			}
			if !w.Over {
				if record != "" && record != target {
					t.writeWaitOverSent(b.ID, "") // a new target: the old record goes
				}
				continue
			}
			key := b.ID + "|" + target
			if t.paceOverSent[key] || record == target {
				t.paceOverSent[key] = true
				continue
			}
			t.paceOverSent[key] = true
			t.sendInitechLine(fmt.Sprintf("☾ %s waits on %s: %s; move it on", progress.Clean(b.ID), w.Target, w.Event))
			t.writeWaitOverSent(b.ID, target)
		}
		t.paceAt = time.Time{} // the lines show the answer at the next refresh
	})
}

// paceWaitCommentState is the comments of each waiting bead, read for its
// stated wait (a WAIT comment) at most once a minute. The poll goroutine's
// alone, as the answer cache is.
type paceWaitCommentState struct {
	read   map[string]paceWaitComments
	last   map[string]progress.WaitState // the last judgement made with comments
	failed map[string]bool               // a failed read already logged
}

type paceWaitComments struct {
	at time.Time
	cs []progress.Comment
	ok bool // the read at at succeeded
}

// waitComments is bead id's comments for its stated wait: read at most once
// a minute, each read bounded by beadPollTimeout. fresh is false when the
// newest read failed; a failure is logged once, naming the bead and never a
// comment's text.
func (t *TUI) waitComments(id string, now time.Time) ([]progress.Comment, bool) {
	s := &t.paceWaitReads
	if s.read == nil {
		s.read = map[string]paceWaitComments{}
		s.last = map[string]progress.WaitState{}
		s.failed = map[string]bool{}
	}
	c, have := s.read[id]
	if have && now.Sub(c.at) < paceWaitEvery {
		return c.cs, c.ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), beadPollTimeout)
	defer cancel()
	cs, err := bdBeadComments(ctx, t.projectRoot, id)
	if err != nil {
		if !s.failed[id] {
			s.failed[id] = true
			LogWarn("waits", "comments not read; the bead's last wait judgement stands", "bead", progress.Clean(id))
		}
		s.read[id] = paceWaitComments{at: now, cs: c.cs, ok: false}
		return c.cs, false
	}
	delete(s.failed, id)
	s.read[id] = paceWaitComments{at: now, cs: cs, ok: true}
	return cs, true
}

// paceWaitWriteTimeout bounds one wait_over_sent write.
const paceWaitWriteTimeout = 10 * time.Second

// writeWaitOverSent records target ("" clears) on bead id, off the main loop,
// once: the value is kept in paceOverWrote until bd's list shows it. A failed
// write is logged; this process still sends nothing twice. Main loop only.
func (t *TUI) writeWaitOverSent(id, target string) {
	if strings.HasPrefix(id, "-") {
		return // never an argument bd could read as a flag
	}
	t.paceOverWrote[id] = target
	root, write := t.projectRoot, bdSetWaitOverSent // the seam is read here, on the main loop
	t.safeGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), paceWaitWriteTimeout)
		defer cancel()
		if err := write(ctx, root, id, target); err != nil {
			LogWarn("waits", "wait_over_sent not recorded", "bead", progress.Clean(id), "err", err)
		}
	})
}

// overSpan is where " · N over" sits in an effort row, in runes; both -1
// when the row has none.
func overSpan(line string) (int, int) {
	rs := []rune(line)
	for i := 0; i+3 < len(rs); i++ {
		if string(rs[i:i+3]) != " · " {
			continue
		}
		j := i + 3
		for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
			j++
		}
		if j > i+3 && strings.HasPrefix(string(rs[j:]), " over") {
			return i + 3, j + len(" over")
		}
	}
	return -1, -1
}

// errBdWriteInTest refuses a real bd write from a test binary.
var errBdWriteInTest = errors.New("waits: refusing a real bd write from a test")

// bdSetWaitOverSent records on the bead that its over line went out for
// target (bd metadata wait_over_sent), or clears that record when target is
// "", so a restarted window 1 does not announce the same wait again. One bd
// call per event, never per tick; off the main loop. A test swaps it.
var bdSetWaitOverSent = func(ctx context.Context, dir, id, target string) error {
	if testing.Testing() {
		return errBdWriteInTest // a test that forgot the seam must not write a real bead
	}
	cmd := exec.CommandContext(ctx, "bd", waitOverSentArgs(id, target)...)
	cmd.Dir = dir
	return cmd.Run()
}

// waitOverSentArgs is the bd command that records target on id, or clears the
// record when target is "".
func waitOverSentArgs(id, target string) []string {
	if target == "" {
		return []string{"update", id, "--unset-metadata", progress.WaitOverSentKey}
	}
	return []string{"update", id, "--set-metadata", progress.WaitOverSentKey + "=" + target}
}
