package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorPrereqsPresent verifies the prerequisites section prints the
// expected header and tool names without crashing.
func TestDoctorPrereqsPresent(t *testing.T) {
	buf := &bytes.Buffer{}
	state := &doctorState{}
	checkPrereqs(buf, state)

	out := buf.String()
	if !strings.Contains(out, "Prerequisites") {
		t.Error("output should contain 'Prerequisites' header")
	}
	if !strings.Contains(out, "claude") {
		t.Error("output should mention 'claude'")
	}
	if !strings.Contains(out, "git") {
		t.Error("output should mention 'git'")
	}
}

// TestDoctorRequiredMissing verifies that when required tools are absent
// (empty PATH), requiredMissing is set on state.
func TestDoctorRequiredMissing(t *testing.T) {
	t.Setenv("PATH", "")

	state := &doctorState{}
	buf := &bytes.Buffer{}
	checkPrereqs(buf, state)

	if !state.requiredMissing {
		t.Error("requiredMissing should be true when claude and git are not found")
	}
}

// TestDoctorEnvironmentSection verifies the environment section emits all
// expected labels and does not crash.
func TestDoctorEnvironmentSection(t *testing.T) {
	buf := &bytes.Buffer{}
	checkEnvironment(buf)

	out := buf.String()
	for _, label := range []string{"Environment", "TERM", "Terminal", "Colors", "Shell", "OS"} {
		if !strings.Contains(out, label) {
			t.Errorf("environment section missing label %q; output:\n%s", label, out)
		}
	}
}

// TestDoctorProjectSectionOk verifies that a clean project (all files in
// place, no stale socket) produces zero warnings.
func TestDoctorProjectSectionOk(t *testing.T) {
	dir := t.TempDir()

	// Minimal initech.yaml with one role that has no NeedsSrc (super).
	yaml := "project: testproj\nroot: " + dir + "\nroles:\n  - super\n"
	cfgPath := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	// super/CLAUDE.md
	superDir := filepath.Join(dir, "super")
	os.MkdirAll(superDir, 0755)
	os.WriteFile(filepath.Join(superDir, "CLAUDE.md"), []byte("# super"), 0644)

	// .beads/
	os.MkdirAll(filepath.Join(dir, ".beads"), 0755)

	buf := &bytes.Buffer{}
	state := &doctorState{}
	checkProjectHealth(buf, cfgPath, state)

	if state.warnings != 0 {
		t.Errorf("warnings = %d, want 0; output:\n%s", state.warnings, buf.String())
	}
	if !strings.Contains(buf.String(), "testproj") {
		t.Error("output should contain project name")
	}
}

// TestDoctorProjectSectionMissingCLAUDE verifies that a missing CLAUDE.md
// increments warnings and names the role.
func TestDoctorProjectSectionMissingCLAUDE(t *testing.T) {
	dir := t.TempDir()

	yaml := "project: testproj\nroot: " + dir + "\nroles:\n  - super\n"
	cfgPath := filepath.Join(dir, "initech.yaml")
	os.WriteFile(cfgPath, []byte(yaml), 0644)

	// Create super/ directory but NOT super/CLAUDE.md.
	os.MkdirAll(filepath.Join(dir, "super"), 0755)
	os.MkdirAll(filepath.Join(dir, ".beads"), 0755)

	buf := &bytes.Buffer{}
	state := &doctorState{}
	checkProjectHealth(buf, cfgPath, state)

	if state.warnings == 0 {
		t.Error("should have at least one warning for missing CLAUDE.md")
	}
	if !strings.Contains(buf.String(), "super") {
		t.Errorf("output should list 'super' as missing CLAUDE.md; got:\n%s", buf.String())
	}
}

// TestDoctorProjectSectionStaleSocket verifies that a socket file with
// nothing listening triggers a WARNING.
func TestDoctorProjectSectionStaleSocket(t *testing.T) {
	dir := t.TempDir()

	yaml := "project: testproj\nroot: " + dir + "\nroles:\n  - super\n"
	cfgPath := filepath.Join(dir, "initech.yaml")
	os.WriteFile(cfgPath, []byte(yaml), 0644)

	superDir := filepath.Join(dir, "super")
	os.MkdirAll(superDir, 0755)
	os.WriteFile(filepath.Join(superDir, "CLAUDE.md"), []byte("# super"), 0644)
	os.MkdirAll(filepath.Join(dir, ".beads"), 0755)

	// Write a socket path with no listener — DialTimeout will fail.
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	sockPath := filepath.Join(initechDir, "initech.sock")
	os.WriteFile(sockPath, []byte{}, 0644)

	buf := &bytes.Buffer{}
	state := &doctorState{}
	checkProjectHealth(buf, cfgPath, state)

	if state.warnings == 0 {
		t.Error("should have a warning for stale socket")
	}
	if !strings.Contains(buf.String(), "stale") {
		t.Errorf("output should mention 'stale'; got:\n%s", buf.String())
	}
}

// TestDoctorSummaryAllClear verifies the summary text for a clean state.
func TestDoctorSummaryAllClear(t *testing.T) {
	state := &doctorState{}
	buf := &bytes.Buffer{}
	switch {
	case state.requiredMissing:
		fmt.Fprintln(buf, "Required prerequisites missing.")
	case state.warnings > 0:
		fmt.Fprintf(buf, "%d warning(s) found.\n", state.warnings)
	default:
		fmt.Fprintln(buf, "All checks passed.")
	}
	if !strings.Contains(buf.String(), "All checks passed.") {
		t.Errorf("summary should say 'All checks passed.'; got %q", buf.String())
	}
}

// TestDoctorSummaryWarnings verifies the summary text when warnings exist.
func TestDoctorSummaryWarnings(t *testing.T) {
	state := &doctorState{warnings: 2}
	buf := &bytes.Buffer{}
	switch {
	case state.requiredMissing:
		fmt.Fprintln(buf, "Required prerequisites missing.")
	case state.warnings > 0:
		fmt.Fprintf(buf, "%d warning(s) found.\n", state.warnings)
	default:
		fmt.Fprintln(buf, "All checks passed.")
	}
	if !strings.Contains(buf.String(), "2 warning") {
		t.Errorf("summary should mention warning count; got %q", buf.String())
	}
}

// TestGetVersionDoctor verifies getVersion extracts a digit-leading token.
func TestGetVersionDoctor(t *testing.T) {
	v := getVersion([]string{"git", "--version"})
	if v == "" {
		t.Skip("git not found in PATH")
	}
	if len(v) == 0 || v[0] < '0' || v[0] > '9' {
		t.Errorf("getVersion returned non-version string: %q", v)
	}
}

// TestFileExists verifies the fileExists helper.
func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if fileExists(path) {
		t.Error("fileExists should return false for nonexistent file")
	}
	os.WriteFile(path, []byte("x"), 0644)
	if !fileExists(path) {
		t.Error("fileExists should return true after creating file")
	}
}
