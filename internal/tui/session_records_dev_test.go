package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// ini-92x: a dev build, any version that is not a plain release number, never
// writes under ~/.claude-accounts. The cla ledger reads every *.jsonl there,
// so a test rig's session would reach the real spend figures.

// devRecorderTUI is window 1 of a rig at a fresh root, stamped with version.
func devRecorderTUI(t *testing.T, version string) (*TUI, string) {
	t.Helper()
	root := t.TempDir()
	tu := newTestTUI(testPane("eng2"))
	tu.projectRoot, tu.projectName, tu.version = root, "initech", version
	return tu, root
}

// recordOneLine drives the recorder the way the main loop does: one working
// pane with a session id, one reconcile.
func recordOneLine(t *testing.T, tu *TUI) {
	t.Helper()
	r := tu.sessionBeadRecorderFor(false)
	if r == nil {
		t.Fatal("window 1 has no recorder")
	}
	r.host = "nmbp"
	r.reconcile([]paneWork{sbWork(new(int), "ini-cnp", "bd", sbBase)}, sbBase.Add(time.Minute))
}

// assertNoAccountFolder fails if anything was made under ~/.claude-accounts,
// the folder itself included: a start check that creates it empty still
// touches the ledger's home.
func assertNoAccountFolder(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(home, ".claude-accounts")); err == nil {
		var names []string
		filepath.WalkDir(filepath.Join(home, ".claude-accounts"), func(p string, _ os.DirEntry, _ error) error {
			names = append(names, p)
			return nil
		})
		t.Errorf("a dev build touched the account folder: %v", names)
	}
}

const agreedRecordLine = `{"v":1,"rig":"initech","host":"nmbp","session_id":"0f3c-uuid","role":"eng2","bead":"ini-cnp","source":"bd","from":"2026-10-31T20:00:00Z","to":null}` + "\n"

// assertRigFolderHasRecord checks the rig folder got the record and the owner
// marker, under the same names and in the same format as the account folder.
func assertRigFolderHasRecord(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".initech", "records")
	if b, err := os.ReadFile(filepath.Join(dir, "initech-2026-10.jsonl")); err != nil || string(b) != agreedRecordLine {
		t.Errorf("rig folder record = %q (%v), want the one agreed line", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "initech.owner")); err != nil || strings.TrimSpace(string(b)) != root {
		t.Errorf("rig folder owner marker = %q (%v), want %q", b, err, root)
	}
}

// AC1, and AC4's hand-set and prerelease cases: every version that is not a
// plain release number records in the rig folder and leaves no trace in the
// account folder. Red at base: the record goes to the account folder.
func TestDevBuildRecords_GoToTheRigFolderNeverTheAccountFolder(t *testing.T) {
	for _, version := range []string{"dev", "", "qa", "2.50.0-rc1"} {
		t.Run("version="+version, func(t *testing.T) {
			records := recordsHome(t)
			tu, root := devRecorderTUI(t, version)
			recordOneLine(t, tu)
			assertRigFolderHasRecord(t, root)
			assertNoAccountFolder(t, filepath.Dir(filepath.Dir(records)))
		})
	}
}

// AC2, and AC4's v-prefixed release: the two shapes our release ldflags can
// stamp write to the account folder exactly as before, and nothing appears in
// the rig folder. Red if the dev check is inverted.
func TestReleaseBuildRecords_GoToTheAccountFolderOnly(t *testing.T) {
	for _, version := range []string{"2.49.0", "v2.49.0"} {
		t.Run("version="+version, func(t *testing.T) {
			records := recordsHome(t)
			tu, root := devRecorderTUI(t, version)
			recordOneLine(t, tu)
			if b, err := os.ReadFile(filepath.Join(records, "initech-2026-10.jsonl")); err != nil || string(b) != agreedRecordLine {
				t.Errorf("account record = %q (%v), want the one agreed line", b, err)
			}
			if b, err := os.ReadFile(filepath.Join(records, "initech.owner")); err != nil || strings.TrimSpace(string(b)) != root {
				t.Errorf("account owner marker = %q (%v), want %q", b, err, root)
			}
			if _, err := os.Stat(filepath.Join(root, ".initech", "records")); err == nil {
				t.Error("a release build made a records folder in the rig")
			}
		})
	}
}

// AC4: what counts as a release is exactly X.Y.Z with an optional leading v.
func TestIsReleaseVersion_OnlyAPlainReleaseNumber(t *testing.T) {
	for v, want := range map[string]bool{
		"2.49.0":      true,
		"v2.49.0":     true,
		"0.0.1":       true,
		"10.20.30":    true,
		"":            false,
		"dev":         false,
		"qa":          false,
		"2.50.0-rc1":  false,
		"v2.50.0-rc1": false,
		"2.49":        false,
		"2.49.0.1":    false,
		"V2.49.0":     false,
		"vv2.49.0":    false,
		"2.49.0\n":    false,
		" 2.49.0":     false,
		"2.49.x":      false,
		"v":           false,
	} {
		if got := IsReleaseVersion(v); got != want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

// AC1: a dev start names the path once, and the check does not make the
// account folder. A release start says nothing when the folder can be written.
func TestDevBuildRecords_StartSaysWhereOnce(t *testing.T) {
	records := recordsHome(t)
	root := t.TempDir()
	lines := SessionRecordStartLines("dev", root)
	want := "dev build: session records go to " + filepath.Join(root, ".initech", "records") + ", not the account ledger"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("dev start lines = %q, want exactly %q", lines, want)
	}
	assertNoAccountFolder(t, filepath.Dir(filepath.Dir(records)))

	if lines := SessionRecordStartLines("2.49.0", t.TempDir()); len(lines) != 0 {
		t.Errorf("release start lines = %q, want none", lines)
	}
}

// AC3: a dev build whose rig folder cannot be written logs once, writes
// nowhere else, and keeps going, as the account path does. The start says so
// against the rig folder too, beside the line that names it.
func TestDevBuildRecords_AnUnwritableRigFolderLogsOnceAndWritesNowhereElse(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through 0500")
	}
	records := recordsHome(t)
	logs := captureLogs(t)
	tu, root := devRecorderTUI(t, "dev")
	dir := filepath.Join(root, ".initech", "records")
	// The rig's .initech is writable; only the records folder is not (MkdirAll
	// would give .initech the same 0500).
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	r := tu.sessionBeadRecorderFor(false)
	w := sbWork(new(int), "ini-a", "bd", sbBase)
	for i := 0; i < 5; i++ { // long enough for several probes, each of which fails
		r.reconcile([]paneWork{w}, sbBase.Add(time.Duration(i+1)*90*time.Second))
	}
	if n := strings.Count(logs.String(), "cannot write the session-bead record"); n != 1 {
		t.Errorf("the failure was logged %d times, want once", n)
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 0 {
		t.Errorf("an unwritable rig folder got %v", names)
	}
	assertNoAccountFolder(t, filepath.Dir(filepath.Dir(records)))

	lines := SessionRecordStartLines("dev", root)
	if len(lines) != 2 || !strings.Contains(lines[1], dir) {
		t.Errorf("start lines = %q, want the dev line and one warning naming %s", lines, dir)
	}
}

// AC3: with HOME unset a dev build still records in the rig folder. A release
// build has no folder to write and says so, as before.
func TestDevBuildRecords_HomeUnsetStillWritesUnderTheRigFolder(t *testing.T) {
	t.Setenv("HOME", "")
	tu, root := devRecorderTUI(t, "dev")
	recordOneLine(t, tu)
	assertRigFolderHasRecord(t, root)

	if got := sessionRecordsDirFor("2.49.0", root); got != "" {
		t.Errorf("a release build with no home records to %q, want none", got)
	}
}

// AC3 feed-it-nothing: with no rig root there is no folder at all, never a
// path relative to wherever the process happens to run, and nothing to say.
func TestDevBuildRecords_NoRigRootHasNoFolder(t *testing.T) {
	if got := sessionRecordsDirFor("dev", ""); got != "" {
		t.Errorf("no rig root: %q, want no folder", got)
	}
	if l := SessionRecordStartLines("dev", ""); len(l) != 0 {
		t.Errorf("no rig root: start lines %q, want none", l)
	}
}

// Reachability: a daemon's recorder and start line come from the version it
// was started with, as window 1's do. A daemon of a dev build leaves the
// account folder alone.
func TestDevBuildRecords_ADaemonRecordsByItsOwnVersion(t *testing.T) {
	records := recordsHome(t)
	root := t.TempDir()
	d := &Daemon{project: &config.Project{Root: root, Name: "initech"}, version: "dev"}
	var buf bytes.Buffer
	d.startSessionRecord(&buf)
	if got := strings.Count(buf.String(), "dev build: session records go to "+filepath.Join(root, ".initech", "records")); got != 1 {
		t.Errorf("a dev daemon's start said %q, want the path once", buf.String())
	}
	if d.sessionBeads == nil {
		t.Fatal("a dev daemon has no recorder")
	}
	d.sessionBeads.host = "nmbp"
	d.sessionBeads.reconcile([]paneWork{sbWork(new(int), "ini-cnp", "bd", sbBase)}, sbBase.Add(time.Minute))
	assertRigFolderHasRecord(t, root)
	assertNoAccountFolder(t, filepath.Dir(filepath.Dir(records)))

	rel := &Daemon{project: &config.Project{Root: root, Name: "initech"}, version: "v2.49.0"}
	buf.Reset()
	rel.startSessionRecord(&buf)
	if rel.sessionBeads == nil || rel.sessionBeads.dir != records {
		t.Errorf("a release daemon records in %+v, want %s", rel.sessionBeads, records)
	}
	if buf.Len() != 0 {
		t.Errorf("a release daemon's start said %q, want nothing", buf.String())
	}
}
