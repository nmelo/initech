package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestRunStart_Success(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: "started"})

	startBead = ""
	defer func() { startBead = "" }()

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"start", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Started eng1") {
		t.Errorf("stdout = %q, want 'Started eng1'", stdout.String())
	}
}

func TestRunStart_AlreadyRunning(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: "already running"})

	startBead = ""
	defer func() { startBead = "" }()

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"start", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "already running") {
		t.Errorf("stdout = %q, want 'already running'", stdout.String())
	}
}

func TestRunStart_BeadWithMultipleAgentsError(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"start", "eng1", "eng2", "--bead", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --bead with multiple agents")
	}
	if !strings.Contains(err.Error(), "single agent") {
		t.Errorf("error = %q, want 'single agent' constraint", err.Error())
	}
}

func TestRunStart_IPCError(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "unknown agent"})

	startBead = ""
	defer func() { startBead = "" }()

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"start", "nobody"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "unknown agent") {
		t.Errorf("stdout = %q, want warning about unknown agent", stdout.String())
	}
}

func TestRunStart_MultipleAgents(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: "started"})

	startBead = ""
	defer func() { startBead = "" }()

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"start", "eng1", "eng2"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(stdout.String(), "Started") != 2 {
		t.Errorf("expected 2 'Started' messages, got: %q", stdout.String())
	}
}
