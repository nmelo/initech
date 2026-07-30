package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestSendCommand_ModalDeferredFeedback verifies the sender is told when a send
// was deferred because the target had a modal open (ini-7txh), instead of a
// misleading "delivered" message.
func TestSendCommand_ModalDeferredFeedback(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true, Data: "deferred (modal open)"})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "growth", "coordinate please"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "deferred") || !strings.Contains(got, "growth") {
		t.Errorf("stderr = %q, want a deferred-modal message mentioning the target", got)
	}
	if strings.Contains(got, "delivered to growth\n") {
		t.Errorf("stderr = %q, must not claim plain delivery when deferred", got)
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

// TestSendCommand_HelpDocumentsBareNameAutoResolve locks the send help text to
// the routing behavior it describes. GitHub #15 asked for bare-name peer
// addressing, which findPaneByName has always supported (internal/tui/
// input_core.go: exact paneKey match, then a bare Name() fallback), and
// handleIPCSend delivers through PaneView.SendText for local and remote panes
// alike. The feature only *looked* unimplemented because this help text said
// "For cross-machine addressing, use host:agent format" and never mentioned
// that a bare name resolves on its own. These assertions keep the text from
// regressing to prefix-required, and keep the duplicate-name caveat documented.
func TestSendCommand_HelpDocumentsBareNameAutoResolve(t *testing.T) {
	long := sendCmd.Long

	// The bare-name path must be documented as needing no host prefix.
	for _, want := range []string{
		"no host prefix required",
		"local or remote",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("send help does not document bare-name auto-resolve: missing %q", want)
		}
	}

	// The one real sharp edge: duplicate agent names across peers resolve to the
	// first matching pane, so the qualified form is how you target the other one.
	for _, want := range []string{
		"more than one machine",
		"first matching pane",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("send help does not document the duplicate-name caveat: missing %q", want)
		}
	}

	// Guard against the original wording, which implied the prefix was mandatory.
	if strings.Contains(long, "For cross-machine addressing, use host:agent format") {
		t.Error("send help still implies host:agent is required for cross-machine addressing")
	}

	// The usage line must keep host optional.
	if !strings.Contains(sendCmd.Use, "[host:]") {
		t.Errorf("send usage should mark host optional, got %q", sendCmd.Use)
	}
}

// capturedIPC records every IPCRequest a fake server receives so tests can
// assert on the exact bytes delivered (ini-da7f: backtick content must arrive
// byte-identical through the shell-safe input paths).
type capturedIPC struct {
	mu   sync.Mutex
	reqs []tui.IPCRequest
}

func (c *capturedIPC) last(t *testing.T) tui.IPCRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		t.Fatal("no IPC request captured")
	}
	return c.reqs[len(c.reqs)-1]
}

// startCapturingIPC is startFakeIPC plus request capture: it decodes each
// incoming request line before replying with the canned response.
func startCapturingIPC(t *testing.T, resp tui.IPCResponse) (string, *capturedIPC) {
	t.Helper()
	captured := &capturedIPC{}
	n := fakeIPCCounter.Add(1)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-test-cap-%d-%d.sock", os.Getpid(), n))
	os.Remove(sockPath)
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
			scanner := tui.NewIPCScanner(conn)
			if scanner.Scan() {
				var req tui.IPCRequest
				if json.Unmarshal(scanner.Bytes(), &req) == nil {
					captured.mu.Lock()
					captured.reqs = append(captured.reqs, req)
					captured.mu.Unlock()
				}
			}
			data, _ := json.Marshal(resp)
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()
	return sockPath, captured
}

// resetSendBodyFlags restores the package-level send flag state that cobra
// leaves behind after Execute, so later tests see pristine defaults.
func resetSendBodyFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		sendStdin = false
		sendFile = ""
		sendNoEnter = false
		rootCmd.SetIn(nil)
		// Cobra's built-in help flag persists across Execute calls; a test that
		// ran `send --help` would otherwise short-circuit every later send test.
		if f := sendCmd.Flags().Lookup("help"); f != nil {
			f.Value.Set("false")
			f.Changed = false
		}
	})
}

func TestSendCommand_StdinBodyArrivesVerbatim(t *testing.T) {
	skipWindows(t)
	resetSendBodyFlags(t)
	sockPath, captured := startCapturingIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	body := "use `if: false` to disable the job\nand `make check` before `git push`\n"
	rootCmd.SetIn(strings.NewReader(body))

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "eng2", "--stdin"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := captured.last(t)
	if req.Action != "send" || req.Target != "eng2" {
		t.Errorf("request = %+v, want action=send target=eng2", req)
	}
	if req.Text != body {
		t.Errorf("delivered body = %q, want byte-identical %q", req.Text, body)
	}
	if !req.Enter {
		t.Error("Enter = false, want true (default submit behavior unchanged)")
	}
	if got := stderr.String(); got != "delivered to eng2\n" {
		t.Errorf("stderr = %q, want %q", got, "delivered to eng2\n")
	}
}

func TestSendCommand_FileBodyArrivesVerbatim(t *testing.T) {
	skipWindows(t)
	resetSendBodyFlags(t)
	sockPath, captured := startCapturingIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	body := "draft for super:\n`initech deliver` walks one step; `bd close` is operator-only.\n"
	path := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"send", "super", "-f", path})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := captured.last(t)
	if req.Target != "super" {
		t.Errorf("target = %q, want super", req.Target)
	}
	if req.Text != body {
		t.Errorf("delivered body = %q, want byte-identical %q", req.Text, body)
	}
}

func TestSendCommand_FileDashReadsStdin(t *testing.T) {
	skipWindows(t)
	resetSendBodyFlags(t)
	sockPath, captured := startCapturingIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	body := "`-f -` must behave exactly like --stdin\n"
	rootCmd.SetIn(strings.NewReader(body))
	rootCmd.SetArgs([]string{"send", "eng2", "-f", "-"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req := captured.last(t); req.Text != body {
		t.Errorf("delivered body = %q, want byte-identical %q", req.Text, body)
	}
}

func TestSendCommand_StdinRejectsPositionalBody(t *testing.T) {
	skipWindows(t)
	resetSendBodyFlags(t)
	rootCmd.SetIn(strings.NewReader("body\n"))
	rootCmd.SetArgs([]string{"send", "eng2", "stray text", "--stdin"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for positional body combined with --stdin, got nil")
	}
}

func TestSendCommand_StdinAndFileConflict(t *testing.T) {
	skipWindows(t)
	resetSendBodyFlags(t)
	rootCmd.SetArgs([]string{"send", "eng2", "--stdin", "-f", "somefile"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --stdin combined with -f, got nil")
	}
	if !strings.Contains(err.Error(), "--stdin") || !strings.Contains(err.Error(), "--file") {
		t.Errorf("error = %q, want a mutual-exclusion message naming --stdin and --file", err)
	}
}

func TestSendCommand_EmptyStdinBodyRejected(t *testing.T) {
	skipWindows(t)
	resetSendBodyFlags(t)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"send", "eng2", "--stdin"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for empty stdin body, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want an empty-body message", err)
	}
}

func TestSendCommand_HelpDocumentsBacktickHazard(t *testing.T) {
	resetSendBodyFlags(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"send", "--help"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "backtick") {
		t.Errorf("send --help must document the backtick/command-substitution hazard; got:\n%s", help)
	}
	if !strings.Contains(help, "--stdin") {
		t.Errorf("send --help must document the shell-safe --stdin path; got:\n%s", help)
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
