package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ini-wra.2: the session-bead record. Each test reads the files back the way
// the cost ledger does, line by line, and checks the agreed keys and values.

// sbLines reads every line of every month file in root's records folder
// (root/records, where sbRecorder records), oldest file first, as raw strings.
func sbLines(t *testing.T, root string) []string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(root, "records", "*.jsonl"))
	sort.Strings(names)
	var out []string
	for _, n := range names {
		f, err := os.Open(n)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			out = append(out, sc.Text())
		}
		f.Close()
	}
	return out
}

// sbParsed is sbLines decoded with unknown keys refused.
func sbParsed(t *testing.T, root string) []sessionBeadLine {
	t.Helper()
	var out []sessionBeadLine
	for _, l := range sbLines(t, root) {
		dec := json.NewDecoder(strings.NewReader(l))
		dec.DisallowUnknownFields()
		var sl sessionBeadLine
		if err := dec.Decode(&sl); err != nil {
			t.Fatalf("line %q does not parse as the agreed format: %v", l, err)
		}
		out = append(out, sl)
	}
	return out
}

func sbStr(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

func sbRecorder(t *testing.T) (*sessionBeadRecorder, string) {
	t.Helper()
	root := t.TempDir()
	r := newSessionBeadRecorder(filepath.Join(root, "records"), filepath.Join(root, "rig"), "initech")
	r.host = "nmbp"
	return r, root
}

var sbBase = time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)

func sbWork(key any, bead, source string, from time.Time) paneWork {
	return paneWork{key: key, role: "eng2", sessionID: "0f3c-uuid", bead: bead, source: source, from: from, working: true}
}

// The format, byte for byte: keys in the agreed order, UTC seconds, an open
// interval with "to": null, a no-bead stretch with "bead": null.
func TestSessionBeads_TheLineIsTheAgreedFormat(t *testing.T) {
	r, root := sbRecorder(t)
	k := new(int)
	r.reconcile([]paneWork{sbWork(k, "ini-cnp", "bd", sbBase.Add(12*time.Second+400*time.Millisecond))}, sbBase.Add(time.Minute))
	r.reconcile([]paneWork{sbWork(k, "", "bd", sbBase.Add(51*time.Minute+3*time.Second))}, sbBase.Add(52*time.Minute))
	got := sbLines(t, root)
	want := []string{
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"0f3c-uuid","role":"eng2","bead":"ini-cnp","source":"bd","from":"2026-10-31T20:00:12Z","to":null}`,
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"0f3c-uuid","role":"eng2","bead":"ini-cnp","source":"bd","from":"2026-10-31T20:00:12Z","to":"2026-10-31T20:51:03Z"}`,
		`{"v":1,"rig":"initech","host":"nmbp","session_id":"0f3c-uuid","role":"eng2","bead":null,"source":"bd","from":"2026-10-31T20:51:03Z","to":null}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("record:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := len(sbParsed(t, root)); n != 3 {
		t.Errorf("parsed %d lines, want 3", n)
	}
}

// sbPane is a local pane with its process started at start and a session id
// from an entry at session.
func sbPane(name string, start, session time.Time) *Pane {
	p := testPane(name)
	p.alive = true
	p.startedAt = start
	p.noteSessionID([]JournalEntry{{Type: "user", SessionID: "sess-" + name, Timestamp: session}})
	return p
}

// AC1: a claim opens an interval from the claim time with source bd, not
// from the poll; initech bead closes it and opens one with source manual; a
// clear goes back to bd from the clear. The tie between two in_progress beads
// goes to the one the pane last named.
func TestSessionBeads_AClaimThenAnOverrideThenAClear(t *testing.T) {
	r, root := sbRecorder(t)
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	p := sbPane("eng2", base, base.Add(time.Minute))
	claim := base.Add(2 * time.Minute)
	poll := base.Add(2*time.Minute + 9*time.Second)
	p.applyDerivedBeads([]bdBead{{ID: "ini-a", Assignee: "eng2", Status: "in_progress", UpdatedAt: claim}}, true, poll)
	r.reconcile(localPaneWork([]PaneView{p}), poll.Add(time.Second))

	p.SetBead("ini-b", "")
	r.reconcile(localPaneWork([]PaneView{p}), time.Now())
	p.SetBead("", "")
	cleared := time.Now()
	r.reconcile(localPaneWork([]PaneView{p}), cleared)

	l := sbParsed(t, root)
	if len(l) != 5 {
		t.Fatalf("got %d lines, want 5 (open a, close a, open b, close b, open a)", len(l))
	}
	if sbStr(l[0].Bead) != "ini-a" || l[0].Source != "bd" || l[0].From != recordTime(claim) || l[0].To != nil {
		t.Errorf("claim line %+v: want ini-a, bd, from the claim %s, open", l[0], recordTime(claim))
	}
	if sbStr(l[2].Bead) != "ini-b" || l[2].Source != "manual" || l[2].To != nil {
		t.Errorf("override line %+v: want ini-b, manual, open", l[2])
	}
	if l[1].From != l[0].From || l[1].To == nil || *l[1].To != l[2].From {
		t.Errorf("the claim interval closes where the override opens: close %+v, open %+v", l[1], l[2])
	}
	if sbStr(l[4].Bead) != "ini-a" || l[4].Source != "bd" || l[3].To == nil || l[4].From != *l[3].To {
		t.Errorf("after the clear %+v: want ini-a from bd again, opening where the override closed (%s)", l[4], sbStr(l[3].To))
	}
	if l[4].From <= l[0].From || l[4].From > recordTime(cleared) {
		t.Errorf("the interval after the clear starts at %s; want the clear (by %s), not the claim %s", l[4].From, recordTime(cleared), l[0].From)
	}

	// Two in_progress beads: the one the pane last named wins (ini-b).
	p.applyDerivedBeads([]bdBead{
		{ID: "ini-a", Assignee: "eng2", Status: "in_progress", UpdatedAt: claim.Add(time.Minute)},
		{ID: "ini-b", Assignee: "eng2", Status: "in_progress", UpdatedAt: claim},
	}, true, time.Now())
	if w := p.sessionWork(); w.bead != "ini-b" {
		t.Errorf("two in_progress beads: the record reads %q, want the last named ini-b", w.bead)
	}
}

// AC2: a clean shutdown closes every open interval with the same session,
// role, bead and from; a second recorder on the same folder (a restart after
// a crash) only appends.
func TestSessionBeads_ShutdownClosesAndARestartNeverRewrites(t *testing.T) {
	r, root := sbRecorder(t)
	a, b := new(int), new(int)
	wb := sbWork(b, "", "bd", sbBase)
	wb.role, wb.sessionID = "eng5", "other-uuid"
	r.reconcile([]paneWork{sbWork(a, "ini-x", "manual", sbBase), wb}, sbBase.Add(time.Minute))
	r.closeAll(sbBase.Add(5 * time.Minute))
	l := sbParsed(t, root)
	if len(l) != 4 {
		t.Fatalf("got %d lines, want 2 opens and 2 closes", len(l))
	}
	closed := 0
	for _, c := range l[2:] {
		for _, o := range l[:2] {
			if c.SessionID == o.SessionID && c.Role == o.Role && sbStr(c.Bead) == sbStr(o.Bead) && c.From == o.From && sbStr(c.To) == "2026-10-31T20:05:00Z" {
				closed++
			}
		}
	}
	if closed != 2 {
		t.Errorf("shutdown closed %d of 2 intervals with matching keys: %+v", closed, l)
	}

	// A crash leaves an interval open; the next start appends and never
	// rewrites what is there.
	r2, _ := sbRecorder(t)
	r2.dir, r2.root = r.dir, r.root
	r.reconcile([]paneWork{sbWork(a, "ini-y", "bd", sbBase.Add(6*time.Minute))}, sbBase.Add(7*time.Minute))
	before, _ := os.ReadFile(filepath.Join(r.dir, "initech-2026-10.jsonl"))
	r2.reconcile([]paneWork{sbWork(a, "ini-y", "bd", sbBase.Add(8*time.Minute))}, sbBase.Add(9*time.Minute))
	after, _ := os.ReadFile(filepath.Join(r.dir, "initech-2026-10.jsonl"))
	if !bytes.HasPrefix(after, before) || len(after) <= len(before) {
		t.Error("a restart rewrote the earlier lines instead of appending")
	}
}

// AC3: suspend closes the interval and wake opens a new one with the same
// bead from the wake; a new session id closes and reopens at the new
// session's first entry; a pane that is gone closes.
func TestSessionBeads_SuspendWakeAndANewSession(t *testing.T) {
	r, root := sbRecorder(t)
	k := new(int)
	w := sbWork(k, "ini-x", "bd", sbBase)
	r.reconcile([]paneWork{w}, sbBase.Add(time.Minute))
	w.working = false
	r.reconcile([]paneWork{w}, sbBase.Add(10*time.Minute))
	w.working, w.from = true, sbBase.Add(20*time.Minute)
	r.reconcile([]paneWork{w}, sbBase.Add(20*time.Minute+time.Second))
	w.sessionID, w.from = "new-uuid", sbBase.Add(30*time.Minute)
	r.reconcile([]paneWork{w}, sbBase.Add(30*time.Minute+500*time.Millisecond))
	w.role = "eng9"
	r.reconcile([]paneWork{w}, sbBase.Add(35*time.Minute))
	r.reconcile(nil, sbBase.Add(40*time.Minute))

	got := []string{}
	for _, l := range sbParsed(t, root) {
		got = append(got, l.SessionID+" "+l.Role+" "+sbStr(l.Bead)+" "+l.From+" "+sbStr(l.To))
	}
	want := []string{
		"0f3c-uuid eng2 ini-x 2026-10-31T20:00:00Z null",
		"0f3c-uuid eng2 ini-x 2026-10-31T20:00:00Z 2026-10-31T20:10:00Z",
		"0f3c-uuid eng2 ini-x 2026-10-31T20:20:00Z null",
		"0f3c-uuid eng2 ini-x 2026-10-31T20:20:00Z 2026-10-31T20:30:00Z",
		"new-uuid eng2 ini-x 2026-10-31T20:30:00Z null",
		"new-uuid eng2 ini-x 2026-10-31T20:30:00Z 2026-10-31T20:35:00Z",
		"new-uuid eng9 ini-x 2026-10-31T20:35:00Z null",
		"new-uuid eng9 ini-x 2026-10-31T20:35:00Z 2026-10-31T20:40:00Z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("record:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A real pane: suspend and wake through its own fields, and the session id
// only from an entry written since the process started.
func TestSessionBeads_APaneTakesItsSessionFromItsOwnEntries(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	p := testPane("eng2")
	p.alive, p.startedAt = true, start
	p.noteSessionID([]JournalEntry{{SessionID: "old-run", Timestamp: start.Add(-time.Minute)}})
	if w := p.sessionWork(); w.sessionID != "" {
		t.Fatalf("an entry from before the start named the pane: %q", w.sessionID)
	}
	p.noteSessionID([]JournalEntry{{SessionID: "this-run", Timestamp: start.Add(time.Minute)}, {SessionID: "this-run", Timestamp: start.Add(2 * time.Minute)}})
	w := p.sessionWork()
	if w.sessionID != "this-run" || !w.from.Equal(start.Add(time.Minute)) {
		t.Errorf("session %q from %v, want this-run from its first entry", w.sessionID, w.from)
	}
	p.suspended = true
	if p.sessionWork().working {
		t.Error("a suspended pane reads as working")
	}
	// Wake: the process restarts and --continue resumes the same session id,
	// so no new first entry; the interval starts at the wake.
	wake := start.Add(30 * time.Minute)
	p.suspended, p.startedAt = false, wake
	p.noteSessionID([]JournalEntry{{SessionID: "this-run", Timestamp: wake.Add(time.Minute)}})
	if w := p.sessionWork(); !w.working || w.sessionID != "this-run" || !w.from.Equal(wake) {
		t.Errorf("after the wake: working %v, session %q, from %v; want this-run from the wake %v", w.working, w.sessionID, w.from, wake)
	}
	if e := parseJSONLEntry(`{"type":"user","sessionId":"abc-123","timestamp":"2026-10-31T20:00:00Z"}`); e.SessionID != "abc-123" {
		t.Errorf("the transcript's sessionId was not read: %q", e.SessionID)
	}
}

// AC4: a line written on the first of the month goes to the new month's file;
// only month files older than 90 days are removed, and only by name.
func TestSessionBeads_MonthlyFilesAndTheNinetyDayKeep(t *testing.T) {
	r, root := sbRecorder(t)
	dir := filepath.Join(root, "records")
	os.MkdirAll(dir, 0o700)
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	keep := []string{
		"initech-2026-08.jsonl", // ended Sep 1: 61 days
		"initech-2026-07.jsonl", // ended Aug 1: exactly 92 days... see below
		"initech-notes.jsonl", "initech-2026-07.jsonl.bak", "otherrig-2025-12.jsonl",
	}
	for _, n := range append(keep, "initech-2026-06.jsonl", "initech-2025-12.jsonl") {
		os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o600)
	}
	k := new(int)
	r.reconcile([]paneWork{sbWork(k, "ini-x", "bd", nov1.Add(-time.Hour))}, nov1.Add(-time.Second))
	// July ended 93 days before this write (Aug 1 to Nov 1 less a second is
	// 91 days 23:59:59): it goes once the month turns.
	r.reconcile([]paneWork{sbWork(k, "ini-y", "bd", nov1)}, nov1)

	oct, nov := filepath.Join(dir, "initech-2026-10.jsonl"), filepath.Join(dir, "initech-2026-11.jsonl")
	if b, _ := os.ReadFile(oct); strings.Count(string(b), "\n") != 1 {
		t.Errorf("October holds %q, want only the line written on Oct 31", b)
	}
	if b, _ := os.ReadFile(nov); strings.Count(string(b), "\n") != 2 {
		t.Errorf("November holds %q, want the close and the open written on Nov 1", b)
	}
	for _, n := range []string{"initech-2026-06.jsonl", "initech-2025-12.jsonl", "initech-2026-07.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s ended more than 90 days ago and is still there", n)
		}
	}
	for _, n := range []string{"initech-2026-08.jsonl", "initech-notes.jsonl", "initech-2026-07.jsonl.bak", "otherrig-2025-12.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s was removed; only month files older than 90 days may go", n)
		}
	}
	// The edge: a month that ended 90 days ago stays, one that ended 91
	// days ago goes.
	edge := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Add(90 * 24 * time.Hour)
	os.WriteFile(filepath.Join(dir, "initech-2026-07.jsonl"), []byte("x\n"), 0o600)
	r.prune(edge)
	if _, err := os.Stat(filepath.Join(dir, "initech-2026-07.jsonl")); err != nil {
		t.Error("a month that ended exactly 90 days ago was removed")
	}
	r.prune(edge.Add(24 * time.Hour))
	if _, err := os.Stat(filepath.Join(dir, "initech-2026-07.jsonl")); err == nil {
		t.Error("a month that ended 91 days ago was kept")
	}
}

// AC5 feed it nothing: a session with no bead is one interval with bead null;
// a pane with no session id writes nothing; an unwritable folder logs once
// and nothing else breaks.
func TestSessionBeads_NothingToRecord(t *testing.T) {
	r, root := sbRecorder(t)
	k := new(int)
	r.reconcile([]paneWork{sbWork(k, "", "bd", sbBase)}, sbBase.Add(time.Minute))
	r.reconcile([]paneWork{sbWork(k, "", "bd", sbBase)}, sbBase.Add(2*time.Minute))
	l := sbParsed(t, root)
	if len(l) != 1 || l[0].Bead != nil {
		t.Errorf("a session with no bead wrote %+v, want one open interval with bead null", l)
	}

	r2, root2 := sbRecorder(t)
	w := sbWork(k, "ini-x", "bd", sbBase)
	w.sessionID = ""
	r2.reconcile([]paneWork{w}, sbBase.Add(time.Minute))
	if _, err := os.Stat(filepath.Join(root2, "records")); err == nil {
		t.Error("a pane with no session id yet wrote a record")
	}
	r2.reconcile(nil, sbBase.Add(time.Minute))

	var buf bytes.Buffer
	appLogger.mu.Lock()
	prev := appLogger.logger
	appLogger.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	appLogger.mu.Unlock()
	t.Cleanup(func() { appLogger.mu.Lock(); appLogger.logger = prev; appLogger.mu.Unlock() })
	r3, root3 := sbRecorder(t)
	os.WriteFile(filepath.Join(root3, "records"), []byte("a file where the folder should be"), 0o600)
	for i := 0; i < 5; i++ {
		ww := sbWork(k, "ini-"+string(rune('a'+i)), "bd", sbBase.Add(time.Duration(i)*time.Minute))
		r3.reconcile([]paneWork{ww}, sbBase.Add(time.Duration(i)*time.Minute+time.Second))
	}
	r3.closeAll(sbBase.Add(time.Hour))
	if n := strings.Count(buf.String(), "cannot write the session-bead record"); n != 1 {
		t.Errorf("an unwritable folder logged %d times, want once", n)
	}
	if !strings.Contains(buf.String(), "is not a folder") {
		t.Errorf("a file where the records folder should be: the warning does not say so:\n%s", buf.String())
	}
}

// bd unreachable: no new interval from a stale derived bead; a stop still
// closes.
func TestSessionBeads_AStaleBeadWritesNothingNew(t *testing.T) {
	r, root := sbRecorder(t)
	k := new(int)
	w := sbWork(k, "ini-x", "bd", sbBase)
	r.reconcile([]paneWork{w}, sbBase.Add(time.Minute))
	w.frozen, w.bead = true, "ini-y"
	r.reconcile([]paneWork{w}, sbBase.Add(2*time.Minute))
	if n := len(sbParsed(t, root)); n != 1 {
		t.Fatalf("a stale bead wrote %d lines, want the first only", n)
	}
	w.working = false
	r.reconcile([]paneWork{w}, sbBase.Add(3*time.Minute))
	w.working = true
	r.reconcile([]paneWork{w}, sbBase.Add(4*time.Minute))
	if l := sbParsed(t, root); len(l) != 2 || l[1].To == nil {
		t.Errorf("stale: a stop must close and nothing reopen until bd answers: %+v", l)
	}
	if p := testPane("eng2"); true {
		p.alive, p.beadStale = true, true
		if !p.sessionWork().frozen {
			t.Error("a stale derived bead is not frozen")
		}
		p.SetBead("ini-z", "")
		if p.sessionWork().frozen {
			t.Error("an override is frozen by a stale bd; it does not come from bd")
		}
	}
}

// Only window 1 records: a second window or a viewer gets no recorder, and a
// window 2 pane is never in the record.
func TestSessionBeads_OnlyWindowOneRecords(t *testing.T) {
	records := recordsHome(t)
	tui, _ := newTestTUIWithScreen("eng2")
	tui.projectRoot, tui.projectName = t.TempDir(), "initech"
	tui.windowID = WindowOne
	if tui.sessionBeadRecorderFor(false) == nil {
		t.Fatal("window 1 has no recorder")
	}
	if tui.sessionBeadRecorderFor(true) != nil {
		t.Error("a viewer records")
	}
	tui.windowID = "w2"
	if tui.sessionBeadRecorderFor(false) != nil {
		t.Error("a second window records")
	}
	if works := localPaneWork([]PaneView{&RemotePane{name: "eng1", host: "w2"}}); len(works) != 0 {
		t.Errorf("a window 2 pane is in the record: %+v", works)
	}
	// Reachability: the main loop's tick reconciles window 1's panes.
	tui.windowID = WindowOne
	tui.sessionBeads = tui.sessionBeadRecorderFor(false)
	p := tui.panes[0].(*Pane)
	p.alive, p.startedAt = true, time.Now().Add(-time.Minute)
	p.noteSessionID([]JournalEntry{{SessionID: "s-1", Timestamp: time.Now().Add(-30 * time.Second)}})
	tui.recordSessionBeads(time.Now())
	if l := sbParsed(t, filepath.Dir(records)); len(l) != 1 || l[0].Role != "eng2" || l[0].SessionID != "s-1" {
		t.Errorf("the tick wrote %+v, want eng2's open interval", l)
	}
}

// AC3 through the real wake: resumePane replaces the pane, and the new one
// carries the session id, so its interval reopens from the wake with the same
// bead without waiting for the agent's next entry.
func TestSessionBeads_AWokenPaneReopensAtTheWake(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.startedAt = time.Now().Add(-time.Hour)
	p.noteSessionID([]JournalEntry{{SessionID: "resumed-uuid", Timestamp: time.Now().Add(-50 * time.Minute)}})
	p.SetBead("ini-x", "")
	p.suspended = true
	tu := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}
	before := time.Now()
	if err := tu.resumePane(p, "test"); err != nil {
		t.Fatalf("resumePane: %v", err)
	}
	np := tu.panes[0].(*Pane)
	t.Cleanup(func() { np.Close() })
	if np == p {
		t.Fatal("resume did not replace the pane; the carry was not exercised")
	}
	w := np.sessionWork()
	if !w.working || w.sessionID != "resumed-uuid" || w.bead != "ini-x" || w.source != "manual" {
		t.Errorf("woken pane: working %v session %q bead %q source %q; want resumed-uuid on ini-x (manual)", w.working, w.sessionID, w.bead, w.source)
	}
	if w.from.Before(before) {
		t.Errorf("the woken interval starts %v, before the wake %v", w.from, before)
	}
}
