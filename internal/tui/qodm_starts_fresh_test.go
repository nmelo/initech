//go:build !windows

package tui

// qodm_starts_fresh_test.go is ini-qodm's composed check: after retiring the
// per-window layout API, a real second window must start at the DEFAULT
// arrangement and window 1's saved layout must be untouched.
//
// This is the AC's "one composed two-process attach after deletion". It runs
// real binaries over a real window server, because the claim being verified is
// about what two PROCESSES do to one file -- which is exactly the class the
// one-process evidence rig could not see (ini-i7fr's named debt).
//
// '//go:build !windows' matches the rest of the two-process suites, whose
// server hangs the tui package on Windows CI (run 31651424793).

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestQodm_ViewerStartsFreshAndLeavesWindowOnesLayoutAlone drives a viewer
// against a live window 1 and asserts the retirement's two halves at once.
//
// WHAT IT PROVES: across a real attach/interact/exit, no per-window layout
// file is created, window 1's saved arrangement is byte-identical afterwards,
// and a leftover pre-upgrade layout-window-2.yaml is neither read nor
// rewritten nor deleted.
//
// WHAT IT DOES NOT PROVE, measured rather than assumed: it does not kill the
// ini-la97 viewer-write mutant. With that guard removed this rig still writes
// nothing, because the subprocess does not reliably reach the layout write
// path -- the same limitation recorded on the la97 decoy suite, and closed
// there by qa1's own composed rig rather than by this one. The write guard's
// teeth are la97's in-process tests; this test's job is the FILE-level facts
// of the retirement, which it does establish.
func TestQodm_ViewerStartsFreshAndLeavesWindowOnesLayoutAlone(t *testing.T) {
	root, err := os.MkdirTemp("", "qodm")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	defer os.RemoveAll(root)

	for _, role := range []string{"super", "eng1"} {
		if err := os.MkdirAll(filepath.Join(root, role), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", role, err)
		}
		if err := os.WriteFile(filepath.Join(root, role, "CLAUDE.md"), []byte("# "+role+"\n"), 0o644); err != nil {
			t.Fatalf("write CLAUDE.md: %v", err)
		}
	}

	_, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("super")})
	cfgYAML := "project: qodm\nroot: " + root + "\nroles:\n    - super\n    - eng1\n" +
		"claude_command:\n    - sleep\nclaude_args:\n    - \"300\"\n" +
		"window_listen: \"" + addr + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dir := filepath.Join(root, ".initech")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir .initech: %v", err)
	}
	// Window 1's layout memory: a distinctive arrangement no default produces.
	windowOneLayout := "grid: 4x2\nmode: focus\ngrid_explicit: true\n" +
		"order:\n    - eng1\n    - super\ngroups:\n    - core\ngroup_of:\n    super: core\n    eng1: core\n"
	layoutFile := filepath.Join(dir, "layout.yaml")
	if err := os.WriteFile(layoutFile, []byte(windowOneLayout), 0o600); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
	// A per-window file as a pre-upgrade fleet would have had it. Nothing may
	// read it, and nothing may recreate it.
	stale := filepath.Join(dir, "layout-window-2.yaml")
	if err := os.WriteFile(stale, []byte("grid: 1x1\nmode: grid\norder:\n    - qodm-sentinel\n"), 0o600); err != nil {
		t.Fatalf("seed stale per-window file: %v", err)
	}
	staleBefore, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read stale: %v", err)
	}

	// A real viewer: attach, open the modal (the path that used to persist
	// layout), and exit the way an operator does.
	runViewerUnderPTYKeysSig(t, buildInitechBinary(t), root, 5*time.Second, "a", syscall.SIGHUP)

	// 1. Window 1's layout memory is untouched.
	after, err := os.ReadFile(layoutFile)
	if err != nil {
		t.Fatalf("window 1's layout file vanished: %v", err)
	}
	if string(after) != windowOneLayout {
		t.Errorf(`WINDOW 1'S LAYOUT MEMORY WAS MODIFIED by a viewer's session.

Arrangement is window 1's; a viewer neither writes it nor inherits it.

before:
%s
after:
%s`, windowOneLayout, string(after))
	}

	// 2. The viewer did not resurrect the retired per-window file, and did not
	//    read it either -- if it had, its sentinel agent would have to come
	//    from somewhere, and nothing in this fleet is named for it.
	nowStale, err := os.ReadFile(stale)
	if err != nil {
		t.Errorf("the retired per-window file was DELETED by the viewer (%v); the retirement "+
			"removes the API, it does not clean up an operator's leftover files", err)
	} else if string(nowStale) != string(staleBefore) {
		t.Errorf("the retired per-window file was REWRITTEN, so something still persists to it:\n%s", nowStale)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "layout-") && e.Name() != "layout-window-2.yaml" {
			t.Errorf("a per-window layout file %q was created after the retirement", e.Name())
		}
	}
}
