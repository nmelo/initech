package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-5qf: the record never carries a bead that is not an id.

// AC3: a non-id fed to the writer is not written; the interval carries the
// derived bead (source bd), and one log line names the rejected value.
func TestSessionBeads_WriterGuardWritesTheDerivedBead(t *testing.T) {
	logs := captureLogs(t)
	r, root := sbRecorder(t)
	w := sbWork(new(int), "ini-y0n.2\tProgress step 2", "manual", sbBase)
	w.derived = "ini-d"
	r.reconcile([]paneWork{w}, sbBase.Add(time.Minute))
	r.reconcile([]paneWork{w}, sbBase.Add(2*time.Minute))
	lines := sbParsed(t, root)
	if len(lines) != 1 || sbStr(lines[0].Bead) != "ini-d" || lines[0].Source != "bd" {
		t.Fatalf("lines %+v, want one open interval on ini-d with source bd", lines)
	}
	if n := strings.Count(logs.String(), "ini-y0n.2\\tProgress step 2"); n != 1 {
		t.Errorf("the rejected value is named %d times in the log, want once:\n%s", n, logs.String())
	}

	// No derived bead either: the interval is written with no bead.
	r2, root2 := sbRecorder(t)
	r2.reconcile([]paneWork{sbWork(new(int), "per hour", "manual", sbBase)}, sbBase.Add(time.Minute))
	if l := sbParsed(t, root2); len(l) != 1 || l[0].Bead != nil {
		t.Errorf("non-id with no derived bead: %+v, want bead null", l)
	}
}

// AC4, on disk: an interval a crashed run left open with a non-id bead is
// closed by the first reconcile after the fix; an open interval on an id is
// left alone; the bytes already in the file are untouched; a second
// reconcile adds nothing more.
func TestSessionBeads_UpgradeClosesOpenNonIDIntervalsOnce(t *testing.T) {
	r, _ := sbRecorder(t)
	dir := r.dir
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "initech.owner"), []byte(r.root+"\n"), 0o600)
	now := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	before := strings.Join([]string{
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"s1","role":"eng4","bead":"ini-old","source":"bd","from":"2026-11-01T01:00:00Z","to":"2026-11-01T02:00:00Z"}`,
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"s1","role":"eng4","bead":"ini-y0n.2\tProgress step 2: a pane header shows its effort pace (closed/total","source":"manual","from":"2026-11-01T02:00:00Z","to":null}`,
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"s2","role":"qa1","bead":"ini-ok","source":"manual","from":"2026-11-01T02:30:00Z","to":null}`,
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"s3","role":"qa2","bead":"per hour","source":"manual","from":"2026-11-01T02:40:00Z","to":null}`,
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"s3","role":"qa2","bead":"per hour","source":"manual","from":"2026-11-01T02:40:00Z","to":"2026-11-01T03:00:00Z"}`,
	}, "\n") + "\n"
	path := filepath.Join(dir, "initech-2026-11.jsonl")
	os.WriteFile(path, []byte(before), 0o600)

	r.reconcile(nil, now)
	r.reconcile(nil, now.Add(time.Minute))
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), before) {
		t.Fatalf("the lines already written changed:\n%s", got)
	}
	added := strings.Split(strings.TrimSuffix(strings.TrimPrefix(string(got), before), "\n"), "\n")
	want := `{"v":1,"rig":"initech","host":"nmbp","session_id":"s1","role":"eng4","bead":"ini-y0n.2\tProgress step 2: a pane header shows its effort pace (closed/total","source":"manual","from":"2026-11-01T02:00:00Z","to":"2026-11-01T04:00:00Z"}`
	if len(added) != 1 || added[0] != want {
		t.Errorf("added %d line(s):\n%s\nwant exactly the close of the open non-id interval:\n%s", len(added), strings.Join(added, "\n"), want)
	}
}

// AC5 feed-it-nothing: a file with no open interval, and no file at all:
// the first reconcile adds nothing and does not fail.
func TestSessionBeads_UpgradeWithNothingOpenAddsNothing(t *testing.T) {
	r, root := sbRecorder(t)
	r.reconcile(nil, sbBase)
	if l := sbLines(t, root); len(l) != 0 {
		t.Errorf("no file: wrote %q", l)
	}
	r2, root2 := sbRecorder(t)
	dir := r2.dir
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "initech.owner"), []byte(r2.root+"\n"), 0o600)
	closed := `{"v":1,"rig":"initech","host":"nmbp","session_id":"s1","role":"eng4","bead":"x\ty","source":"manual","from":"2026-10-31T19:00:00Z","to":"2026-10-31T19:30:00Z"}` + "\n"
	os.WriteFile(filepath.Join(dir, "initech-2026-10.jsonl"), []byte(closed), 0o600)
	r2.reconcile(nil, sbBase)
	if l := sbLines(t, root2); len(l) != 1 {
		t.Errorf("a closed non-id interval was closed again: %q", l)
	}
}

// The guard's stand-in is the pane's own derived bead, read with the rest
// of its work.
func TestSessionBeads_GuardUsesThePanesDerivedBead(t *testing.T) {
	r, root := sbRecorder(t)
	p := sbPane("eng4", sbBase, sbBase.Add(time.Second))
	p.mu.Lock()
	p.derivedBead = "ini-d"
	p.beadIDs = []string{"per hour"}
	p.mu.Unlock()
	r.reconcile([]paneWork{p.sessionWork()}, sbBase.Add(time.Minute))
	if l := sbParsed(t, root); len(l) != 1 || sbStr(l[0].Bead) != "ini-d" || l[0].Source != "bd" {
		t.Errorf("lines %+v, want one interval on the pane's derived ini-d, source bd", l)
	}
}
