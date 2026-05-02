package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ── peek ────────────────────────────────────────────────────────────

func TestRunPeek_Success(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true, Data: "line1\nline2\n"})
	t.Setenv("INITECH_SOCKET", sockPath)

	// runPeek uses fmt.Print (real stdout), not cmd.OutOrStdout().
	// We just verify it doesn't error.
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"peek", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPeek_Error(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: false, Error: "agent not found"})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"peek", "nobody"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("error = %q, want 'agent not found'", err.Error())
	}
}

// ── interrupt ───────────────────────────────────────────────────────

func TestRunInterrupt_Success(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	interruptHard = false
	defer func() { interruptHard = false }()

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"interrupt", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "interrupted eng1") {
		t.Errorf("stderr = %q, want 'interrupted eng1'", stderr.String())
	}
}

func TestRunInterrupt_Hard(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"interrupt", "eng1", "--hard"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunInterrupt_CrossMachine(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	interruptHard = false
	defer func() { interruptHard = false }()

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"interrupt", "workbench:eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "workbench:eng1") {
		t.Errorf("stderr = %q, want cross-machine format", stderr.String())
	}
}

// ── restart ─────────────────────────────────────────────────────────

func TestRunRestart_Success(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})

	restartBead = ""
	defer func() { restartBead = "" }()

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"restart", "eng1"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Restarted eng1") {
		t.Errorf("stdout = %q, want 'Restarted eng1'", stdout.String())
	}
}

func TestRunRestart_Error(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "agent not found"})

	restartBead = ""
	defer func() { restartBead = "" }()

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"restart", "nobody"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── remove ──────────────────────────────────────────────────────────

func TestRunRemove_Success(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"remove", "intern"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed intern") {
		t.Errorf("stdout = %q, want 'Removed intern'", stdout.String())
	}
}

func TestRunRemove_Error(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "pane not found"})

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"remove", "nobody"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── peers ───────────────────────────────────────────────────────────

func TestRunPeers_Success(t *testing.T) {
	skipWindows(t)
	peers := []tui.PeerInfo{
		{Name: "local", Agents: []string{"eng1", "eng2"}},
		{Name: "workbench", Agents: []string{"intern"}},
	}
	data, _ := json.Marshal(peers)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: string(data)})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"peers"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "workbench") {
		t.Errorf("stdout = %q, want peer listing", stdout.String())
	}
}

// ── listPanes ───────────────────────────────────────────────────────

func TestListPanes_Success(t *testing.T) {
	skipWindows(t)
	panes := []tui.PaneInfo{
		{Name: "eng1", Activity: "running", Alive: true, Visible: true},
		{Name: "eng2", Activity: "idle", Alive: true, Visible: false},
	}
	data, _ := json.Marshal(panes)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true, Data: string(data)})
	t.Setenv("INITECH_SOCKET", sockPath)

	got, err := listPanes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d panes, want 2", len(got))
	}
}

func TestListPanes_Error(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: false, Error: "no session"})
	t.Setenv("INITECH_SOCKET", sockPath)

	_, err := listPanes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunPeers_NoPeers(t *testing.T) {
	skipWindows(t)
	data, _ := json.Marshal([]tui.PeerInfo{})
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: string(data)})

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"peers"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No peers") {
		t.Errorf("stdout = %q, want 'No peers'", stdout.String())
	}
}
