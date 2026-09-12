package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// errRefusedByBd stands in for bd declining a write (e.g. "issue not
// claimable"), which is the shape the half-update edge is about.
var errRefusedByBd = errors.New("bd update failed: issue not claimable")

// startRecordingIPC is startFakeIPC plus a record of which ACTIONS reached
// the socket, so a test can assert what did and did not happen on the TUI
// side of a command.
func startRecordingIPC(t *testing.T, actions *[]string) string {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-rec-%d-%d.sock", os.Getpid(), n))
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })

	var mu sync.Mutex
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req tui.IPCRequest
			dec := json.NewDecoder(conn)
			if err := dec.Decode(&req); err == nil {
				mu.Lock()
				*actions = append(*actions, req.Action)
				mu.Unlock()
			}
			data, _ := json.Marshal(tui.IPCResponse{OK: true})
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()
	return sockPath
}

// deliver_lifecycle_test.go reproduces GitHub #32/#33/#34 at the COMMAND
// level (ini-1fb9). The transition table has its own exhaustive test in
// internal/lifecycle; these assert that the commands actually read it —
// a table one caller can bypass is how #21's May fix failed to cover #33.

// deliverWriteFromStatus runs deliver and returns the bd write it produced.
func deliverWriteFromStatus(t *testing.T, agent, startStatus, implementer string, args ...string) (beadWrite, string, error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)

	var got beadWrite
	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "bead-title", agent, startStatus, nil
	}
	bdBeadImplementerFn = func(id string) (string, error) { return implementer, nil }
	bdUpdateBeadFn = func(id string, w beadWrite) error {
		got = w
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", agent)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"deliver", "ini-test"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return got, stderr.String(), err
}

// GITHUB #32: deliver flipped to ready_for_qa, posted the comment, cleared
// the TUI pointer and notified super — and never touched the assignee, so
// every correct handoff left the bead claimed by the implementer.
func TestRunDeliver_ImplementerHandoffClearsTheAssignee_GH32(t *testing.T) {
	skipWindows(t)
	for _, agent := range []string{"eng1", "shipper"} {
		t.Run(agent, func(t *testing.T) {
			got, _, err := deliverWriteFromStatus(t, agent, "in_progress", "")
			if err != nil {
				t.Fatalf("deliver: %v", err)
			}
			if got.Status != "ready_for_qa" {
				t.Errorf("status = %q, want ready_for_qa", got.Status)
			}
			if !got.SetAssignee || got.Assignee != "" {
				t.Errorf("assignee write = {set:%v %q}, want it CLEARED — a handed-off bead claimed by its implementer is GitHub #32", got.SetAssignee, got.Assignee)
			}
			if got.Implementer != agent {
				t.Errorf("implementer recorded = %q, want %q — this write is the last moment that fact exists on the bead", got.Implementer, agent)
			}
		})
	}
}

// GITHUB #33: for a QA role, --verdict FAIL wrote ready_for_qa — the
// implementer's handoff state, the wrong DIRECTION — and kept the QA agent
// as assignee, which blocked the implementer's re-claim.
func TestRunDeliver_QAFailReturnsTheBeadToItsImplementer_GH33(t *testing.T) {
	skipWindows(t)
	got, _, err := deliverWriteFromStatus(t, "qa3", "in_qa", "eng2", "--verdict", "FAIL", "--reason", "regression found")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got.Status == "ready_for_qa" {
		t.Fatal("a QA FAIL wrote ready_for_qa — the implementer's handoff state, from a QA role (GitHub #33)")
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
	if !got.SetAssignee || got.Assignee != "eng2" {
		t.Errorf("assignee write = {set:%v %q}, want eng2 — the bead goes back to whoever built it", got.SetAssignee, got.Assignee)
	}
}

// The AC's edge: a FAIL on a bead with no recorded implementer leaves it
// unassigned AND says so. Never silently keeps the reviewer.
func TestRunDeliver_QAFailWithNoRecordedImplementerUnassignsAndSaysSo(t *testing.T) {
	skipWindows(t)
	got, stderr, err := deliverWriteFromStatus(t, "qa3", "in_qa", "", "--verdict", "FAIL", "--reason", "regression")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if !got.SetAssignee || got.Assignee != "" {
		t.Errorf("assignee write = {set:%v %q}, want CLEARED", got.SetAssignee, got.Assignee)
	}
	if !strings.Contains(stderr, "records no implementer") {
		t.Errorf("stderr = %q, want it to say the bead goes back unassigned", stderr)
	}
}

// A QA PASS keeps the assignee and advances: the half of the verdict path
// the issues call correct, pinned so this change did not move it.
func TestRunDeliver_QAPassAdvancesAndKeepsTheAssignee(t *testing.T) {
	skipWindows(t)
	got, _, err := deliverWriteFromStatus(t, "qa3", "in_qa", "eng2", "--verdict", "PASS")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got.Status != "qa_passed" {
		t.Errorf("status = %q, want qa_passed", got.Status)
	}
	if got.SetAssignee {
		t.Errorf("PASS rewrote the assignee to %q; it is kept", got.Assignee)
	}
}

// The fallback row, at the command level: super walks a passed bead toward
// closed with repeated deliver calls, and the assignee is not touched.
func TestRunDeliver_WalkToClosedLeavesTheAssigneeAlone(t *testing.T) {
	skipWindows(t)
	got, _, err := deliverWriteFromStatus(t, "eng1", "qa_passed", "")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got.Status != "ready_to_ship" {
		t.Errorf("status = %q, want ready_to_ship", got.Status)
	}
	if got.SetAssignee {
		t.Errorf("the walk rewrote the assignee to %q; it is kept", got.Assignee)
	}
}

// ── assign (GitHub #34) ─────────────────────────────────────────────

// dispatchWriteFor runs assign and returns the bd write it produced.
func dispatchWriteFor(t *testing.T, target string) (status string, recordImplementer bool) {
	t.Helper()
	stubBdFns(t)
	resetAssignFlags(t)
	bdShowTitleFn = func(id string) (string, error) { return "Some bead", nil }
	bdDispatchFn = func(id, agent, st string, record bool) error {
		status, recordImplementer = st, record
		return nil
	}
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", target, "ini-abc"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("assign: %v", err)
	}
	return status, recordImplementer
}

// GITHUB #34: assign wrote in_progress whatever the target's role, so a
// validator dispatch misreported the bead from dispatch until the validator
// self-flipped — and a session reset inside that window left a validator
// holding an in_progress bead.
func TestRunAssign_ValidatorDispatchWritesInQA_GH34(t *testing.T) {
	skipWindows(t)
	status, record := dispatchWriteFor(t, "qa1")
	if status != "in_qa" {
		t.Errorf("assign qa1 wrote %q, want in_qa (GitHub #34)", status)
	}
	if record {
		t.Error("a validator dispatch must not record the validator as the bead's implementer")
	}
}

func TestRunAssign_ImplementerDispatchWritesInProgressAndRecordsThem(t *testing.T) {
	skipWindows(t)
	for _, target := range []string{"eng1", "shipper"} {
		t.Run(target, func(t *testing.T) {
			status, record := dispatchWriteFor(t, target)
			if status != "in_progress" {
				t.Errorf("assign %s wrote %q, want in_progress", target, status)
			}
			if !record {
				t.Errorf("assign %s did not record the implementer; a later QA FAIL has nobody to hand the bead back to", target)
			}
		})
	}
}

// The AC's half-update edge: a bd write refused by bd's own rules must not
// leave the bead half-dispatched — the TUI pointer set with the bd state
// unchanged. bd first, TUI second, and a refusal skips the bead entirely.
func TestRunAssign_ARefusedWriteNeverReachesTheTUIPointer(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	bdShowTitleFn = func(id string) (string, error) { return "Some bead", nil }
	bdDispatchFn = func(id, agent, st string, record bool) error {
		return errRefusedByBd
	}
	var actions []string
	sockPath := startRecordingIPC(t, &actions)
	t.Setenv("INITECH_SOCKET", sockPath)
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "qa1", "ini-abc"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("assign reported success though every bd write was refused")
	}
	for _, a := range actions {
		if a == "bead" {
			t.Error("the TUI bead pointer was set for a bead bd refused to update — a half-dispatched bead")
		}
	}
}
