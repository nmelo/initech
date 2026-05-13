//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorProject_PermissionWarn_Looseyamlmode asserts the doctor's new
// permission check (ini-45h): when initech.yaml is mode 0644, a "Config
// perms" WARN is emitted with the chmod fix in the detail.
func TestDoctorProject_PermissionWarn_LooseYamlMode(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: permtest\nroot: %s\nwebhook_url: https://hooks.slack.com/x\nroles:\n  - super\n", dir)
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod 0644: %v", err)
	}

	// Minimal scaffold so the rest of runProjectChecks has nothing else
	// to flag (we want to assert the perm WARN is what's emitted).
	if err := os.MkdirAll(filepath.Join(dir, "super"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "super", "CLAUDE.md"), []byte("# super"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}

	checks, _, _ := runProjectChecks(path)

	var permWarn *checkResult
	for i := range checks {
		if checks[i].Label == "Config perms" {
			permWarn = &checks[i]
			break
		}
	}
	if permWarn == nil {
		t.Fatalf("expected 'Config perms' check; got %+v", checks)
	}
	if permWarn.Status != "WARN" {
		t.Errorf("status = %q, want WARN", permWarn.Status)
	}
	if !strings.Contains(permWarn.Detail, "chmod 600") {
		t.Errorf("detail = %q, want it to contain 'chmod 600' so the user can copy-paste the fix", permWarn.Detail)
	}
}

// TestDoctorProject_PermissionWarn_TightYamlMode asserts the doctor stays
// silent on the happy path (0600). The check only surfaces issues — it
// MUST NOT emit a "Config perms OK" line that would clutter every doctor
// run.
func TestDoctorProject_PermissionWarn_TightYamlMode(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: permtest\nroot: %s\nwebhook_url: https://hooks.slack.com/x\nroles:\n  - super\n", dir)
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod 0600: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "super"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "super", "CLAUDE.md"), []byte("# super"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}

	checks, _, _ := runProjectChecks(path)

	for _, c := range checks {
		if c.Label == "Config perms" {
			t.Errorf("unexpected 'Config perms' check on 0600 yaml: %+v", c)
		}
	}
}

// TestCheckConfigPermissions_MissingFile_NoCheck asserts that doctor stays
// quiet when initech.yaml doesn't exist (per bead edge case: "Skip the
// check cleanly if initech.yaml doesn't exist — that's a different
// problem"). Direct unit test of checkConfigPermissions so this doesn't
// have to set up the full runProjectChecks fixture.
func TestCheckConfigPermissions_MissingFile_NoCheck(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "initech.yaml")
	checks := checkConfigPermissions(missing)
	if len(checks) != 0 {
		t.Errorf("expected no checks for missing yaml, got %+v", checks)
	}
}

// TestCheckConfigPermissions_Mode0700_NoWarn pins the bitmask choice: the
// check uses 'mode & 0o077 != 0', NOT 'mode > 0o600'. 0o700 is rwx for
// owner — numerically greater than 0o600 but still owner-only readable,
// so it must NOT warn. Catching this distinction protects against a
// future refactor that "simplifies" to the numeric form.
func TestCheckConfigPermissions_Mode0700_NoWarn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte("x: y\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod 0700: %v", err)
	}
	checks := checkConfigPermissions(path)
	if len(checks) != 0 {
		t.Errorf("0700 must not warn (owner-only), got %+v", checks)
	}
}
