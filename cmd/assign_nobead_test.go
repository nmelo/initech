package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ── ini-1q3x.2: assign with -m and no bead ──

const q3x2Msg = "bump main to 0.28.0 in package.json, tauri.conf.json, Cargo.toml, Cargo.lock, one commit, push"

// noBdRig is a q3xRig whose fake bd fails the test if assign touches it.
func noBdRig(t *testing.T) *q3xRig {
	t.Helper()
	rig := q3xSetup(t, nil, "[]")
	bdShowBeadFn = func(id string) (string, string, string, error) {
		t.Errorf("no-bead assign read bd for %q", id)
		return "", "", "", nil
	}
	bdDispatchFn = func(id, agent, status string, record bool) error {
		t.Errorf("no-bead assign wrote bd for %q", id)
		return nil
	}
	return rig
}

// Criteria 1, 2 and 4: no bd at all; the message goes exactly as initech
// send sends it; then the marker and the event, never a bead id.
func TestAssignNoBead_DeliversLikeSendSetsTheMarkerAndWritesNoBd(t *testing.T) {
	rig := noBdRig(t)
	assignMessage = q3x2Msg
	stderr, err := q3xRun(t, "eng3", "-m", q3x2Msg)
	if err != nil {
		t.Fatalf("no-bead assign: %v", err)
	}
	if r := rig.of("send_ready", ""); len(r) != 0 {
		t.Errorf("a readiness preflight ran (%+v); initech send has none", r)
	}
	sends := rig.of("send", "")
	if len(sends) != 1 || sends[0].Target != "eng3" || sends[0].Text != q3x2Msg || !sends[0].Enter || sends[0].Host != "" {
		t.Fatalf("sends = %+v, want the message exactly as given, submitted", sends)
	}
	if !strings.Contains(stderr, "delivered to eng3") {
		t.Errorf("stderr = %q, want initech send's report", stderr)
	}
	marks := rig.of("no_bead", "eng3")
	wantWords := "bump main to 0.28.0 in package.json, tauri.conf.json, Cargo.toml, Cargo.lock,..."
	if len(marks) != 1 || marks[0].Text != wantWords {
		t.Errorf("no_bead requests = %+v, want one with %q", marks, wantWords)
	}
	if b := rig.of("bead", ""); len(b) != 0 {
		t.Errorf("bead requests = %+v; the marker is never a bead", b)
	}
	ev := rig.of("emit_event", "eng3")
	if len(ev) != 1 || ev[0].Host != "" || ev[0].Text != "bead_assigned|no bead, assigned to eng3: "+wantWords {
		t.Errorf("events = %+v, want one bead_assigned with no bead id", ev)
	}
	// Order: the marker comes only after the send was accepted.
	rig.mu.Lock()
	defer rig.mu.Unlock()
	var order []string
	for _, q := range rig.reqs {
		order = append(order, q.Action)
	}
	if strings.Join(order, ",") != "send,no_bead,emit_event" {
		t.Errorf("request order = %v, want send, no_bead, emit_event", order)
	}
}

// A send that fails leaves nothing set.
func TestAssignNoBead_AFailedSendSetsNoMarkerAndNoEvent(t *testing.T) {
	rig := noBdRig(t)
	rig.answer("send", tui.IPCResponse{Error: `pane "eng3" not found`})
	_, err := q3xRun(t, "eng3", "-m", "do it")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want send's error", err)
	}
	if n := len(rig.of("no_bead", "")) + len(rig.of("emit_event", "")); n != 0 {
		t.Errorf("%d marker/event requests after a failed send, want none", n)
	}
}

// Older TUI: it does not know no_bead. The message is delivered, assign
// warns, and nothing else is tried on the ribbon.
func TestAssignNoBead_AnOlderTUIDeliversWarnsAndLeavesTheRibbon(t *testing.T) {
	rig := noBdRig(t)
	rig.answer("no_bead", tui.IPCResponse{Error: `unknown action "no_bead"`})
	stderr, err := q3xRun(t, "eng3", "-m", "do it")
	if err != nil {
		t.Fatalf("no-bead assign against an older TUI failed: %v", err)
	}
	if len(rig.of("send", "eng3")) != 1 {
		t.Error("the message was not delivered")
	}
	if !strings.Contains(stderr, "eng3's ribbon not updated: the running initech does not know work with no bead") {
		t.Errorf("stderr = %q, want the older-TUI warning", stderr)
	}
	if b := rig.of("bead", ""); len(b) != 0 {
		t.Errorf("bead requests = %+v; an older TUI's ribbon is left as it was", b)
	}
}

// Criterion 6 and the feed-it-nothing case: neither a bead nor a message (or a
// blank one) is a usage error that touches nothing.
func TestAssignNoBead_NeitherBeadNorMessageIsAUsageError(t *testing.T) {
	for name, args := range map[string][]string{
		"agent alone": {"eng3"},
		"blank -m":    {"eng3", "-m", "   "},
		"empty -m":    {"eng3", "-m", ""},
	} {
		t.Run(name, func(t *testing.T) {
			rig := noBdRig(t)
			_, err := q3xRun(t, args...)
			if err == nil || !strings.Contains(err.Error(), "-m") {
				t.Fatalf("err = %v, want a usage error naming -m", err)
			}
			rig.mu.Lock()
			defer rig.mu.Unlock()
			if len(rig.reqs) != 0 {
				t.Errorf("IPC requests = %+v, want none", rig.reqs)
			}
		})
	}
}

// Cross-machine: the message routes by host as send routes it; the marker
// goes to the local TUI by agent name, as the bead path does.
func TestAssignNoBead_CrossMachineRoutesTheMessageByHost(t *testing.T) {
	rig := noBdRig(t)
	stderr, err := q3xRun(t, "box2:eng3", "-m", "do it")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	s := rig.of("send", "eng3")
	if len(s) != 1 || s[0].Host != "box2" {
		t.Errorf("send = %+v, want routed to host box2", s)
	}
	if m := rig.of("no_bead", "eng3"); len(m) != 1 || m[0].Host != "" {
		t.Errorf("no_bead = %+v, want one to the local TUI", m)
	}
	if !strings.Contains(stderr, "delivered to box2:eng3") {
		t.Errorf("stderr = %q, want send's host report", stderr)
	}
}

func TestNoBeadWords_CollapsesWhitespaceAndCutsLikeATitle(t *testing.T) {
	if got := noBeadWords("  fix\n\tthe   thing  "); got != "fix the thing" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("é", 100)
	got := noBeadWords(long)
	if r := []rune(got); len(r) != noBeadWordsMax || !strings.HasSuffix(got, "...") {
		t.Errorf("cut = %d runes %q, want %d ending ...", len(r), got, noBeadWordsMax)
	}
}

func TestAssignHelp_ShowsTheNoBeadExample(t *testing.T) {
	if !strings.Contains(assignCmd.Long, `initech assign eng3 -m "bump main to 0.28.0"`) {
		t.Error("assign help is missing the no-bead example")
	}
}
