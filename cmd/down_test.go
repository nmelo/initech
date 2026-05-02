package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestRunDown_Success(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"down"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "stopped") {
		t.Errorf("stdout = %q, want 'stopped' confirmation", stdout.String())
	}
}

func TestRunDown_IPCError(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "session locked"})

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"down"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when IPC returns error")
	}
	if !strings.Contains(err.Error(), "session locked") {
		t.Errorf("error = %q, want 'session locked'", err.Error())
	}
}

func TestRunDown_NoSocket(t *testing.T) {
	skipWindows(t)
	// Chdir to empty temp dir — no initech.yaml, discoverSocket fails.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"down"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no project found")
	}
}
