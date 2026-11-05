package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/machine"
)

// rigCeilingEnv is the environment a rig's initech child gets on top of its
// own: the machine folder (machine.DirEnv) in a scratch folder whose
// machine.yaml sets a ceiling no test runner reaches (a 7 GB CI runner is
// always under 8 GB free). Set by isolateMachineCeiling. It never moves HOME:
// the child passes HOME on to the agents it starts, and a real Claude with a
// scratch HOME opens on its first-run screen instead of the session a rig
// reads (ini-f3lq).
var rigCeilingEnv []string

// isolateMachineCeiling keeps the package's tests off this machine's
// ceiling (ini-49q): in-process TUIs get reads that see nothing (so the
// ceiling never engages and no fleet file is written), and rig children a
// scratch home. It returns the undo.
func isolateMachineCeiling() func() {
	prev, prevQuota := ceilingDefaults, quotaPath
	// Nor the operator's account usage file (ini-kb4c).
	quotaPath = func() (string, error) { return filepath.Join(os.TempDir(), "initech-tests-no-usage.json"), nil }
	none := errors.New("tests do not read this machine")
	ceilingDefaults = func() ceilingReads {
		return ceilingReads{
			dir:       func() (string, error) { return "", none },
			procTable: func() (string, error) { return "", none },
			freeKB:    func() (int64, error) { return 0, none },
			totalKB:   func() (int64, error) { return 0, none },
			swapKB:    func() (int64, error) { return 0, none },
			load5:     func() (float64, error) { return 0, none },
			alive:     func(int) bool { return false },
		}
	}
	home, err := os.MkdirTemp("", "initech-ceiling-home-")
	if err == nil {
		writeOpenCeiling(home)
		rigCeilingEnv = []string{machine.DirEnv + "=" + home}
	}
	return func() {
		ceilingDefaults, quotaPath = prev, prevQuota
		if home != "" {
			_ = os.RemoveAll(home)
		}
	}
}

// writeOpenCeiling writes, under a scratch home, a machine.yaml with a
// ceiling no test runner reaches, where each platform's initech looks.
func writeOpenCeiling(dir string) {
	open := []byte("ceiling:\n  sessions: 100000\n  free_memory_gb: 0\n  load: 100000\n")
	_ = os.WriteFile(filepath.Join(dir, machine.FileName), open, 0o600)
}

// openCeilingConfigEnv points the config folder variables at home, for a
// test that gives its child a scratch HOME on purpose (no Claude agents).
func openCeilingConfigEnv(home string) []string {
	return []string{"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "APPDATA=" + filepath.Join(home, "AppData", "Roaming")}
}

// The package's tests never read this machine: a TUI with no reads set
// steps a reading in which nothing is readable, and writes no fleet file.
func TestCeilingIsolation_ATestTUIReadsNothingOfThisMachine(t *testing.T) {
	tui := newTestTUI()
	tui.stepCeiling(nil, time.Now())
	r, _, engaged, _ := tui.ceiling.gate.Snapshot()
	if r.AwakeOK || r.FreeOK || r.LoadOK || engaged {
		t.Fatalf("a test TUI read this machine: %+v engaged=%v", r, engaged)
	}
	if len(rigCeilingEnv) == 0 {
		t.Fatal("rig children get no scratch machine folder")
	}
	dir := ""
	for _, kv := range rigCeilingEnv {
		if strings.HasPrefix(kv, machine.DirEnv+"=") {
			dir = strings.TrimPrefix(kv, machine.DirEnv+"=")
		}
	}
	// What a rig's child resolves, through the product's own Dir.
	t.Setenv(machine.DirEnv, dir)
	got, err := machine.Dir()
	if err != nil || got != dir || dir == "" {
		t.Fatalf("a rig child's machine folder = %q, %v; want the scratch %q", got, err, dir)
	}
	c, err := machine.LoadCeiling(got, 16)
	if err != nil || c.Sessions != 100000 || c.FreeMemoryGB != 0 {
		t.Fatalf("rig ceiling = %+v, %v; want the open ceiling", c, err)
	}
}

// TestCeilingIsolation_ARigsAgentsKeepTheRealHome is ini-f3lq: a rig child
// that moved HOME passed it to its Claude agents, which opened on their
// first-run screen, so the 6M4 rig never saw a session.
func TestCeilingIsolation_ARigsAgentsKeepTheRealHome(t *testing.T) {
	for _, kv := range rigCeilingEnv {
		for _, k := range []string{"HOME=", "XDG_CONFIG_HOME=", "APPDATA="} {
			if strings.HasPrefix(kv, k) {
				t.Fatalf("rig children get %s, which their agents inherit", kv)
			}
		}
	}
}
