package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestRunStop_Success(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: "stopped"})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"stop", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Stopped eng1") {
		t.Errorf("stdout = %q, want 'Stopped eng1'", stdout.String())
	}
}

func TestRunStop_AlreadyStopped(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: "already stopped"})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"stop", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "already stopped") {
		t.Errorf("stdout = %q, want 'already stopped'", stdout.String())
	}
}

func TestRunStop_MultipleAgents(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: "stopped"})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"stop", "eng1", "eng2", "eng3"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(stdout.String(), "Stopped") != 3 {
		t.Errorf("expected 3 'Stopped' messages, got: %q", stdout.String())
	}
}

func TestRunStop_IPCError(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "agent not found"})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"stop", "nobody"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "agent not found") {
		t.Errorf("stdout = %q, want warning about agent not found", stdout.String())
	}
}
