package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-5jh: the session-bead record is written where the cla ledger reads it,
// ~/.claude-accounts/records/<rig>-YYYY-MM.jsonl, and nowhere else.

// recordsHome points HOME at a fresh folder and returns the records path
// under it.
func recordsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".claude-accounts", "records")
}

// AC1: window 1 of a rig named initech writes its record to
// ~/.claude-accounts/records/initech-YYYY-MM.jsonl in the agreed shape, and
// nothing appears under .initech. Red at base: the reverse.
func TestSessionRecords_WrittenToTheClaudeAccountsFolderOnly(t *testing.T) {
	records := recordsHome(t)
	root := t.TempDir()
	tu := newTestTUI(testPane("eng2"))
	tu.projectRoot, tu.projectName, tu.version = root, "initech", "2.49.0"
	r := tu.sessionBeadRecorderFor(false)
	if r == nil {
		t.Fatal("window 1 has no recorder")
	}
	r.host = "nmbp"
	r.reconcile([]paneWork{sbWork(new(int), "ini-cnp", "bd", sbBase)}, sbBase.Add(time.Minute))

	b, err := os.ReadFile(filepath.Join(records, "initech-2026-10.jsonl"))
	want := `{"v":1,"rig":"initech","host":"nmbp","session_id":"0f3c-uuid","role":"eng2","bead":"ini-cnp","source":"bd","from":"2026-10-31T20:00:00Z","to":null}` + "\n"
	if err != nil || string(b) != want {
		t.Errorf("records/initech-2026-10.jsonl = %q (%v), want the one agreed line", b, err)
	}
	if names, _ := filepath.Glob(filepath.Join(root, ".initech", "*")); len(names) != 0 {
		t.Errorf("the rig's .initech got %v; the record is written only to the records folder", names)
	}
}

// recorderIn is a recorder for the rig at root, named rig, writing into dir.
func recorderIn(dir, root, rig string) *sessionBeadRecorder {
	r := newSessionBeadRecorder(dir, root, rig)
	r.host = "nmbp"
	return r
}

// AC2: two rig roots with one name on one machine. The first owns the name
// (<rig>.owner holds its root); the second writes the suffixed file, logs one
// line, and leaves the first's file as it was. The rig field is the check in
// both.
func TestSessionRecords_TwoRigsWithOneNameDoNotShareAFile(t *testing.T) {
	logs := captureLogs(t)
	dir := filepath.Join(t.TempDir(), "records")
	rootA, rootB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	a, b := recorderIn(dir, rootA, "initech"), recorderIn(dir, rootB, "initech")
	a.reconcile([]paneWork{sbWork(new(int), "ini-a", "bd", sbBase)}, sbBase.Add(time.Minute))
	first, _ := os.ReadFile(filepath.Join(dir, "initech-2026-10.jsonl"))
	b.reconcile([]paneWork{sbWork(new(int), "ini-b", "bd", sbBase)}, sbBase.Add(time.Minute))
	b.reconcile([]paneWork{sbWork(new(int), "ini-c", "bd", sbBase)}, sbBase.Add(2*time.Minute))

	if after, _ := os.ReadFile(filepath.Join(dir, "initech-2026-10.jsonl")); string(after) != string(first) {
		t.Errorf("the first rig's file changed:\n%s\nwas:\n%s", after, first)
	}
	suffixed := filepath.Join(dir, "initech-"+rootSuffix(rootB)+"-2026-10.jsonl")
	got, err := os.ReadFile(suffixed)
	if err != nil || !strings.Contains(string(got), `"rig":"initech"`) || !strings.Contains(string(got), `"bead":"ini-b"`) {
		t.Errorf("the second rig's file %s = %q (%v), want its lines with rig initech", filepath.Base(suffixed), got, err)
	}
	if owner, _ := os.ReadFile(filepath.Join(dir, "initech.owner")); strings.TrimSpace(string(owner)) != rootA {
		t.Errorf("owner marker %q, want the first rig's root %q", owner, rootA)
	}
	if n := strings.Count(logs.String(), "another rig root owns this rig name"); n != 1 {
		t.Errorf("the clash was logged %d times, want once", n)
	}
}

// AC3: a missing folder is created 0700 and written.
func TestSessionRecords_AMissingFolderIsCreatedPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude-accounts", "records")
	r := recorderIn(dir, t.TempDir(), "initech")
	r.reconcile([]paneWork{sbWork(new(int), "ini-a", "bd", sbBase)}, sbBase.Add(time.Minute))
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("records folder %v (%v), want created with 0700", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "initech-2026-10.jsonl")); !strings.Contains(string(b), "ini-a") {
		t.Errorf("nothing written to the created folder: %q", b)
	}
}

// AC3: an unwritable folder (0500): no record, one warning, at most one probe
// a minute however often the main loop reconciles (no blocking call); then,
// writable again, the next probe writes the held lines with their original
// from.
func TestSessionRecords_AnUnwritableFolderWarnsOnceRetriesAndCatchesUp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through 0500")
	}
	logs := captureLogs(t)
	dir := filepath.Join(t.TempDir(), "records")
	os.MkdirAll(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	r := recorderIn(dir, t.TempDir(), "initech")
	k := new(int)
	w := sbWork(k, "ini-a", "bd", sbBase.Add(5*time.Second))
	start := time.Now()
	for i := 0; i < 600; i++ { // ten minutes of once-a-second ticks
		r.reconcile([]paneWork{w}, sbBase.Add(time.Duration(i)*time.Second+10*time.Second))
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("600 reconciles against an unwritable folder took %v", took)
	}
	if r.probes > 11 {
		t.Errorf("%d probes of the folder in ten minutes, want at most one a minute", r.probes)
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 0 {
		t.Errorf("an unwritable folder got %v", names)
	}
	if n := strings.Count(logs.String(), "cannot write the session-bead record"); n != 1 {
		t.Errorf("the failure was logged %d times, want once", n)
	}

	os.Chmod(dir, 0o700)
	later := sbBase.Add(11 * time.Minute)
	r.reconcile([]paneWork{w}, later)
	b, err := os.ReadFile(filepath.Join(dir, "initech-2026-10.jsonl"))
	if err != nil || !strings.Contains(string(b), `"bead":"ini-a"`) || !strings.Contains(string(b), `"from":"2026-10-31T20:00:05Z"`) {
		t.Errorf("after the folder became writable: %q (%v), want the held open line with its original from", b, err)
	}
}

// AC4: the 90-day keep removes only this rig's own month files; other rigs'
// files, the owner markers and anything else are never touched.
func TestSessionRecords_TheKeepRemovesOnlyThisRigsOwnFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "records")
	os.MkdirAll(dir, 0o700)
	r := recorderIn(dir, t.TempDir(), "initech")
	r.reconcile([]paneWork{sbWork(new(int), "ini-a", "bd", sbBase)}, sbBase.Add(time.Minute))
	for _, n := range []string{"initech-2025-12.jsonl", "otherrig-2025-12.jsonl", "initech-a1b2c3-2025-12.jsonl", "other.owner", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o600)
	}
	r.prune(sbBase)
	if _, err := os.Stat(filepath.Join(dir, "initech-2025-12.jsonl")); err == nil {
		t.Error("this rig's file older than 90 days was kept")
	}
	for _, n := range []string{"otherrig-2025-12.jsonl", "initech-a1b2c3-2025-12.jsonl", "other.owner", "notes.txt", "initech.owner", "initech-2026-10.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s was removed; only this rig's own old month files may go", n)
		}
	}
}

// AC5 feed-it-nothing: a rig with no name records under its root folder's
// name; an empty records folder takes the first line; a recorder with nothing
// to write leaves the folder untouched.
func TestSessionRecords_NoNameEmptyFolderAndNothingToWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "records")
	os.MkdirAll(dir, 0o700)
	root := filepath.Join(t.TempDir(), "my rig")
	idle := recorderIn(dir, root, "")
	idle.reconcile(nil, sbBase)
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 0 {
		t.Errorf("a recorder with nothing to write touched the folder: %v", names)
	}
	r := recorderIn(dir, root, "")
	r.reconcile([]paneWork{sbWork(new(int), "ini-a", "bd", sbBase)}, sbBase.Add(time.Minute))
	b, err := os.ReadFile(filepath.Join(dir, "my-rig-2026-10.jsonl"))
	if err != nil || !strings.Contains(string(b), `"rig":"my rig"`) {
		t.Errorf("no-name rig: %q (%v), want my-rig-2026-10.jsonl with rig \"my rig\"", b, err)
	}
}

// AC3: the start check says one line when the folder cannot be written, and
// nothing when it can (a missing folder is made, as the writer would).
func TestSessionRecords_StartWarningOnlyWhenTheFolderCannotBeWritten(t *testing.T) {
	records := recordsHome(t)
	if l := SessionRecordStartLines("2.49.0", t.TempDir()); len(l) != 0 {
		t.Errorf("a missing folder: lines %q, want none (it is created)", l)
	}
	if st, err := os.Stat(records); err != nil || !st.IsDir() {
		t.Fatalf("the start check did not create the folder: %v", err)
	}
	if names, _ := filepath.Glob(filepath.Join(records, "*")); len(names) != 0 {
		t.Errorf("the start check left %v in the folder", names)
	}
	os.Remove(records)
	os.WriteFile(records, []byte("a file"), 0o600)
	w := strings.Join(SessionRecordStartLines("2.49.0", t.TempDir()), "\n")
	if w == "" || strings.Contains(w, "\n") || !strings.Contains(w, records) {
		t.Errorf("records is a file: warning %q, want one line naming the folder", w)
	}
}
