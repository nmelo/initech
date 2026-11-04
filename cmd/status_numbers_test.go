package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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
	want := "  rig: merges/h 4 · gate 3g/1r med - · landing 2 · filed +5/-3 · no move 20m 1 · idle 2\n"
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
	if !strings.Contains(out, "  rig: merges/h - · gate - · landing - · filed +0/-0 · no move 20m 0 · idle 1  (computed now: window 1 has no numbers yet)") {
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
