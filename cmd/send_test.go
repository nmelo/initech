package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

var fakeIPCCounter atomic.Int64

// startFakeIPC spins up a Unix socket that returns a canned IPCResponse.
// Uses /tmp to keep socket paths under macOS's 108-char limit.
func startFakeIPC(t *testing.T, resp tui.IPCResponse) string {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-test-%d-%d.sock", os.Getpid(), n))
	os.Remove(sockPath) // clean up any stale socket
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			data, _ := json.Marshal(resp)
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()
	return sockPath
}

func TestSendCommand_LocalDeliveryConfirmation(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "eng2", "hello world"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}
	got := stderr.String()
	if got != "delivered to eng2\n" {
		t.Errorf("stderr = %q, want %q", got, "delivered to eng2\n")
	}
}

func TestSendCommand_RemoteDeliveryConfirmation(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "workbench:intern", "do the thing"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}
	got := stderr.String()
	if got != "delivered to workbench:intern\n" {
		t.Errorf("stderr = %q, want %q", got, "delivered to workbench:intern\n")
	}
}

func TestSendCommand_ErrorPrintsToStderr(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: false, Error: "agent not found"})
	t.Setenv("INITECH_SOCKET", sockPath)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"send", "nobody", "hello"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "agent not found" {
		t.Errorf("got error %q, want %q", err.Error(), "agent not found")
	}
}

func TestSendCommand_NoSocket(t *testing.T) {
	// Point to a non-existent socket and no config to discover.
	t.Setenv("INITECH_SOCKET", "")
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	rootCmd.SetArgs([]string{"send", "eng1", "hello"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no socket available")
	}
}
