package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// downFake is a socket that answers each action with its own reply and
// records the actions in the order they arrived. An action with no reply
// gets its connection closed unanswered.
func downFake(t *testing.T, replies map[string]tui.IPCResponse) (string, func() []string) {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-down-%d-%d.sock", os.Getpid(), n))
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })
	var mu sync.Mutex
	var actions []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			sc := bufio.NewScanner(conn)
			if sc.Scan() {
				var req tui.IPCRequest
				json.Unmarshal(sc.Bytes(), &req)
				mu.Lock()
				actions = append(actions, req.Action)
				mu.Unlock()
				if resp, ok := replies[req.Action]; ok {
					data, _ := json.Marshal(resp)
					conn.Write(append(data, '\n'))
				}
			}
			conn.Close()
		}
	}()
	return sockPath, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), actions...)
	}
}

func runDownCapture(t *testing.T) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"down"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return stdout.String(), err
}

// Run from a folder of project testproj with INITECH_SOCKET naming another
// rig's socket: the line names the rig that stopped, not the folder.
func TestRunDown_NamesTheSocketsProjectNotTheFolders(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true}) // cwd = project testproj
	sock, actions := downFake(t, map[string]tui.IPCResponse{
		"list": {OK: true, Data: "[]", Project: &tui.IPCProject{Name: "otherrig"}},
		"quit": {OK: true},
	})
	t.Setenv("INITECH_SOCKET", sock)

	out, err := runDownCapture(t)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if out != "Session 'otherrig' stopped.\n" {
		t.Errorf("stdout = %q, want Session 'otherrig' stopped.", out)
	}
	if got := actions(); strings.Join(got, ",") != "list,quit" {
		t.Errorf("actions = %v, want [list quit]", got)
	}
}

// An older server names no project: through INITECH_SOCKET the line names
// the socket it stopped, never the caller's folder or a placeholder.
func TestRunDown_OlderServerThroughSocketPrintsTheSocketPath(t *testing.T) {
	skipWindows(t)
	for name, chdir := range map[string]bool{"from another project": true, "from no project": false} {
		t.Run(name, func(t *testing.T) {
			if chdir {
				fakeProjectWithIPC(t, tui.IPCResponse{OK: true})
			} else {
				orig, _ := os.Getwd()
				os.Chdir(t.TempDir())
				t.Cleanup(func() { os.Chdir(orig) })
			}
			sock, _ := downFake(t, map[string]tui.IPCResponse{
				"list": {OK: true, Data: "[]"},
				"quit": {OK: true},
			})
			t.Setenv("INITECH_SOCKET", sock)

			out, err := runDownCapture(t)
			if err != nil {
				t.Fatalf("down: %v", err)
			}
			if out != "Session '"+sock+"' stopped.\n" {
				t.Errorf("stdout = %q, want the socket path %s", out, sock)
			}
		})
	}
}

// The ordinary case: no INITECH_SOCKET, an older server. The project found
// from the folder is the one whose socket was stopped, so its name stands.
func TestRunDown_OwnSocketOlderServerKeepsTheProjectName(t *testing.T) {
	skipWindows(t)
	t.Setenv("INITECH_SOCKET", "")
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})

	out, err := runDownCapture(t)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if out != "Session 'testproj' stopped.\n" {
		t.Errorf("stdout = %q, want Session 'testproj' stopped.", out)
	}
}

// The name is only a label: a list that fails never stops the quit.
func TestRunDown_ListFailureStillQuits(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})
	sock, actions := downFake(t, map[string]tui.IPCResponse{
		"list": {OK: false, Error: "busy", Project: &tui.IPCProject{Name: "notthis"}},
		"quit": {OK: true},
	})
	t.Setenv("INITECH_SOCKET", sock)

	out, err := runDownCapture(t)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if out != "Session '"+sock+"' stopped.\n" {
		t.Errorf("stdout = %q, want the socket path", out)
	}
	if got := actions(); strings.Join(got, ",") != "list,quit" {
		t.Errorf("actions = %v, want [list quit]", got)
	}
}

// A quit that gets no reply is an error, and no "stopped" line is printed.
func TestRunDown_QuitWithoutReplyIsAnError(t *testing.T) {
	skipWindows(t)
	fakeProjectWithIPC(t, tui.IPCResponse{OK: true})
	sock, _ := downFake(t, map[string]tui.IPCResponse{
		"list": {OK: true, Data: "[]", Project: &tui.IPCProject{Name: "otherrig"}},
	})
	t.Setenv("INITECH_SOCKET", sock)

	out, err := runDownCapture(t)
	if err == nil || !strings.Contains(err.Error(), "send quit") {
		t.Fatalf("err = %v, want a send quit error", err)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("stdout = %q, want no stopped line", out)
	}
}
