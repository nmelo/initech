package machine

import (
	"os"
	"path/filepath"
	"runtime"
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
	c, err := LoadCeiling(t.TempDir(), 48)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Ceiling{Sessions: 30, FreeMemoryGB: 8, Load: 30, FreeSource: SourceDefault}) {
		t.Fatalf("defaults = %+v, want 30 / 8 GB / 30", c)
	}
}

func TestCeiling_TheFileSetsItsKeysAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	writeMachineYAML(t, dir, "ceiling:\n  sessions: 3\n  load: 12.5\n")
	c, err := LoadCeiling(dir, 48)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Ceiling{Sessions: 3, FreeMemoryGB: 8, Load: 12.5, FreeSource: SourceDefault}) {
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
		_, err := LoadCeiling(dir, 48)
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
	if got, want := StatusLine(r, testCeiling), "fleet 31 awake · 6.1 GB free · swap ? GB · load 34 · ceiling 30 / 8 GB / 30"; got != want {
		t.Errorf("status line\n got %q\nwant %q", got, want)
	}
	if got := StatusLine(Reading{}, testCeiling); got != "fleet ? awake · ? GB free · swap ? GB · load ? · ceiling 30 / 8 GB / 30" {
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

func TestCeilingFor_TheLowerOf8GBAnd20PercentOfTotal(t *testing.T) {
	cases := []struct {
		total, want float64
		source      string
	}{
		{48, 8, SourceDefault},  // 9.6: the cap holds
		{256, 8, SourceDefault}, // 51.2: the cap holds
		{40, 8, SourceDefault},  // exactly 8
		{32, 6.4, "20% of 32 GB"},
		{16, 3.2, "20% of 16 GB"},
		{8, 1.6, "20% of 8 GB"},
		{0, 8, SourceDefault}, // total unknown
		{-1, 8, SourceDefault},
	}
	for _, c := range cases {
		got := CeilingFor(c.total)
		if got.FreeMemoryGB != c.want || got.FreeSource != c.source {
			t.Errorf("total %v GB: %v GB (%s), want %v GB (%s)", c.total, got.FreeMemoryGB, got.FreeSource, c.want, c.source)
		}
		if got.Sessions != 30 || got.Load != 30 {
			t.Errorf("total %v GB moved the other numbers: %+v", c.total, got)
		}
	}
}

func TestCeiling_ASetFreeMemoryKeyIsUsedAsWritten(t *testing.T) {
	for _, total := range []float64{16, 48, 256} {
		dir := t.TempDir()
		writeMachineYAML(t, dir, "ceiling:\n  free_memory_gb: 12\n")
		c, err := LoadCeiling(dir, total)
		if err != nil {
			t.Fatal(err)
		}
		if c.FreeMemoryGB != 12 || c.FreeSource != SourceFile {
			t.Errorf("total %v GB: %v GB (%s), want 12 GB (machine.yaml)", total, c.FreeMemoryGB, c.FreeSource)
		}
	}
}

func TestCeiling_AnAbsentFileUsesTheMachinesDefault(t *testing.T) {
	c, err := LoadCeiling(t.TempDir(), 16)
	if err != nil || c.FreeMemoryGB != 3.2 {
		t.Fatalf("absent file on 16 GB = %+v, %v; want 3.2 GB", c, err)
	}
}

func TestStatusLine_NamesTheValueInEffectAndItsSource(t *testing.T) {
	r := Reading{Awake: 3, AwakeOK: true, FreeGB: 18.7, FreeOK: true, Load: 6, LoadOK: true, SwapGB: 2.34, SwapOK: true}
	for _, tc := range []struct {
		c    Ceiling
		want string
	}{
		{CeilingFor(48), "fleet 3 awake · 18.7 GB free · swap 2.3 GB · load 6 · ceiling 30 / 8 GB (default) / 30"},
		{CeilingFor(16), "fleet 3 awake · 18.7 GB free · swap 2.3 GB · load 6 · ceiling 30 / 3.2 GB (20% of 16 GB) / 30"},
		{Ceiling{Sessions: 30, FreeMemoryGB: 12, Load: 30, FreeSource: SourceFile}, "fleet 3 awake · 18.7 GB free · swap 2.3 GB · load 6 · ceiling 30 / 12 GB (machine.yaml) / 30"},
	} {
		if got := StatusLine(r, tc.c); got != tc.want {
			t.Errorf("line\n got %q\nwant %q", got, tc.want)
		}
	}
	// The refusal gives the value in effect, in the ruling's words.
	if got := Refusal(r, CeilingFor(16)); !strings.HasSuffix(got, "ceiling 30 / 3.2 GB / 30") {
		t.Errorf("refusal %q", got)
	}
}

func TestOver_SwapNeverEngagesIt(t *testing.T) {
	r := under()
	r.SwapGB, r.SwapOK = 1e6, true
	if Over(r, testCeiling) {
		t.Fatal("swap engaged the ceiling")
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Real output: two captures from a 48 GB M-series Mac, a hand-built 4096-byte
// page output, and a Linux workstation's /proc/meminfo. The expected figures
// were computed from the files with awk, not with this parser.
func TestParseVMStat_ReclaimableFromRealOutput(t *testing.T) {
	for name, want := range map[string]int64{
		"vm_stat_m_series_48gb.txt":        19629696, // free 0.76 GB, reclaimable 18.7 GB
		"vm_stat_m_series_48gb_second.txt": 23469952,
		"vm_stat_intel_4096_handbuilt.txt": 20000000, // free 1.28 GB, reclaimable 19.1 GB
	} {
		got, err := parseVMStat(readFixture(t, name))
		if err != nil || got != want {
			t.Errorf("%s: %d, %v; want %d KB", name, got, err, want)
		}
	}
}

// A Mac with most of its memory in file cache sits under the ceiling on
// reclaimable memory, where bare free pages would keep it shut.
func TestParseVMStat_AHealthyMacIsNotAtTheCeiling(t *testing.T) {
	kb, err := parseVMStat(readFixture(t, "vm_stat_m_series_48gb.txt"))
	if err != nil {
		t.Fatal(err)
	}
	r := under()
	r.FreeGB = float64(kb) / (1024 * 1024)
	if Over(r, CeilingFor(48)) {
		t.Fatalf("%.1f GB reclaimable on 48 GB engaged the ceiling", r.FreeGB)
	}
}

func TestParseVMStat_AMissingLineIsUnreadable(t *testing.T) {
	real := readFixture(t, "vm_stat_m_series_48gb.txt")
	for _, drop := range []string{"page size of", "Pages free:", "Pages inactive:", "Pages speculative:"} {
		var kept []string
		for _, l := range strings.Split(real, "\n") {
			if !strings.Contains(l, drop) {
				kept = append(kept, l)
			}
		}
		if _, err := parseVMStat(strings.Join(kept, "\n")); err == nil {
			t.Errorf("output without %q parsed", drop)
		}
	}
	for _, bad := range []string{"", "not vm_stat at all"} {
		if _, err := parseVMStat(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestMeminfo_FromRealOutput(t *testing.T) {
	real := readFixture(t, "meminfo_workbench_linux.txt")
	if got, err := parseMeminfoReclaimable(real); err != nil || got != 34469828 {
		t.Errorf("MemAvailable = %d, %v (MemFree is 11421860: it must not be read)", got, err)
	}
	if got, err := meminfoKB(real, "MemTotal"); err != nil || got != 63310140 {
		t.Errorf("MemTotal = %d, %v", got, err)
	}
	if got, err := parseMeminfoSwapUsed(real); err != nil || got != 8388604-1941232 {
		t.Errorf("swap used = %d, %v", got, err)
	}
	if _, err := meminfoKB("MemFree: 1 kB\n", "MemAvailable"); err == nil {
		t.Error("meminfo with no MemAvailable parsed")
	}
}

func TestParseSwapUsage_RealAndScaled(t *testing.T) {
	for in, want := range map[string]int64{
		"total = 0.00M  used = 0.00M  free = 0.00M  (encrypted)\n":        0, // this Mac tonight
		"total = 2048.00M  used = 1024.50M  free = 1023.50M  (encrypted)": 1049088,
		"total = 8.00G  used = 2.00G  free = 6.00G":                       2097152,
		"total = 512.00K  used = 256.00K  free = 256.00K":                 256,
	} {
		if got, err := parseSwapUsage(in); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "total = 1M", "used = lots"} {
		if _, err := parseSwapUsage(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// The readers against this machine, on the two systems they read.
func TestReaders_ReadThisMachine(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("memory is not read on this platform")
	}
	total, err := TotalKB()
	if err != nil || total <= 0 {
		t.Fatalf("TotalKB = %d, %v", total, err)
	}
	free, err := ReclaimableKB()
	if err != nil || free <= 0 || free > total {
		t.Fatalf("ReclaimableKB = %d of %d, %v", free, total, err)
	}
	if swap, err := SwapUsedKB(); err != nil || swap < 0 {
		t.Fatalf("SwapUsedKB = %d, %v", swap, err)
	}
	if load, err := Load5(); err != nil || load < 0 {
		t.Fatalf("Load5 = %v, %v", load, err)
	}
}

// TestDir_TheVariableNamesTheFolderOutright: a rig points its initech at a
// scratch ceiling without moving HOME (ini-f3lq); unset, the user's folder.
func TestDir_TheVariableNamesTheFolderOutright(t *testing.T) {
	t.Setenv(DirEnv, "/tmp/scratch-machine")
	if d, err := Dir(); err != nil || d != "/tmp/scratch-machine" {
		t.Fatalf("Dir() = %q, %v with %s set", d, err, DirEnv)
	}
	t.Setenv(DirEnv, "")
	base, _ := os.UserConfigDir()
	if d, err := Dir(); err != nil || d != filepath.Join(base, "initech") {
		t.Fatalf("Dir() = %q, %v unset; want the user's config folder", d, err)
	}
}

// TestDir_AnEmptyOrMissingFolderGivesTheDefaultCeiling is feed-it-nothing:
// a folder with no machine.yaml, or none at all, reads the default ceiling,
// never an error.
func TestDir_AnEmptyOrMissingFolderGivesTheDefaultCeiling(t *testing.T) {
	for name, dir := range map[string]string{"empty": t.TempDir(), "missing": filepath.Join(t.TempDir(), "not-there")} {
		t.Setenv(DirEnv, dir)
		d, err := Dir()
		if err != nil || d != dir {
			t.Fatalf("%s: Dir() = %q, %v", name, d, err)
		}
		c, err := LoadCeiling(d, 16)
		if err != nil || c != CeilingFor(16) {
			t.Fatalf("%s folder: ceiling %+v, %v; want the default %+v", name, c, err, CeilingFor(16))
		}
	}
}
