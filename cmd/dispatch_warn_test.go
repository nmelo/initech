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

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// warnTUI is a fake TUI for the high-context reminder (ini-qn9b): it answers
// list with the panes it holds, everything else with OK, and records every
// request in order.
type warnTUI struct {
	mu    sync.Mutex
	panes []map[string]any
	reqs  []tui.IPCRequest
	// refuseSendTo makes a send to this target answer not OK.
	refuseSendTo string
}

func (f *warnTUI) requests() []tui.IPCRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tui.IPCRequest(nil), f.reqs...)
}

// sends are the send requests the fake saw, in order.
func (f *warnTUI) sends() []tui.IPCRequest {
	var out []tui.IPCRequest
	for _, r := range f.requests() {
		if r.Action == "send" {
			out = append(out, r)
		}
	}
	return out
}

func startWarnTUI(t *testing.T, panes ...map[string]any) *warnTUI {
	t.Helper()
	skipWindows(t)
	f := &warnTUI{panes: panes}
	n := fakeIPCCounter.Add(1)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-warn-%d-%d.sock", os.Getpid(), n))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req tui.IPCRequest
				if json.Unmarshal(line, &req) != nil {
					return
				}
				f.mu.Lock()
				f.reqs = append(f.reqs, req)
				resp := tui.IPCResponse{OK: true}
				if req.Action == "list" {
					data, _ := json.Marshal(f.panes)
					resp.Data = string(data)
				}
				if req.Action == "send" && req.Target == f.refuseSendTo && f.refuseSendTo != "" {
					resp = tui.IPCResponse{OK: false, Error: "agent not found: " + req.Target}
				}
				f.mu.Unlock()
				out, _ := json.Marshal(resp)
				c.Write(append(out, '\n'))
			}()
		}
	}()
	t.Setenv("INITECH_SOCKET", sock)
	return f
}

// warnRig stubs bd and makes the cwd a rig folder whose initech.yaml carries
// line. stubBdFns changes into a rig folder of its own, so it runs first.
func warnRig(t *testing.T, line string) {
	t.Helper()
	stubBdFns(t)
	dir := t.TempDir()
	body := "project: demo\nroot: " + dir + "\nroles: [super, eng1]\n" + line
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// warnPane is a local pane at context percent ctx (nil: not reported), with
// beads on its ribbon.
func warnPane(name string, ctx *int, beads ...string) map[string]any {
	p := map[string]any{"name": name, "alive": true, "context_percent": ctx}
	if beads != nil {
		p["beads"] = beads
	}
	return p
}

func pct(n int) *int { return &n }

// assignAs runs initech assign agent id, as the agent who in INITECH_AGENT
// ("" for a shell outside any pane), with bd answering a plain dispatch.
func assignAs(t *testing.T, who, agent, id string) (stdout, stderr string, err error) {
	t.Helper()
	resetAssignFlags(t)
	assignTitles(func(string) (string, error) { return "Fix the bug", nil })
	bdDispatchFn = func(string, string, string, bool) error { return nil }
	t.Setenv("INITECH_AGENT", who)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs([]string{"assign", agent, id})
	defer rootCmd.SetArgs(nil)
	err = rootCmd.Execute()
	return out.String(), errb.String(), err
}

const warnLine85 = "[from initech] eng1 is at 85% context; clear it (initech clear eng1) or restart it (initech restart eng1) before new work"

// warnSent is the high-context line among the sends, and to whom.
func warnSent(f *warnTUI) []tui.IPCRequest {
	var out []tui.IPCRequest
	for _, s := range f.sends() {
		if strings.HasPrefix(s.Text, "[from initech]") && strings.Contains(s.Text, "context; clear it") {
			out = append(out, s)
		}
	}
	return out
}

// AC1: at 80, a new bead to an agent at 85% tells the dispatcher, through the
// send path from initech; the dispatch itself is what it always was.
func TestAssign_AHighContextTarget_WarnsTheDispatcher_AndStillDispatches(t *testing.T) {
	warnRig(t, "dispatch_context_warn_percent: 80\n")
	f := startWarnTUI(t, warnPane("super", pct(30)), warnPane("eng1", pct(85)))
	stdout, _, err := assignAs(t, "super", "eng1", "ini-abc")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	w := warnSent(f)
	if len(w) != 1 {
		t.Fatalf("high-context lines = %d, want 1; sends: %+v", len(w), f.sends())
	}
	if w[0].Target != "super" || w[0].Text != warnLine85 || w[0].From != "initech" || !w[0].Enter {
		t.Errorf("line = %+v, want %q to super from initech", w[0], warnLine85)
	}
	dispatch := f.sends()[0]
	if dispatch.Target != "eng1" || !strings.Contains(dispatch.Text, "ini-abc") {
		t.Errorf("first send = %+v, want the dispatch to eng1 first", dispatch)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want assign's stdout unchanged (empty)", stdout)
	}
}

// AC2: below the setting no line; exactly at it, a line.
func TestAssign_TheSettingIsAtOrAbove(t *testing.T) {
	for ctx, want := range map[int]int{79: 0, 80: 1, 100: 1} {
		t.Run(fmt.Sprint(ctx), func(t *testing.T) {
			warnRig(t, "dispatch_context_warn_percent: 80\n")
			f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(ctx)))
			if _, _, err := assignAs(t, "super", "eng1", "ini-abc"); err != nil {
				t.Fatal(err)
			}
			if got := len(warnSent(f)); got != want {
				t.Errorf("at %d%%: %d lines, want %d", ctx, got, want)
			}
		})
	}
}

// AC3: absent or 0 is off, and assign asks the TUI exactly what it asks today.
func TestAssign_SettingOffOrAbsent_NoLine_AndTheSameRequests(t *testing.T) {
	trace := func(line string) []string {
		warnRig(t, line)
		f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(99)))
		if _, _, err := assignAs(t, "super", "eng1", "ini-abc"); err != nil {
			t.Fatal(err)
		}
		if n := len(warnSent(f)); n != 0 {
			t.Errorf("%q: %d high-context lines, want none", line, n)
		}
		var out []string
		for _, r := range f.requests() {
			out = append(out, r.Action+" "+r.Target)
		}
		return out
	}
	absent, zero := trace(""), trace("dispatch_context_warn_percent: 0\n")
	if strings.Join(absent, "|") != strings.Join(zero, "|") {
		t.Errorf("0 asks %v, absent asks %v", zero, absent)
	}
	for _, r := range absent {
		if strings.HasPrefix(r, "list ") {
			t.Errorf("with the reminder off, assign read the pane list: %v", absent)
		}
	}
}

// AC4: a target whose context is not reported (a Codex or agy pane, a generic
// command): no line, the dispatch happens, exit 0.
func TestAssign_UnknownContext_NoLine_DispatchHappens(t *testing.T) {
	warnRig(t, "dispatch_context_warn_percent: 80\n")
	f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", nil))
	if _, _, err := assignAs(t, "super", "eng1", "ini-abc"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if n := len(warnSent(f)); n != 0 {
		t.Errorf("%d lines for a target with no context reading", n)
	}
	if s := f.sends(); len(s) != 1 || s[0].Target != "eng1" {
		t.Errorf("sends = %+v, want only the dispatch to eng1", s)
	}
}

// Feed it nothing: a target the list does not name, and no rig config.
func TestAssign_NoConfig_OrATargetNotListed_NoLine(t *testing.T) {
	t.Run("not listed", func(t *testing.T) {
		warnRig(t, "dispatch_context_warn_percent: 80\n")
		f := startWarnTUI(t, warnPane("super", pct(95)))
		if _, _, err := assignAs(t, "super", "eng1", "ini-abc"); err != nil {
			t.Fatal(err)
		}
		if n := len(warnSent(f)); n != 0 {
			t.Errorf("%d lines for a target the list does not name", n)
		}
	})
	t.Run("no config", func(t *testing.T) {
		stubBdFns(t)
		t.Chdir(t.TempDir())
		f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(95)))
		if _, _, err := assignAs(t, "super", "eng1", "ini-abc"); err != nil {
			t.Fatal(err)
		}
		if n := len(warnSent(f)); n != 0 {
			t.Errorf("%d lines with no config", n)
		}
	})
}

// AC5 for assign: a bead already on the target's ribbon is not new, and a
// bead bd already holds for the target (a fold-in) is not new.
func TestAssign_ABeadTheAgentAlreadyHas_NoLine(t *testing.T) {
	t.Run("on the ribbon", func(t *testing.T) {
		warnRig(t, "dispatch_context_warn_percent: 80\n")
		f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(90), "ini-abc"))
		if _, _, err := assignAs(t, "super", "eng1", "ini-abc"); err != nil {
			t.Fatal(err)
		}
		if n := len(warnSent(f)); n != 0 {
			t.Errorf("%d lines re-assigning the bead on the ribbon", n)
		}
	})
	t.Run("bd holds it for the target", func(t *testing.T) {
		warnRig(t, "dispatch_context_warn_percent: 80\n")
		f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(90)))
		resetAssignFlags(t)
		bdShowBeadFn = func(string) (string, string, string, error) { return "Fix the bug", "eng1", "in_progress", nil }
		bdDispatchFn = func(string, string, string, bool) error { return nil }
		t.Setenv("INITECH_AGENT", "super")
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc"})
		defer rootCmd.SetArgs(nil)
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if n := len(warnSent(f)); n != 0 {
			t.Errorf("%d lines for a fold-in", n)
		}
	})
}

// AC6: run outside any pane, the line goes to stderr; exit and stdout unchanged.
func TestAssign_OutsideAPane_TheLineGoesToStderr(t *testing.T) {
	warnRig(t, "dispatch_context_warn_percent: 80\n")
	f := startWarnTUI(t, warnPane("eng1", pct(85)))
	stdout, stderr, err := assignAs(t, "", "eng1", "ini-abc")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !strings.Contains(stderr, warnLine85+"\n") {
		t.Errorf("stderr = %q, want the line", stderr)
	}
	if n := len(warnSent(f)); n != 0 {
		t.Errorf("sent the line %d times with no pane to send it to", n)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want unchanged", stdout)
	}
}

// beadAs runs initech bead --agent agent id, as who.
func beadAs(t *testing.T, who, agent, id string) (string, error) {
	t.Helper()
	bdShowTitleFn = func(string) (string, error) { return "Fix the bug", nil }
	t.Setenv("INITECH_AGENT", who)
	beadAgent, beadClear = "", false
	t.Cleanup(func() { beadAgent, beadClear = "", false })
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&bytes.Buffer{})
	args := []string{"bead", id}
	if agent != "" {
		args = append(args, "--agent", agent)
	}
	rootCmd.SetArgs(args)
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return out.String(), err
}

// AC5: initech bead <bead> --agent warns the same way; a bead already on the
// ribbon does not; an agent setting its own bead never does.
func TestBead_ANewBeadForAHighContextAgent_Warns_ARepeatDoesNot(t *testing.T) {
	warnRig(t, "dispatch_context_warn_percent: 80\n")
	f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(85), "ini-old"))
	stdout, err := beadAs(t, "super", "eng1", "ini-abc")
	if err != nil {
		t.Fatalf("bead: %v", err)
	}
	if w := warnSent(f); len(w) != 1 || w[0].Target != "super" || w[0].Text != warnLine85 {
		t.Errorf("lines = %+v, want one %q to super", w, warnLine85)
	}
	if stdout != "Set ini-abc for eng1.\n" {
		t.Errorf("stdout = %q, want bead's own line unchanged", stdout)
	}

	f2 := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(85), "ini-abc"))
	if _, err := beadAs(t, "super", "eng1", "ini-abc"); err != nil {
		t.Fatal(err)
	}
	if n := len(warnSent(f2)); n != 0 {
		t.Errorf("%d lines re-showing the bead the agent already has", n)
	}

	f3 := startWarnTUI(t, warnPane("eng1", pct(85)))
	if _, err := beadAs(t, "eng1", "", "ini-abc"); err != nil {
		t.Fatal(err)
	}
	if n := len(warnSent(f3)); n != 0 {
		t.Errorf("%d lines for an agent setting its own bead", n)
	}
}

// A line that cannot be delivered never fails the dispatch: one stderr line,
// exit and stdout as without the reminder.
func TestAssign_TheLineCannotBeDelivered_TheDispatchStillSucceeds(t *testing.T) {
	warnRig(t, "dispatch_context_warn_percent: 80\n")
	f := startWarnTUI(t, warnPane("super", nil), warnPane("eng1", pct(85)))
	f.mu.Lock()
	f.refuseSendTo = "super"
	f.mu.Unlock()
	stdout, stderr, err := assignAs(t, "super", "eng1", "ini-abc")
	if err != nil {
		t.Fatalf("assign failed over an undelivered reminder: %v", err)
	}
	if strings.Count(stderr, "did not reach super") != 1 {
		t.Errorf("stderr = %q, want one line saying the reminder did not reach super", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want unchanged", stdout)
	}
}

// The line itself is decided in one pure place: off (0) never owes a line,
// whatever the target reads; unlisted and unknown never do either.
func TestHighContextLine_DecidesOffUnknownAndNew(t *testing.T) {
	high := dispatchTarget{listed: true, context: pct(95)}
	cases := []struct {
		name  string
		th    int
		t     dispatchTarget
		beads []string
		want  bool
	}{
		{"off, even at 95%", 0, high, []string{"ini-a"}, false},
		{"on, 95% at 80", 80, high, []string{"ini-a"}, true},
		{"unlisted target", 80, dispatchTarget{context: pct(95)}, []string{"ini-a"}, false},
		{"context not read", 80, dispatchTarget{listed: true}, []string{"ini-a"}, false},
		{"every bead already on the ribbon", 80, dispatchTarget{listed: true, context: pct(95), beads: []string{"ini-a"}}, []string{"ini-a"}, false},
		{"one bead of two is new", 80, dispatchTarget{listed: true, context: pct(95), beads: []string{"ini-a"}}, []string{"ini-a", "ini-b"}, true},
	}
	for _, c := range cases {
		if _, got := highContextLine(config.WarnPercent(c.th), "eng1", c.t, c.beads); got != c.want {
			t.Errorf("%s: owed = %v, want %v", c.name, got, c.want)
		}
	}
}
