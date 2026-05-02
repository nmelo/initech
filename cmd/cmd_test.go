package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
)

// ── Command registration ────────────────────────────────────────────

func TestAllCommandsRegistered(t *testing.T) {
	// Every subcommand should be registered on rootCmd.
	want := map[string]bool{
		"version": true, "init": true, "doctor": true,
		"send": true, "peek": true, "status": true,
		"stop": true, "start": true, "restart": true,
		"down": true, "standup": true, "patrol": true,
		"bead": true, "add": true, "remove": true,
	}

	for _, cmd := range rootCmd.Commands() {
		name := cmd.Name()
		if want[name] {
			delete(want, name)
		}
	}

	for name := range want {
		t.Errorf("command %q not registered on rootCmd", name)
	}
}

func TestRootCommandHasFlags(t *testing.T) {
	flags := []string{"reset-layout", "verbose", "auto-suspend", "no-color"}
	for _, name := range flags {
		if rootCmd.Flags().Lookup(name) == nil && rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("flag --%s not found on rootCmd", name)
		}
	}
}

// ── Flag parsing ────────────────────────────────────────────────────

func TestSendCommandFlags(t *testing.T) {
	f := sendCmd.Flags().Lookup("no-enter")
	if f == nil {
		t.Fatal("send --no-enter flag not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-enter default = %q, want false", f.DefValue)
	}
}

func TestPeekCommandFlags(t *testing.T) {
	f := peekCmd.Flags().Lookup("lines")
	if f == nil {
		t.Fatal("peek --lines flag not registered")
	}
	if f.Shorthand != "n" {
		t.Errorf("--lines shorthand = %q, want n", f.Shorthand)
	}
	if f.DefValue != "0" {
		t.Errorf("--lines default = %q, want 0", f.DefValue)
	}
}

func TestPatrolCommandFlags(t *testing.T) {
	flags := map[string]string{
		"lines":  "20",
		"active": "false",
		"agent":  "[]",
	}
	for name, wantDef := range flags {
		f := patrolCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("patrol --%s flag not registered", name)
			continue
		}
		if f.DefValue != wantDef {
			t.Errorf("patrol --%s default = %q, want %q", name, f.DefValue, wantDef)
		}
	}
}

func TestBeadCommandFlags(t *testing.T) {
	for _, name := range []string{"agent", "clear"} {
		if beadCmd.Flags().Lookup(name) == nil {
			t.Errorf("bead --%s flag not registered", name)
		}
	}
}

func TestAddCommandFlags(t *testing.T) {
	if addCmd.Flags().Lookup("bead") == nil {
		t.Error("add --bead flag not registered")
	}
}

func TestStartCommandFlags(t *testing.T) {
	if startCmd.Flags().Lookup("bead") == nil {
		t.Error("start --bead flag not registered")
	}
}

func TestRestartCommandFlags(t *testing.T) {
	if restartCmd.Flags().Lookup("bead") == nil {
		t.Error("restart --bead flag not registered")
	}
}

// ── Args validation ─────────────────────────────────────────────────

func TestSendRequiresMinArgs(t *testing.T) {
	if sendCmd.Args == nil {
		t.Fatal("send should have Args validator")
	}
	if err := sendCmd.Args(sendCmd, []string{"eng1"}); err == nil {
		t.Error("send with 1 arg should fail (needs role + text)")
	}
	if err := sendCmd.Args(sendCmd, []string{"eng1", "hello"}); err != nil {
		t.Errorf("send with 2 args should pass: %v", err)
	}
}

func TestPeekRequiresExactArgs(t *testing.T) {
	if err := peekCmd.Args(peekCmd, nil); err == nil {
		t.Error("peek with 0 args should fail")
	}
	if err := peekCmd.Args(peekCmd, []string{"eng1"}); err != nil {
		t.Errorf("peek with 1 arg should pass: %v", err)
	}
	if err := peekCmd.Args(peekCmd, []string{"eng1", "extra"}); err == nil {
		t.Error("peek with 2 args should fail")
	}
}

func TestStopRequiresMinArgs(t *testing.T) {
	if err := stopCmd.Args(stopCmd, nil); err == nil {
		t.Error("stop with 0 args should fail")
	}
	if err := stopCmd.Args(stopCmd, []string{"eng1"}); err != nil {
		t.Errorf("stop with 1 arg should pass: %v", err)
	}
	if err := stopCmd.Args(stopCmd, []string{"eng1", "eng2"}); err != nil {
		t.Errorf("stop with 2 args should pass: %v", err)
	}
}

func TestStartRequiresMinArgs(t *testing.T) {
	if err := startCmd.Args(startCmd, nil); err == nil {
		t.Error("start with 0 args should fail")
	}
}

func TestRestartRequiresExactArgs(t *testing.T) {
	if err := restartCmd.Args(restartCmd, nil); err == nil {
		t.Error("restart with 0 args should fail")
	}
	if err := restartCmd.Args(restartCmd, []string{"eng1", "eng2"}); err == nil {
		t.Error("restart with 2 args should fail")
	}
}

func TestAddRequiresExactArgs(t *testing.T) {
	if err := addCmd.Args(addCmd, nil); err == nil {
		t.Error("add with 0 args should fail")
	}
	if err := addCmd.Args(addCmd, []string{"eng3"}); err != nil {
		t.Errorf("add with 1 arg should pass: %v", err)
	}
}

func TestRemoveRequiresExactArgs(t *testing.T) {
	if err := removeCmd.Args(removeCmd, nil); err == nil {
		t.Error("remove with 0 args should fail")
	}
}

// ── runBead error paths ─────────────────────────────────────────────

func TestRunBead_NoAgentNoEnv(t *testing.T) {
	os.Unsetenv("INITECH_AGENT")
	beadAgent = ""
	err := runBead(beadCmd, []string{"ini-abc.1"})
	if err == nil {
		t.Fatal("should fail with no agent specified")
	}
	if got := err.Error(); got != "no agent specified (set --agent or run inside a TUI pane where INITECH_AGENT is set)" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestRunBead_NoClearNoArgs(t *testing.T) {
	beadAgent = "eng1"
	beadClear = false
	err := runBead(beadCmd, nil)
	if err == nil {
		t.Fatal("should fail with no bead ID and no --clear")
	}
	if got := err.Error(); got != "bead ID required (or use --clear)" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestRunBead_AgentFromEnv(t *testing.T) {
	os.Setenv("INITECH_AGENT", "eng2")
	defer os.Unsetenv("INITECH_AGENT")
	// Disconnect from any running TUI to prevent the test from sending a real
	// IPC bead command (ini-6hz: this was the root cause of the ghost bead).
	origSocket := os.Getenv("INITECH_SOCKET")
	os.Setenv("INITECH_SOCKET", "/tmp/initech-test-nonexistent.sock")
	defer os.Setenv("INITECH_SOCKET", origSocket)
	beadAgent = ""
	beadClear = false
	// This will fail at ipcCall (socket doesn't exist) but should get past the agent check.
	err := runBead(beadCmd, []string{"ini-test.1"})
	if err == nil {
		t.Fatal("should fail with nonexistent socket")
	}
	// If it fails, it should be an IPC error, not an agent error.
	if got := err.Error(); got == "no agent specified (set --agent or run inside a TUI pane where INITECH_AGENT is set)" {
		t.Error("should have resolved agent from INITECH_AGENT env")
	}
}

func TestRunBead_SingleWithTitle(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	bdShowTitleFn = func(id string) (string, error) { return "Fix the bug", nil }
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")
	beadAgent = ""
	beadClear = false
	defer func() { beadAgent = ""; beadClear = false }()

	err := runBead(beadCmd, []string{"ini-abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBead_SingleWithoutTitle(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	bdShowTitleFn = func(id string) (string, error) { return "", fmt.Errorf("bd not found") }
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")
	beadAgent = ""
	beadClear = false
	defer func() { beadAgent = ""; beadClear = false }()

	err := runBead(beadCmd, []string{"ini-abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBead_MultipleBead(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")
	beadAgent = ""
	beadClear = false
	defer func() { beadAgent = ""; beadClear = false }()

	err := runBead(beadCmd, []string{"ini-a", "ini-b", "ini-c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBead_Clear(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")
	beadAgent = ""
	beadClear = true
	defer func() { beadAgent = ""; beadClear = false }()

	err := runBead(beadCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── runStart error paths ────────────────────────────────────────────

func TestRunStart_BeadWithMultipleAgents(t *testing.T) {
	startBead = "ini-abc.1"
	err := runStart(startCmd, []string{"eng1", "eng2"})
	if err == nil {
		t.Fatal("should fail with --bead and multiple agents")
	}
	if got := err.Error(); got != "--bead can only be used when starting a single agent" {
		t.Errorf("unexpected error: %s", got)
	}
	startBead = ""
}

// ── statusColor ─────────────────────────────────────────────────────

func TestStatusColor(t *testing.T) {
	// statusColor should return non-empty strings for all known inputs.
	cases := []string{"running", "in_progress", "qa_passed", "idle", "stopped", "stalled (5m)", "dead", "stuck", "custom"}
	for _, s := range cases {
		got := statusColor(s)
		if got == "" {
			t.Errorf("statusColor(%q) returned empty", s)
		}
	}
}

// ── getBeadAssignments ──────────────────────────────────────────────

func TestGetBeadAssignments_NoBd(t *testing.T) {
	r := &iexec.FakeRunner{Err: fmt.Errorf("not found")}
	result := getBeadAssignments(r)
	if len(result) != 0 {
		t.Error("should return empty map when bd not found")
	}
}

func TestGetBeadAssignments_JSONArray(t *testing.T) {
	beads := []beadInfo{
		{ID: "ini-a.1", Title: "Do stuff", Status: "in_progress", Assignee: "eng1"},
		{ID: "ini-a.2", Title: "Other", Status: "in_qa", Assignee: "qa1"},
		{ID: "ini-a.3", Title: "Closed", Status: "closed", Assignee: "eng2"},
	}
	data, _ := json.Marshal(beads)

	callCount := 0
	r := &fakeMultiRunner{
		responses: []fakeResponse{
			{output: "/usr/local/bin/bd"}, // which bd
			{output: string(data)},        // bd list --status in_progress --json
		},
	}
	_ = callCount

	result := getBeadAssignments(r)

	if _, ok := result["eng1"]; !ok {
		t.Error("eng1 should have a bead (in_progress)")
	}
	if _, ok := result["qa1"]; !ok {
		t.Error("qa1 should have a bead (in_qa)")
	}
	if _, ok := result["eng2"]; ok {
		t.Error("eng2 should not have a bead (closed)")
	}
}

func TestGetBeadAssignments_JSONLines(t *testing.T) {
	lines := `{"id":"ini-b.1","title":"Task","status":"in_progress","assignee":"eng1"}
{"id":"ini-b.2","title":"Task2","status":"in_progress","assignee":"eng2"}`

	r := &fakeMultiRunner{
		responses: []fakeResponse{
			{output: "/usr/local/bin/bd"},
			{output: lines},
		},
	}

	result := getBeadAssignments(r)
	if len(result) != 2 {
		t.Errorf("expected 2 assignments, got %d", len(result))
	}
}

// ── queryBeads ──────────────────────────────────────────────────────

func TestQueryBeads_JSONArray(t *testing.T) {
	beads := []standupBead{
		{ID: "ini-c.1", Title: "Feature"},
		{ID: "ini-c.2", Title: "Bug"},
	}
	data, _ := json.Marshal(beads)
	r := &iexec.FakeRunner{Output: string(data)}

	got := queryBeads(r, "list", "--json")
	if len(got) != 2 {
		t.Fatalf("got %d beads, want 2", len(got))
	}
	if got[0].ID != "ini-c.1" {
		t.Errorf("got[0].ID = %q", got[0].ID)
	}
}

func TestQueryBeads_JSONLines(t *testing.T) {
	lines := `{"id":"ini-d.1","title":"A"}
{"id":"ini-d.2","title":"B"}`
	r := &iexec.FakeRunner{Output: lines}

	got := queryBeads(r, "ready", "--json")
	if len(got) != 2 {
		t.Fatalf("got %d beads, want 2", len(got))
	}
}

func TestQueryBeads_Error(t *testing.T) {
	r := &iexec.FakeRunner{Err: fmt.Errorf("bd not found")}
	got := queryBeads(r, "list")
	if got != nil {
		t.Error("should return nil on error")
	}
}

// ── patrolStatusColor ───────────────────────────────────────────────

func TestPatrolStatusColor(t *testing.T) {
	cases := []string{"running", "stalled (2m)", "dead", "idle", "custom"}
	for _, s := range cases {
		got := patrolStatusColor(s)
		if got == "" {
			t.Errorf("patrolStatusColor(%q) returned empty", s)
		}
	}
}

// ── Version command ─────────────────────────────────────────────────

func TestVersionCommand(t *testing.T) {
	// versionCmd uses fmt.Printf directly, so we verify the command exists
	// and is runnable rather than capturing output.
	if versionCmd.RunE == nil {
		t.Fatal("version command should have RunE")
	}
	err := versionCmd.RunE(versionCmd, nil)
	if err != nil {
		t.Errorf("version RunE returned error: %v", err)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

// fakeMultiRunner returns different responses for sequential calls.
type fakeMultiRunner struct {
	responses []fakeResponse
	idx       int
}

type fakeResponse struct {
	output string
	err    error
}

func (f *fakeMultiRunner) Run(name string, args ...string) (string, error) {
	return f.RunInDir("", name, args...)
}

func (f *fakeMultiRunner) RunInDir(dir, name string, args ...string) (string, error) {
	if f.idx >= len(f.responses) {
		return "", fmt.Errorf("no more fake responses")
	}
	r := f.responses[f.idx]
	f.idx++
	return r.output, r.err
}
