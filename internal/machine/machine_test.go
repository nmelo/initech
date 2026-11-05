package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMachineYAML(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCeiling_AnAbsentFileIsTheDefaults(t *testing.T) {
	c, err := LoadCeiling(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c != (Ceiling{Sessions: 30, FreeMemoryGB: 8, Load: 30}) {
		t.Fatalf("defaults = %+v, want 30 / 8 GB / 30", c)
	}
}

func TestCeiling_TheFileSetsItsKeysAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	writeMachineYAML(t, dir, "ceiling:\n  sessions: 3\n  load: 12.5\n")
	c, err := LoadCeiling(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Ceiling{Sessions: 3, FreeMemoryGB: 8, Load: 12.5}) {
		t.Fatalf("ceiling = %+v", c)
	}
}

func TestCeiling_ABadValueFailsNamingTheKey(t *testing.T) {
	cases := []struct{ body, key string }{
		{"ceiling:\n  sessions: many\n", "ceiling.sessions"},
		{"ceiling:\n  sessions: 0\n", "ceiling.sessions"},
		{"ceiling:\n  sessions: 2.5\n", "ceiling.sessions"},
		{"ceiling:\n  free_memory_gb: -1\n", "ceiling.free_memory_gb"},
		{"ceiling:\n  load: lots\n", "ceiling.load"},
		{"ceiling:\n  load: 0\n", "ceiling.load"},
		{"ceiling:\n  sesions: 3\n", "ceiling.sesions"},
		{"ceilng:\n  sessions: 3\n", "ceilng"},
		{"ceiling: 30\n", "ceiling:"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		writeMachineYAML(t, dir, tc.body)
		_, err := LoadCeiling(dir)
		if err == nil || !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%q: err = %v, want one naming %s", tc.body, err, tc.key)
		}
	}
}

func alwaysAlive(int) bool { return true }

func TestFleet_TwoRigsSumAndAStaleFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	for _, f := range []FleetFile{
		{Rig: "initech", PID: 1, Time: now, Awake: 9}, // our own: not summed here
		{Rig: "hover", PID: 2, Time: now.Add(-time.Minute), Awake: 7},
		{Rig: "theguy", PID: 3, Time: now.Add(-30 * time.Second), Awake: 4},
		{Rig: "old", PID: 4, Time: now.Add(-StaleAfter), Awake: 50}, // three minutes: stale
		{Rig: "gone", PID: 5, Time: now, Awake: 60},                 // pid gone
	} {
		if err := WriteFleet(dir, f); err != nil {
			t.Fatal(err)
		}
	}
	alive := func(pid int) bool { return pid != 5 }
	if got := SumOthers(dir, "initech", now, alive); got != 11 {
		t.Fatalf("sum of other rigs = %d, want 11 (hover 7 + theguy 4; old is stale, gone has no pid)", got)
	}
}

func TestFleet_NoFolderSumsToZero(t *testing.T) {
	if got := SumOthers(filepath.Join(t.TempDir(), "absent"), "initech", time.Now(), alwaysAlive); got != 0 {
		t.Fatalf("sum with no fleet folder = %d, want 0", got)
	}
}

func TestFleet_TheFilesAre0600InA0700Folder(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFleet(dir, FleetFile{Rig: "initech", PID: os.Getpid(), Time: time.Now(), Awake: 3}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, FleetDir, "initech.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("fleet file mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Join(dir, FleetDir))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("fleet folder mode = %o, want 700", di.Mode().Perm())
	}
}

func TestFleet_ARigNameNeverLeavesTheFolder(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFleet(dir, FleetFile{Rig: "../../evil", PID: 1, Time: time.Now(), Awake: 1}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, FleetDir))
	if len(entries) != 1 || strings.Contains(entries[0].Name(), "/") {
		t.Fatalf("fleet folder = %v, want one file inside it", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.json")); err == nil {
		t.Fatal("a rig name wrote outside the fleet folder")
	}
}

var testCeiling = Ceiling{Sessions: 30, FreeMemoryGB: 8, Load: 30}

func under() Reading {
	return Reading{Awake: 10, AwakeOK: true, FreeGB: 20, FreeOK: true, Load: 5, LoadOK: true}
}

func TestGate_EachNumberEngagesItAtItsValue(t *testing.T) {
	cases := map[string]func(*Reading){
		"sessions at 30": func(r *Reading) { r.Awake = 30 },
		"free at 8 GB":   func(r *Reading) { r.FreeGB = 8 },
		"load at 30":     func(r *Reading) { r.Load = 30 },
	}
	for name, set := range cases {
		r := under()
		set(&r)
		var g Gate
		if engaged, _ := g.Step(r, testCeiling, time.Now()); !engaged {
			t.Errorf("%s: gate not engaged", name)
		}
	}
	var g Gate
	if engaged, _ := g.Step(under(), testCeiling, time.Now()); engaged {
		t.Error("all three under: gate engaged")
	}
}

func TestGate_ClearsOnlyAfterAFullMinuteUnder(t *testing.T) {
	var g Gate
	t0 := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	over := under()
	over.Awake = 31
	g.Step(over, testCeiling, t0)
	// A dip under for less than a minute does not clear it.
	if engaged, cleared := g.Step(under(), testCeiling, t0.Add(10*time.Second)); !engaged || cleared {
		t.Fatalf("first reading under: engaged=%v cleared=%v, want still engaged", engaged, cleared)
	}
	if engaged, _ := g.Step(under(), testCeiling, t0.Add(69*time.Second)); !engaged {
		t.Fatal("59 s under: cleared early")
	}
	// Back over restarts the minute.
	g.Step(over, testCeiling, t0.Add(70*time.Second))
	if engaged, _ := g.Step(under(), testCeiling, t0.Add(80*time.Second)); !engaged {
		t.Fatal("under again after over: cleared without a fresh minute")
	}
	if engaged, _ := g.Step(under(), testCeiling, t0.Add(139*time.Second)); !engaged {
		t.Fatal("59 s into the second minute: cleared early")
	}
	engaged, cleared := g.Step(under(), testCeiling, t0.Add(140*time.Second))
	if engaged || !cleared {
		t.Fatalf("a full minute under: engaged=%v cleared=%v, want cleared", engaged, cleared)
	}
}

func TestGate_AnUnreadableNumberNeverEngagesIt(t *testing.T) {
	var g Gate
	r := Reading{Awake: 99, FreeGB: 0, Load: 99} // nothing readable
	if engaged, _ := g.Step(r, testCeiling, time.Now()); engaged {
		t.Fatal("unreadable numbers engaged the gate")
	}
	if g.Engaged() {
		t.Fatal("Engaged() true on unreadable numbers")
	}
	var fresh Gate
	if fresh.Engaged() {
		t.Fatal("a gate with no reading is engaged")
	}
}

func TestWords_TheRulingsLines(t *testing.T) {
	r := Reading{Awake: 31, AwakeOK: true, FreeGB: 6.1, FreeOK: true, Load: 34, LoadOK: true}
	if got, want := Refusal(r, testCeiling), "not started: the Mac is at its ceiling: 31 awake sessions, 6.1 GB free, load 34; ceiling 30 / 8 GB / 30"; got != want {
		t.Errorf("refusal\n got %q\nwant %q", got, want)
	}
	if got, want := StatusLine(r, testCeiling), "fleet 31 awake · 6.1 GB free · load 34 · ceiling 30 / 8 GB / 30"; got != want {
		t.Errorf("status line\n got %q\nwant %q", got, want)
	}
	if got := StatusLine(Reading{}, testCeiling); got != "fleet ? awake · ? GB free · load ? · ceiling 30 / 8 GB / 30" {
		t.Errorf("unreadable status line = %q", got)
	}
}

func TestParseLoad5_ReadsTheFiveMinuteFigure(t *testing.T) {
	for in, want := range map[string]float64{"{ 7.50 8.84 9.37 }\n": 8.84, "1.00 2.50 3.00 2/900 1234\n": 2.5} {
		got, err := parseLoad5(in)
		if err != nil || got != want {
			t.Errorf("parseLoad5(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseLoad5(""); err == nil {
		t.Error("an empty load line parsed")
	}
}

func TestGate_AdmitCountsTheWakesItLetsThroughUntilTheNextReading(t *testing.T) {
	var g Gate
	r := under()
	r.Awake = 28
	g.Step(r, testCeiling, time.Now())
	for i := 0; i < 2; i++ {
		if ok, words := g.Admit(); !ok {
			t.Fatalf("wake %d at %d awake refused: %s", i+1, 28+i, words)
		}
	}
	ok, words := g.Admit()
	if ok {
		t.Fatal("third wake inside the minute admitted: 28 + 2 is the ceiling")
	}
	if !strings.Contains(words, "30 awake sessions") {
		t.Fatalf("refusal %q does not count the admitted wakes", words)
	}
	// The next reading counts them itself.
	r.Awake = 25
	g.Step(r, testCeiling, time.Now())
	if ok, _ := g.Admit(); !ok {
		t.Fatal("a fresh reading under the ceiling still refuses")
	}
}

func TestGate_AdmitBeforeAnyReadingLetsItThrough(t *testing.T) {
	var g Gate
	if ok, _ := g.Admit(); !ok {
		t.Fatal("a gate with no reading refused")
	}
}

func TestGate_AdmitRefusesWithTheWordsWhileEngaged(t *testing.T) {
	var g Gate
	r := Reading{Awake: 31, AwakeOK: true, FreeGB: 6.1, FreeOK: true, Load: 34, LoadOK: true}
	g.Step(r, testCeiling, time.Now())
	ok, words := g.Admit()
	if ok || words != "not started: the Mac is at its ceiling: 31 awake sessions, 6.1 GB free, load 34; ceiling 30 / 8 GB / 30" {
		t.Fatalf("Admit at the ceiling = %v %q", ok, words)
	}
}
