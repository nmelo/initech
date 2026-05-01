package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

func TestRunPatrol_PrintsFilteredEntries(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	restorePatrolFlags := setPatrolFlags(t, 7, true, []string{"eng1", "eng3"})
	defer restorePatrolFlags()

	resp := tui.IPCResponse{
		OK: true,
		Data: `[{"name":"eng1","activity":"running","bead":"ini-p.1","alive":true,"content":"line 1\nline 2\n"},` +
			`{"name":"eng2","activity":"idle","alive":true,"content":""},` +
			`{"name":"eng3","activity":"stalled (2m)","bead":"ini-p.3","alive":false,"content":""}]`,
	}
	sockPath, reqCh := startPatrolIPCServer(t, resp)
	t.Setenv("INITECH_SOCKET", sockPath)

	output, err := captureStdout(t, func() error {
		return runPatrol(&cobra.Command{}, nil)
	})
	if err != nil {
		t.Fatalf("runPatrol: %v", err)
	}

	req := waitPatrolRequest(t, reqCh)
	if req.Action != "patrol" {
		t.Fatalf("request action = %q, want patrol", req.Action)
	}
	if req.Lines != 7 {
		t.Fatalf("request lines = %d, want 7", req.Lines)
	}

	for _, want := range []string{
		"=== eng1 (running | ini-p.1) ===",
		"line 1\nline 2",
		"=== eng3 (stalled (2m) | ini-p.3 | dead) ===",
		"[no recent output]",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("patrol output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "eng2") {
		t.Fatalf("idle empty agent should be filtered when --active is set:\n%s", output)
	}
}

func TestRunPatrol_ReturnsIPCError(t *testing.T) {
	skipWindows(t)
	restorePatrolFlags := setPatrolFlags(t, 5, false, nil)
	defer restorePatrolFlags()

	sockPath, _ := startPatrolIPCServer(t, tui.IPCResponse{OK: false, Error: "patrol failed"})
	t.Setenv("INITECH_SOCKET", sockPath)

	err := runPatrol(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("runPatrol should return IPC errors")
	}
	if !strings.Contains(err.Error(), "patrol failed") {
		t.Fatalf("runPatrol error = %v, want patrol failed", err)
	}
}

func TestRunPatrol_ReturnsParseError(t *testing.T) {
	skipWindows(t)
	restorePatrolFlags := setPatrolFlags(t, 5, false, nil)
	defer restorePatrolFlags()

	sockPath, _ := startPatrolIPCServer(t, tui.IPCResponse{OK: true, Data: "{not-json}"})
	t.Setenv("INITECH_SOCKET", sockPath)

	_, err := captureStdout(t, func() error {
		return runPatrol(&cobra.Command{}, nil)
	})
	if err == nil {
		t.Fatal("runPatrol should fail on invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse patrol response") {
		t.Fatalf("runPatrol error = %v, want parse error", err)
	}
}

func setPatrolFlags(t *testing.T, lines int, active bool, agents []string) func() {
	t.Helper()
	oldLines, oldActive, oldAgents := patrolLines, patrolActive, append([]string(nil), patrolAgents...)
	patrolLines = lines
	patrolActive = active
	patrolAgents = append([]string(nil), agents...)
	return func() {
		patrolLines = oldLines
		patrolActive = oldActive
		patrolAgents = oldAgents
	}
}

func startPatrolIPCServer(t *testing.T, resp tui.IPCResponse) (string, <-chan tui.IPCRequest) {
	t.Helper()

	sockPath := fmt.Sprintf("/tmp/initech-patrol-%d.sock", time.Now().UnixNano())
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	reqCh := make(chan tui.IPCRequest, 1)
	errCh := make(chan error, 1)

	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				errCh <- err
			}
			return
		}

		var req tui.IPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			errCh <- err
			return
		}
		reqCh <- req

		data, _ := json.Marshal(resp)
		if _, err := conn.Write(data); err != nil {
			errCh <- err
			return
		}
		_, err = conn.Write([]byte("\n"))
		if err != nil {
			errCh <- err
		}
	}()

	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		select {
		case err := <-errCh:
			if !strings.Contains(err.Error(), "use of closed network connection") {
				t.Fatalf("patrol IPC server error: %v", err)
			}
		default:
		}
	})

	return sockPath, reqCh
}

func waitPatrolRequest(t *testing.T, reqCh <-chan tui.IPCRequest) tui.IPCRequest {
	t.Helper()
	select {
	case req := <-reqCh:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for patrol IPC request")
	}
	return tui.IPCRequest{}
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	runErr := fn()

	_ = w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom stdout pipe: %v", err)
	}
	_ = r.Close()

	return buf.String(), runErr
}
