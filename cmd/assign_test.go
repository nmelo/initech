package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 80, "short"},
		{"exactly 10", 10, "exactly 10"},
		{"this is a very long title that exceeds the limit", 20, "this is a very lo..."},
		{"", 80, ""},
	}
	for _, tc := range tests {
		got := truncateTitle(tc.input, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncateTitle(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
		}
	}
}

func TestBdShowTitle_ParsesJSON(t *testing.T) {
	// Create a fake bd script that outputs JSON.
	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
echo '[{"id":"ini-abc","title":"Fix the bug","status":"open"}]'
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	// Prepend fake bd to PATH.
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	title, err := bdShowTitle("ini-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "Fix the bug" {
		t.Errorf("title = %q, want %q", title, "Fix the bug")
	}
}

func TestBdShowTitle_NotFound(t *testing.T) {
	// Create a fake bd that fails.
	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
echo "error: bead not found" >&2
exit 1
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	_, err := bdShowTitle("ini-nonexistent")
	if err == nil {
		t.Fatal("expected error for missing bead")
	}
}

func TestAssignCommand_RequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"assign"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}
