package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsNewer_Update(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"2.0.0", "1.0.0", true},
		{"1.1.0", "1.0.0", true},
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"v2.0.0", "v1.0.0", true},
		{"0.24.0", "0.23.28", true},
	}
	for _, tc := range tests {
		t.Run(tc.latest+"_vs_"+tc.current, func(t *testing.T) {
			if got := isNewer(tc.latest, tc.current); got != tc.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
			}
		})
	}
}

func TestIsHomebrewInstall(t *testing.T) {
	// This test checks the current binary's path. In test environments
	// it should NOT be under a Homebrew prefix.
	if isHomebrewInstall() {
		t.Skip("running under Homebrew, expected non-Homebrew test env")
	}
}

func TestLiveSession_NoPIDFile(t *testing.T) {
	// From a temp directory with no initech.yaml, liveSession returns false.
	dir := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)

	_, running := liveSession()
	if running {
		t.Error("liveSession should return false with no PID file")
	}
}

func TestLiveSession_StalePID(t *testing.T) {
	dir := t.TempDir()
	// Create a minimal initech.yaml so Discover finds it.
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte("project: test\nroot: "+dir+"\nroles:\n  - a\n"), 0644)
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	// Write a PID that doesn't exist.
	os.WriteFile(filepath.Join(initechDir, "initech.pid"), []byte("99999999\n"), 0644)

	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)

	_, running := liveSession()
	if running {
		t.Error("liveSession should return false for dead PID")
	}
}

func TestUpdateCommandRegistered(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "update" {
			found = true
			break
		}
	}
	if !found {
		t.Error("update command not registered")
	}
}

func TestUpdateCommandFlags(t *testing.T) {
	if updateCmd.Flags().Lookup("check") == nil {
		t.Error("--check flag not registered")
	}
	if updateCmd.Flags().Lookup("force") == nil {
		t.Error("--force flag not registered")
	}
}
