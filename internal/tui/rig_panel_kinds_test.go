package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/beadkind"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/progress"
)

// ini-u2xf hookup: the moved box's kinds come from the beadkind reader, a
// commit from the crossing read, a comment from the poll's count; the newest
// wins, and with none the box keeps the status kind.

var k0 = time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC)

// kindsBD is a journal-on bd with the given rows.
type kindsBD struct {
	mu   sync.Mutex
	rows []string
	n    int
}

func (f *kindsBD) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	all := strings.Join(args, " ")
	switch {
	case strings.Contains(all, "config get events-journal"):
		return []byte("true\n"), nil
	case strings.Contains(all, "events tail"):
		var since int
		for i, a := range args {
			if a == "--since" {
				since = atoiOr(args[i+1])
			}
		}
		if since >= len(f.rows) {
			return nil, nil
		}
		return []byte(strings.Join(f.rows[since:], "\n") + "\n"), nil
	}
	return []byte("[]"), nil
}

func atoiOr(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func kindsRow(seq int, op, id, status, assignee string, at time.Time) string {
	b, _ := json.Marshal(map[string]any{"seq": seq, "ts": at.Format(time.RFC3339), "op": op, "issue_id": id,
		"issue": map[string]any{"id": id, "status": status, "assignee": assignee, "updated_at": at.Format(time.RFC3339)}})
	return string(b)
}

// kindsTUI is window 1 with the reader on f, its poll run inline.
func kindsTUI(t *testing.T, f *kindsBD) *TUI {
	t.Helper()
	orig := kindsRunner
	t.Cleanup(func() { kindsRunner = orig })
	kindsRunner = func(string) beadkind.Runner { return f.run }
	tui := newTestTUI()
	tui.startPanelKinds()
	tui.kinds.spawn = func(fn func()) { fn() }
	return tui
}

// The poll hook: an answered bead poll reaches the reader, and the box's
// source then answers with the journal's kind.
func TestPanelKinds_ABeadPollFeedsTheReader(t *testing.T) {
	f := &kindsBD{rows: []string{kindsRow(1, "create", "ini-a", "open", "", k0), kindsRow(2, "update", "ini-a", "in_progress", "eng5", k0.Add(time.Minute))}}
	tui := kindsTUI(t, f)
	if tui.panelKinds == nil {
		t.Fatal("startPanelKinds left the box without a kind source")
	}
	tui.pollKinds([]bdBead{{ID: "ini-a", Status: "in_progress", Assignee: "eng5", UpdatedAt: k0.Add(time.Minute)}}, k0.Add(2*time.Minute))
	if k, ok := tui.panelKinds.Kind("ini-a"); !ok || k != "claimed" {
		t.Fatalf("kind %q %v, want claimed", k, ok)
	}
}

// The real poll path: pollBeadsOnce hands its answer to the reader.
func TestPanelKinds_PollBeadsOnceReachesTheReader(t *testing.T) {
	f := &kindsBD{rows: []string{kindsRow(1, "comment", "ini-b", "open", "", k0)}}
	tui := kindsTUI(t, f)
	tui.projectRoot = t.TempDir()
	origList, origCustom := bdListBeads, bdStatusCustom
	t.Cleanup(func() { bdListBeads, bdStatusCustom = origList, origCustom })
	bdListBeads = func(context.Context, string) ([]bdBead, error) {
		return []bdBead{{ID: "ini-b", Status: "open", UpdatedAt: k0}}, nil
	}
	bdStatusCustom = func(context.Context, string) (string, error) { return "", nil }
	tui.pollBeadsOnce(k0.Add(time.Minute))
	if k, ok := tui.panelKinds.Kind("ini-b"); !ok || k != "comment" {
		t.Fatalf("kind %q %v, want comment through the real poll", k, ok)
	}
}

// One reader poll at a time: a poll that arrives while one runs is skipped.
func TestPanelKinds_OnePollAtATime(t *testing.T) {
	f := &kindsBD{}
	tui := kindsTUI(t, f)
	var held func()
	spawned := 0
	tui.kinds.spawn = func(fn func()) { spawned++; held = fn }
	tui.pollKinds(nil, k0)
	tui.pollKinds(nil, k0)
	if spawned != 1 || held == nil {
		t.Fatalf("%d polls started while one ran, want 1", spawned)
	}
	before := f.n
	held()
	if f.n == before {
		t.Fatal("the held poll did not run")
	}
	runs := 0
	tui.kinds.spawn = func(fn func()) { runs++; fn() }
	tui.pollKinds(nil, k0)
	if runs != 1 {
		t.Fatalf("after the first finished, a new poll ran %d times, want 1", runs)
	}
}

// Newest wins: a commit the crossing read found after the reader's kind
// reads commit; a comment the poll saw after it reads comment; an older
// commit leaves the reader's kind.
func TestPanelKinds_TheNewestSourceWins(t *testing.T) {
	f := &kindsBD{rows: []string{kindsRow(1, "create", "ini-c", "open", "", k0), kindsRow(2, "update", "ini-c", "in_qa", "qa2", k0.Add(10*time.Minute))}}
	tui := kindsTUI(t, f)
	tui.pollKinds([]bdBead{{ID: "ini-c", Status: "in_qa", Assignee: "qa2", UpdatedAt: k0.Add(10 * time.Minute)}}, k0.Add(11*time.Minute))
	tui.paceReads = map[string]paceRead{"ini-c": {at: k0.Add(5 * time.Minute), by: "commit"}}
	if k, _ := tui.panelKinds.Kind("ini-c"); k != "qa" {
		t.Fatalf("an older commit overrode qa: %q", k)
	}
	tui.paceReads["ini-c"] = paceRead{at: k0.Add(15 * time.Minute), by: "commit"}
	if k, _ := tui.panelKinds.Kind("ini-c"); k != "commit" {
		t.Fatalf("a newer commit read %q, want commit", k)
	}
	tui.paceReads["ini-c"] = paceRead{at: k0.Add(15 * time.Minute), by: "eng5"} // a comment's author, not a commit
	if k, _ := tui.panelKinds.Kind("ini-c"); k != "qa" {
		t.Fatalf("a non-commit read set %q", k)
	}
	tui.paceCommentAt = map[string]time.Time{"ini-c": k0.Add(20 * time.Minute)}
	if k, _ := tui.panelKinds.Kind("ini-c"); k != "comment" {
		t.Fatalf("a newer comment read %q, want comment", k)
	}
}

// With no source the box keeps the status kind, as before the reader.
func TestPanelKinds_NoSourceKeepsTheStatusKind(t *testing.T) {
	tui := kindsTUI(t, &kindsBD{})
	if k, ok := tui.panelKinds.Kind("ini-z"); ok || k != "" {
		t.Fatalf("kind %q %v from nothing", k, ok)
	}
	tui.paceAnswered = true
	tui.paceBeads = []bdBead{{ID: "ini-z", Status: "waiting", UpdatedAt: k0}}
	snap := tui.movedSnapshot(k0.Add(time.Minute))
	if len(snap) != 1 || snap[0].Kind != "waiting" {
		t.Fatalf("snapshot %+v, want the status kind waiting", snap)
	}
}

// The crossing read keeps who made the last move, so a commit is known.
func TestPaceRead_KeepsACommitAsTheMover(t *testing.T) {
	tui := newTestTUI()
	tui.projectRoot = t.TempDir()
	origC, origG := bdBeadComments, gitBeadCommits
	t.Cleanup(func() { bdBeadComments, gitBeadCommits = origC, origG })
	bdBeadComments = func(context.Context, string, string) ([]progress.Comment, error) { return nil, nil }
	gitBeadCommits = func(context.Context, string, string) ([]progress.Commit, error) {
		return []progress.Commit{{When: k0.Add(time.Hour), Subject: "fix: thing (ini-m)"}}, nil
	}
	root := tui.projectRoot
	if err := os.MkdirAll(filepath.Join(root, "eng1", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	tui.project = &config.Project{Name: "demo", Root: root, Roles: []string{"eng1"},
		Repos: []config.Repo{{URL: "https://github.com/acme/product", Name: "src"}}}
	tui.readPaceCrossings([]paceCandidate{{bead: progress.Bead{ID: "ini-m", UpdatedAt: k0}, known: k0}})
	if r := tui.paceReads["ini-m"]; r.by != "commit" || !r.at.Equal(k0.Add(time.Hour)) {
		t.Fatalf("paceRead = %+v, want a commit at %v", r, k0.Add(time.Hour))
	}
}

// The reader starts with window 1's bead poller, so the box has its source.
func TestPanelKinds_TheBeadPollerStartsTheReader(t *testing.T) {
	f := &kindsBD{}
	orig := kindsRunner
	t.Cleanup(func() { kindsRunner = orig })
	kindsRunner = func(string) beadkind.Runner { return f.run }
	origList, origCustom, origHas := bdListBeads, bdStatusCustom, projectHasBeads
	t.Cleanup(func() { bdListBeads, bdStatusCustom, projectHasBeads = origList, origCustom, origHas })
	bdListBeads = func(context.Context, string) ([]bdBead, error) { return nil, nil }
	bdStatusCustom = func(context.Context, string) (string, error) { return "", nil }
	// The real check also needs bd on PATH, which a CI runner lacks.
	projectHasBeads = func(string) bool { return true }
	tui := newTestTUI()
	tui.windowID = WindowOne
	tui.projectRoot = t.TempDir()
	// The poller's first poll runs on its own goroutine and reads the stubs
	// above; the cleanups restore them. pollKinds is that poll's last step,
	// so its spawn is the event that says the goroutine is done with them.
	polled := make(chan struct{})
	tui.kinds.spawn = func(fn func()) { fn(); close(polled) }
	quit := make(chan struct{})
	close(quit)
	tui.startBeadPoller(quit)
	select {
	case <-polled:
	case <-time.After(10 * time.Second): // a safety bound only; the event ends the wait
		t.Fatal("the poller's first poll never reached the reader")
	}
	if tui.panelKinds == nil || tui.kinds.reader == nil {
		t.Fatal("the bead poller started without the change-kind reader")
	}
}

// Plan B through the hook: with the journal off, the reader reads the bead's
// own audit events (bd history --events), and the box gets that kind.
func TestPanelKinds_PlanBReadsTheBeadsOwnEvents(t *testing.T) {
	var calls []string
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		all := strings.Join(args, " ")
		calls = append(calls, all)
		switch {
		case strings.Contains(all, "config get events-journal"):
			return []byte("false\n"), nil
		case strings.Contains(all, "history ini-n --events"):
			return []byte(`[{"event_type":"status_changed","old_value":"{\"status\":\"in_progress\",\"assignee\":\"eng1\"}","new_value":"{\"status\":\"waiting\"}","created_at":"` + k0.Format(time.RFC3339) + `"},{"event_type":"created","created_at":"` + k0.Add(-time.Hour).Format(time.RFC3339) + `"}]`), nil
		}
		return nil, nil
	}
	orig := kindsRunner
	t.Cleanup(func() { kindsRunner = orig })
	kindsRunner = func(string) beadkind.Runner { return run }
	tui := newTestTUI()
	tui.startPanelKinds()
	tui.kinds.spawn = func(fn func()) { fn() }
	tui.pollKinds([]bdBead{{ID: "ini-n", Status: "waiting", Assignee: "eng1", UpdatedAt: k0}}, k0.Add(time.Minute))
	if k, ok := tui.panelKinds.Kind("ini-n"); !ok || k != "waiting" {
		t.Fatalf("kind %q %v via calls %q, want waiting from the bead's events", k, ok, calls)
	}
}

// pm's ruling in the box: a QA FAIL with no assignee reads blank. The reader
// knows the newest change maps to no kind, so the box does not fall back to
// the status kind (which would say claimed for in_progress).
func TestPanelKinds_ABlankKindStaysBlankInTheBox(t *testing.T) {
	f := &kindsBD{rows: []string{
		kindsRow(1, "create", "ini-f", "in_progress", "eng5", k0),
		kindsRow(2, "update", "ini-f", "in_qa", "qa2", k0.Add(time.Minute)),
		kindsRow(3, "update", "ini-f", "in_progress", "", k0.Add(2*time.Minute)),
	}}
	tui := kindsTUI(t, f)
	bead := bdBead{ID: "ini-f", Status: "in_progress", UpdatedAt: k0.Add(2 * time.Minute)}
	tui.pollKinds([]bdBead{bead}, k0.Add(3*time.Minute))
	tui.paceAnswered, tui.paceBeads = true, []bdBead{bead}
	snap := tui.movedSnapshot(k0.Add(4 * time.Minute))
	if len(snap) != 1 || snap[0].Kind != "" {
		t.Fatalf("snapshot %+v, want a blank kind (not qa, not the status's claimed)", snap)
	}
	// A comment after it still shows.
	tui.paceCommentAt = map[string]time.Time{"ini-f": k0.Add(3 * time.Minute)}
	if snap := tui.movedSnapshot(k0.Add(4 * time.Minute)); snap[0].Kind != "comment" {
		t.Fatalf("a newer comment: %q", snap[0].Kind)
	}
}
