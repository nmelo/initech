package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestRunAdd_Success(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})

	addBead = ""
	defer func() { addBead = "" }()

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"add", "intern"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Added intern") {
		t.Errorf("stdout = %q, want 'Added intern'", stdout.String())
	}
}

func TestRunAdd_IPCError(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "workspace not found"})

	addBead = ""
	defer func() { addBead = "" }()

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"add", "nope"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when IPC returns error")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want 'workspace not found'", err.Error())
	}
}

func TestRunAdd_NoSocket(t *testing.T) {
	skipWindows(t)
	// Chdir to empty temp dir — no initech.yaml, discoverSocket fails.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	addBead = ""
	defer func() { addBead = "" }()

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"add", "eng1"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no project found")
	}
}

func TestRunAdd_IPCErrorWithHint(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: false, Error: "workspace not found"})

	addBead = ""
	defer func() { addBead = "" }()

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"add", "eng1"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(stderr.String(), "add-agent") {
		t.Errorf("stderr should contain add-agent hint, got: %q", stderr.String())
	}
}

func TestAddAgentHint_KnownRole(t *testing.T) {
	hint := addAgentHint("eng1", "/nonexistent/path")
	if hint == "" {
		t.Error("expected hint for known role with missing directory")
	}
	if !strings.Contains(hint, "add-agent") {
		t.Errorf("hint = %q, want 'add-agent' suggestion", hint)
	}
}

func TestAddAgentHint_UnknownRole(t *testing.T) {
	hint := addAgentHint("not-a-real-role", "/some/path")
	if hint != "" {
		t.Errorf("expected empty hint for unknown role, got %q", hint)
	}
}

func TestAddAgentHint_DirExists(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	hint := addAgentHint("eng1", dir)
	if hint != "" {
		t.Errorf("expected empty hint when directory exists, got %q", hint)
	}
}

func TestAddAgentHint_EmptyRoot(t *testing.T) {
	hint := addAgentHint("eng1", "")
	if hint == "" {
		t.Error("expected hint when projRoot is empty (catalog check only)")
	}
}

func TestRunAddPrintsShortTileSize(t *testing.T) {
	skipWindows(t)
	const notice = "a 1-row tile; its terminal is 40 rows"
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true, Data: notice})
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"add", "intern"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "Added intern: "+notice+".\n" {
		t.Fatalf("add output %q", got)
	}
}

func TestRunAddUnusableTerminalExitsOne(t *testing.T) {
	skipWindows(t)
	const why = "could not size the terminal for eng12: got 47x1"
	fakeProjectWithIPC(t, tui.IPCResponse{Error: why})
	if err := os.Mkdir("eng12", 0755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"add", "eng12"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var code int
	defer stubExitRoot(t, func(c int) { code = c })()
	restore, stderr := captureStderr(t)
	Execute()
	restore()
	if code != 1 || stdout.Len() != 0 || stderr.String() != why+"\n" {
		t.Fatalf("exit %d; stdout %q; stderr %q", code, stdout.String(), stderr.String())
	}
}
