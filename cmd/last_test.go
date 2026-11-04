package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// lastFixture is the recorded Claude Code session the app contract test
// uses: all four kinds, a thinking block and a /clear echo left out, and one
// line cut mid-write (malformed, counted).
const lastFixture = "testdata/appcontract/session.jsonl"

// lastCLI runs initech last against a fake TUI whose pane list names panes,
// and returns stdout, stderr and the error.
func lastCLI(t *testing.T, panes []map[string]any, args ...string) (string, string, error) {
	t.Helper()
	skipWindows(t)
	data, err := json.Marshal(panes)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INITECH_SOCKET", startFakeIPC(t, tui.IPCResponse{OK: true, Data: string(data)}))
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"last"}, args...))
	defer rootCmd.SetArgs(nil)
	lastLines = 20
	err = rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

// hhmmss is a fixture time (UTC, 2026-10-31) as last prints it: local time.
func hhmmss(utc string) string {
	at, err := time.Parse(time.RFC3339, "2026-10-31T"+utc+"Z")
	if err != nil {
		panic(err)
	}
	return at.Local().Format("15:04:05")
}

// headers counts the entry header lines last printed.
func headers(out string) int {
	return len(regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d (agent|nelson|tool|message)( from \S+)?$`).FindAllString(out, -1))
}

func pane(name, sessionFile string) map[string]any {
	return map[string]any{"name": name, "alive": true, "session_file": sessionFile}
}

func absFixture(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(lastFixture)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// AC1: the last N entries, oldest first, each with its time, kind and text.
func TestLast_PrintsTheLastEntries_InOrder_WithTimeKindAndText(t *testing.T) {
	out, _, err := lastCLI(t, []map[string]any{pane("eng1", absFixture(t))}, "eng1")
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	want := []string{
		hhmmss("21:50:00") + " nelson", "ship the fix tonight",
		hhmmss("21:50:05") + " message from super",
		hhmmss("21:50:20") + " tool", "Bash",
		hhmmss("21:51:00") + " agent", "The cursor is a number.",
		hhmmss("21:52:00") + " nelson", "yes, release it",
		hhmmss("21:52:30") + " agent", "Released.",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(out[at:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order after byte %d:\n%s", w, at, out)
		}
		at += i + len(w)
	}
	for _, never := range []string{"private", "/clear", "<system-reminder>"} {
		if strings.Contains(out, never) {
			t.Errorf("printed %q, which the reader leaves out:\n%s", never, out)
		}
	}
}

func TestLast_N5_PrintsTheNewestFive(t *testing.T) {
	path := absFixture(t)
	out, _, err := lastCLI(t, []map[string]any{pane("eng1", path)}, "eng1", "-n", "5")
	if err != nil {
		t.Fatalf("last -n 5: %v", err)
	}
	pg, err := appserve.ReadPage(path, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	newest := pg.Messages[len(pg.Messages)-5:]
	if got := headers(out); got != 5 {
		t.Errorf("entries = %d, want 5:\n%s", got, out)
	}
	if strings.Contains(out, "ship the fix tonight") {
		t.Errorf("-n 5 printed an entry older than the newest five:\n%s", out)
	}
	if !strings.HasPrefix(out, newest[0].At.Local().Format("15:04:05")) {
		t.Errorf("first entry is not the fifth newest (%s):\n%s", newest[0].At.Local().Format("15:04:05"), out)
	}
}

// AC1 and AC4: a tool's detail is cut at the app's 4 KB, as the app gets it.
func TestLast_AToolDetail_IsCutAt4KB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	const fourKB = 4 << 10 // the contract, held here, not read from the reader it checks
	big := strings.Repeat("x", 12<<10)
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-31T21:50:20Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"cat big.log"}}]}}`,
		fmt.Sprintf(`{"type":"user","uuid":"r1","timestamp":"2026-10-31T21:50:40Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":%q}]}}`, big),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := lastCLI(t, []map[string]any{pane("eng1", path)}, "eng1")
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if n := strings.Count(out, "x"); n == 0 || n > fourKB {
		t.Errorf("tool detail printed %d bytes of its result, want 1..%d", n, fourKB)
	}
}

// AC2: one reader. The command prints exactly the app route's page for the
// same transcript, entry for entry, and the malformed line is counted.
func TestLast_PrintsTheAppRoutesPage_AndCountsTheMalformedLine(t *testing.T) {
	path := absFixture(t)
	out, _, err := lastCLI(t, []map[string]any{pane("eng1", path)}, "eng1")
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	pg, err := appserve.ReadPage(path, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	formatLast(&want, pg)
	if out != want.String() {
		t.Errorf("last differs from the app route's page:\n--- last\n%s\n--- route\n%s", out, want.String())
	}
	if pg.Skipped != 1 {
		t.Fatalf("fixture skipped %d lines, want its 1 malformed line", pg.Skipped)
	}
	if !strings.HasSuffix(out, "skipped 1 malformed line\n") {
		t.Errorf("no trailing count of the malformed line:\n%s", out)
	}
	// The route's entries in order, each header present once, nothing more.
	if got := headers(out); got != len(pg.Messages) {
		t.Errorf("entries = %d, want the route's %d", got, len(pg.Messages))
	}
}

// AC3, feed it nothing.
func TestLast_NoTranscript_SaysSoAndFails(t *testing.T) {
	out, _, err := lastCLI(t, []map[string]any{pane("eng1", filepath.Join(t.TempDir(), "none.jsonl"))}, "eng1")
	if err == nil || !strings.Contains(err.Error(), "no transcript for eng1") {
		t.Errorf("err = %v, want one line naming no transcript", err)
	}
	if out != "" {
		t.Errorf("printed %q with no transcript", out)
	}
}

func TestLast_ARemoteAgent_IsRefused(t *testing.T) {
	remote := pane("eng9", absFixture(t))
	remote["host"] = "workbench"
	for name, args := range map[string][]string{"host:agent": {"workbench:eng9"}, "a remote pane": {"eng9"}} {
		t.Run(name, func(t *testing.T) {
			out, _, err := lastCLI(t, []map[string]any{remote}, args...)
			if err == nil || !strings.Contains(err.Error(), "reads this machine's agents only") {
				t.Errorf("err = %v, want the remote refusal", err)
			}
			if out != "" {
				t.Errorf("printed a remote agent's transcript:\n%s", out)
			}
		})
	}
}

func TestLast_NOutOfRange_IsRefused(t *testing.T) {
	for _, n := range []string{"0", "201", "-3"} {
		out, _, err := lastCLI(t, []map[string]any{pane("eng1", absFixture(t))}, "eng1", "-n", n)
		if err == nil || !strings.Contains(err.Error(), "-n must be 1 to 200") {
			t.Errorf("-n %s: err = %v, want the range refusal", n, err)
		}
		if out != "" {
			t.Errorf("-n %s printed %q", n, out)
		}
	}
}

func TestLast_AnUnknownAgent_IsRefused(t *testing.T) {
	_, _, err := lastCLI(t, []map[string]any{pane("eng1", absFixture(t))}, "eng42")
	if err == nil || !strings.Contains(err.Error(), "eng42") {
		t.Errorf("err = %v, want it to name eng42", err)
	}
}
