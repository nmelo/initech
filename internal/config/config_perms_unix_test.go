//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWrite_FreshFile_HasMode0600 is the happy path: a brand new yaml file
// created by Write must have mode 0600. This pins the contract that
// auth-token-bearing files are owner-only readable.
func TestWrite_FreshFile_HasMode0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")

	p := &Project{Name: "perms-fresh", Root: dir, Roles: []string{"eng1"}}
	if err := Write(path, p); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("Mode().Perm() = %#o, want 0o600", got)
	}
}

// TestWrite_ExistingFile_TightensMode is the ini-45h regression check:
// a yaml file that pre-existed at 0644 (e.g., created by an older
// initech version) must be tightened to 0600 when Write rewrites it.
// Without the explicit Chmod after os.WriteFile, this fails — perm is
// the create-mode and is ignored on subsequent writes to an existing file.
func TestWrite_ExistingFile_TightensMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")

	// Simulate an older binary's output by pre-creating at 0644.
	if err := os.WriteFile(path, []byte("placeholder: true\n"), 0o644); err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("pre-chmod 0644: %v", err)
	}

	p := &Project{Name: "perms-existing", Root: dir, Roles: []string{"eng1"}}
	if err := Write(path, p); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("Mode().Perm() = %#o, want 0o600 (existing-file regression)", got)
	}
}
