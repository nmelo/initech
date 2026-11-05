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
// own: HOME and XDG_CONFIG_HOME in a scratch folder whose machine.yaml sets
// a ceiling no test runner reaches (a 7 GB CI runner is always under 8 GB
// free). Set by isolateMachineCeiling.
var rigCeilingEnv []string

// isolateMachineCeiling keeps the package's tests off this machine's
// ceiling (ini-49q): in-process TUIs get reads that see nothing (so the
// ceiling never engages and no fleet file is written), and rig children a
// scratch home. It returns the undo.
func isolateMachineCeiling() func() {
	prev := ceilingDefaults
	none := errors.New("tests do not read this machine")
	ceilingDefaults = func() ceilingReads {
		return ceilingReads{
			dir:       func() (string, error) { return "", none },
			procTable: func() (string, error) { return "", none },
			freeKB:    func() (int64, error) { return 0, none },
			load5:     func() (float64, error) { return 0, none },
			alive:     func(int) bool { return false },
		}
	}
	home, err := os.MkdirTemp("", "initech-ceiling-home-")
	if err == nil {
		writeOpenCeiling(home)
		rigCeilingEnv = append([]string{"HOME=" + home}, openCeilingConfigEnv(home)...)
	}
	return func() {
		ceilingDefaults = prev
		if home != "" {
			_ = os.RemoveAll(home)
		}
	}
}

// writeOpenCeiling writes, under a scratch home, a machine.yaml with a
// ceiling no test runner reaches, where each platform's initech looks.
func writeOpenCeiling(home string) {
	open := []byte("ceiling:\n  sessions: 100000\n  free_memory_gb: 0\n  load: 100000\n")
	for _, dir := range []string{
		filepath.Join(home, "Library", "Application Support", "initech"), // macOS
		filepath.Join(home, ".config", "initech"),                        // Linux
		filepath.Join(home, "AppData", "Roaming", "initech"),             // Windows
	} {
		if os.MkdirAll(dir, 0o700) == nil {
			_ = os.WriteFile(filepath.Join(dir, "machine.yaml"), open, 0o600)
		}
	}
}

// openCeilingConfigEnv points the config folder variables at home, so a
// value inherited from the machine cannot lead the child back to it.
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
		t.Fatal("rig children get no scratch home")
	}
	home := strings.TrimPrefix(rigCeilingEnv[0], "HOME=")
	c, err := machine.LoadCeiling(filepath.Join(home, "Library", "Application Support", "initech"))
	if err != nil || c.Sessions != 100000 || c.FreeMemoryGB != 0 {
		t.Fatalf("rig ceiling = %+v, %v; want the open ceiling", c, err)
	}
}
