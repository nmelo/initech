package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nmelo/initech/internal/config"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/rignumbers"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-3sl: status prints the rig line after the agent table, from window 1's
// cached numbers; it reads gh and bd itself only when window 1 has none, and
// says so. --json keeps its agent array; --json --numbers is one object.

// countingRigRun records every read status makes for the rig numbers.
type countingRigRun struct {
	mu    sync.Mutex
	calls []string
}

func (c *countingRigRun) install(t *testing.T) {
	t.Helper()
	orig := statusRigRun
	t.Cleanup(func() { statusRigRun = orig })
	statusRigRun = func(string) rignumbers.Runner {
		return func(_ context.Context, name string, args ...string) ([]byte, error) {
			c.mu.Lock()
			c.calls = append(c.calls, name+" "+strings.Join(args, " "))
			c.mu.Unlock()
			return []byte("[]"), nil
		}
	}
}

func intp(v int) *int { return &v }

// windowOneNumbers is what window 1 would have cached.
func windowOneNumbers() *rignumbers.Numbers {
	return &rignumbers.Numbers{
		At: time.Now(), Repo: true,
		MergesPerHour: intp(4), GateGreen: intp(3), GateRed: intp(1), LandingDepth: intp(2),
		Filed: intp(5), Closed: intp(3), NoMove20m: intp(1), IdleEngineers: intp(2),
	}
}

func runStatusWith(t *testing.T, resp tui.IPCResponse, json, numbers bool) string {
	t.Helper()
	skipWindows(t)
	t.Cleanup(disableColor(t))
	t.Setenv("INITECH_SOCKET", "")
	fakeProjectWithIPC(t, resp)
	oj, on := statusJSON, statusNumbers
	statusJSON, statusNumbers = json, numbers
	t.Cleanup(func() { statusJSON, statusNumbers = oj, on })
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	if err := runStatus(c, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	return out.String()
}

func paneReply(rig *rignumbers.Numbers) tui.IPCResponse {
	panes, _ := json.Marshal([]map[string]any{{"name": "eng1", "activity": "idle", "alive": true, "visible": true}})
	return tui.IPCResponse{OK: true, Data: string(panes), Rig: rig}
}

// The line after the table is window 1's, and status reads nothing itself.
func TestStatusNumbers_TheLineIsWindowOnesCachedNumbers(t *testing.T) {
	var c countingRigRun
	c.install(t)
	out := runStatusWith(t, paneReply(windowOneNumbers()), false, false)
	// Window 1's cached numbers here predate today's main fields, as an
	// older window 1's would: those read ? (ini-9z0b).
	want := "  merges/h 4 · main ? · median ? · closed today ?\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("status ends\n%q\nwant the rig line\n%q", out[max(0, len(out)-200):], want)
	}
	if strings.Contains(out, "computed now") {
		t.Errorf("status said it computed window 1's numbers:\n%s", out)
	}
	if len(c.calls) != 0 {
		t.Errorf("status read %q with window 1's numbers in hand", c.calls)
	}
}

// Window 1 has none yet: status computes them itself and says so.
func TestStatusNumbers_WithNoneFromWindowOneStatusComputesAndSaysSo(t *testing.T) {
	var c countingRigRun
	c.install(t)
	out := runStatusWith(t, paneReply(nil), false, false)
	if !strings.Contains(out, "  merges/h - · main - · median - · closed today 0  (computed now: window 1 has no numbers yet)") {
		t.Errorf("status:\n%s", out)
	}
	if len(c.calls) != 1 || !strings.HasPrefix(c.calls[0], "bd list --all") {
		t.Errorf("reads %q, want one bd list (the fixture has no repo)", c.calls)
	}
}

// --json keeps the bare agent array; --json --numbers is one object.
func TestStatusNumbers_JSONShapes(t *testing.T) {
	var c countingRigRun
	c.install(t)
	var agents []map[string]any
	if err := json.Unmarshal([]byte(runStatusWith(t, paneReply(windowOneNumbers()), true, false)), &agents); err != nil || len(agents) != 1 {
		t.Fatalf("status --json is no longer the agent array: %v %v", err, agents)
	}
	var rig struct {
		Source  string             `json:"source"`
		Numbers rignumbers.Numbers `json:"numbers"`
		Line    string             `json:"line"`
	}
	if err := json.Unmarshal([]byte(runStatusWith(t, paneReply(windowOneNumbers()), true, true)), &rig); err != nil {
		t.Fatal(err)
	}
	if rig.Source != "window 1" || rig.Numbers.GateRed == nil || *rig.Numbers.GateRed != 1 || !strings.HasPrefix(rig.Line, "merges/h 4") {
		t.Errorf("status --json --numbers: %+v", rig)
	}
}

// ini-61by: with no numbers from window 1, status reads product_repo's
// clone, not the first repo, through the same gather window 1 uses.
func TestStatusNumbers_ComputedReadsTheProductRepo(t *testing.T) {
	var c countingRigRun
	c.install(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "product", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &config.Project{Name: "demo", Root: root, ProductRepo: "product",
		Repos: []config.Repo{{URL: "https://github.com/acme/scaffold", Name: "scaffold"}}}
	statusRigNumbers(&statusRead{}, p, time.Now())
	joined := strings.Join(c.calls, "\n")
	if !strings.Contains(joined, "git -C "+filepath.Join(root, "product")+" fetch") || strings.Contains(joined, "scaffold") {
		t.Fatalf("status read %q, want the product clone and never the first repo", c.calls)
	}
}

// TestStatusNumbers_TheJSONFieldsAreUnchanged is AC3 (ini-9z0b): every field
// --json --numbers had keeps its name; the new ones are added beside them,
// and line keeps the old one-line form.
func TestStatusNumbers_TheJSONFieldsAreUnchanged(t *testing.T) {
	n := windowOneNumbers()
	b, err := json.Marshal(rigNumbersJSON{Source: "window 1", Numbers: *n, Line: n.Line()})
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	json.Unmarshal(b, &top)
	for _, k := range []string{"source", "numbers", "line"} {
		if _, ok := top[k]; !ok {
			t.Errorf("--json --numbers lost %q", k)
		}
	}
	var nums map[string]json.RawMessage
	json.Unmarshal(top["numbers"], &nums)
	for _, k := range []string{"at", "repo", "merges_per_hour", "gate_green", "gate_red", "gate_median_minutes",
		"landing_depth", "filed", "closed", "no_move_20m", "idle_engineers"} {
		if _, ok := nums[k]; !ok {
			t.Errorf("numbers lost %q", k)
		}
	}
	for _, k := range []string{"main_pass_today", "main_fail_today", "main_latest_red", "main_median_minutes_today", "closed_today"} {
		if _, ok := nums[k]; !ok {
			t.Errorf("numbers lacks the new %q", k)
		}
	}
	if !strings.HasPrefix(string(top["line"]), `"merges/h`) || !strings.Contains(string(top["line"]), "gate") {
		t.Errorf("line changed form: %s", top["line"])
	}
}
