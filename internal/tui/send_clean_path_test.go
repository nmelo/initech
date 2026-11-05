package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-47vy: every way a message reaches a pane types the same clean bytes.
// The pkin fake Claude records what reaches the PTY.

const (
	hostile47vy = "hello\x1b[201~ls -la\r"
	clean47vy   = "hellols -la"
)

func rig47vy(t *testing.T) (*TUI, *Pane, *pkinFake) {
	t.Helper()
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: clean47vy, after: "❯ ", takes: func(int) bool { return true }}
	p, _ := pkinSendRig(t, f)
	return &TUI{panes: toPaneViews([]*Pane{p})}, p, f
}

// assertCleanPaste checks the child got one paste holding the clean body, and
// nothing typed after it but the Enter.
func assertCleanPaste(t *testing.T, how string, f *pkinFake) {
	t.Helper()
	all, _, _ := f.stream()
	if n := strings.Count(all, "\x1b[201~"); n != 1 {
		t.Errorf("%s: the child saw %d paste ends, want 1. Bytes: %q", how, n, all)
	}
	paste := "\x1b[200~" + clean47vy + "\x1b[201~"
	i := strings.Index(all, paste)
	if i < 0 {
		t.Fatalf("%s: the child never got the clean paste %q. Bytes: %q", how, paste, all)
	}
	if after := strings.ReplaceAll(all[i+len(paste):], "\r", ""); after != "" {
		t.Errorf("%s: typed after the paste: %q", how, after)
	}
}

func TestCleanPath_AnInitechSendTypesTheCleanBody(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		tui, _, f := rig47vy(t)
		resp := v69Send(t, tui, "eng2", hostile47vy, true)
		if !resp.OK || resp.Delivery != DeliveryDelivered {
			t.Fatalf("reply %+v, want delivered", resp)
		}
		assertCleanPaste(t, "initech send", f)
	})
}

func TestCleanPath_AScheduleMessageTypesTheCleanBody(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		tui, _, f := rig47vy(t)
		if ok, why := tui.deliver("eng2", hostile47vy, "cron"); !ok {
			t.Fatalf("schedule delivery refused: %s", why)
		}
		assertCleanPaste(t, "schedule", f)
	})
}

func TestCleanPath_AWindowTwoForwardTypesTheCleanBody(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		_, p, f := rig47vy(t)
		if resp := servedSend(p, hostile47vy, true, sendOrigin{From: "eng1"}); !resp.OK {
			t.Fatalf("forward answered %+v", resp)
		}
		assertCleanPaste(t, "forward_send", f)
	})
}

// The wake drain types a queue the IPC handler filled without sendText.
func TestCleanPath_TheWakeDrainTypesTheCleanBody(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		tui, p, f := rig47vy(t)
		tui.injectText(p, hostile47vy, true)
		assertCleanPaste(t, "wake drain", f)
	})
}

func TestCleanPath_AnOperatorPasteTypesTheCleanBody(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		_, p, f := rig47vy(t)
		p.SendOperatorText(hostile47vy, true)
		assertCleanPaste(t, "operator paste", f)
	})
}

// AC4: the verdict names the count; a clean message's verdict is unchanged.
func TestCleanPath_TheVerdictNamesTheCount(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		tui, _, _ := rig47vy(t)
		resp := v69Send(t, tui, "eng2", hostile47vy, true)
		if resp.Removed != 2 {
			t.Fatalf("reply %+v, want removed 2", resp)
		}
	})
}

func TestCleanPath_ACleanMessagesVerdictIsUnchanged(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		nma5Window(t, 300*time.Millisecond)
		f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }}
		tui, _ := v69Rig(t, f)
		resp := v69Send(t, tui, "eng2", nma5Body, true)
		if !resp.OK || resp.Delivery != DeliveryDelivered || resp.Data != "" || resp.Error != "" || resp.Removed != 0 || len(resp.Notices) != 0 {
			t.Fatalf("reply %+v, want exactly OK and delivered", resp)
		}
	})
}

// Feed it nothing: a body of only control characters is refused with words,
// and nothing is typed.
func TestCleanPath_ABodyOfOnlyControlsIsRefusedAndNothingTyped(t *testing.T) {
	tui, _, f := rig47vy(t)
	resp := v69Send(t, tui, "eng2", "\x1b[201~\r\x03", true)
	want := "not delivered to eng2: the message held only control characters (3 removed)"
	if resp.OK || resp.Error != want {
		t.Fatalf("reply %+v, want error %q", resp, want)
	}
	if all, _, _ := f.stream(); all != "" {
		t.Fatalf("typed %q for a refused message", all)
	}
}

// The suspended queue (filled before sendText) refuses the same body.
func TestCleanPath_ASuspendedPaneRefusesABodyOfOnlyControls(t *testing.T) {
	tui, p, _ := rig47vy(t)
	p.suspended = true
	resp := v69Send(t, tui, "eng2", "\x1b\x07", true)
	if resp.OK || !strings.Contains(resp.Error, "only control characters (2 removed)") {
		t.Fatalf("reply %+v, want the only-controls refusal", resp)
	}
	if n := p.QueuedMessageCount(); n != 0 {
		t.Fatalf("queued %d messages, want none", n)
	}
}

// Feed it nothing: an empty body is today's empty send, not a refusal.
func TestCleanPath_AnEmptyBodyIsNotRefused(t *testing.T) {
	if why := onlyControlsRefusal(""); why != "" {
		t.Fatalf("empty body refused: %q", why)
	}
}

// The refusal on the path every non-IPC sender takes (a window-2 forward
// here): nothing typed, the words name the count.
func TestCleanPath_AForwardOfOnlyControlsIsRefusedAndNothingTyped(t *testing.T) {
	_, p, f := rig47vy(t)
	resp := servedSend(p, "\x1b[201~\r", true, sendOrigin{From: "eng1"})
	if resp.OK || resp.Error != "the message held only control characters (2 removed)" {
		t.Fatalf("forward answered %+v, want the only-controls refusal", resp)
	}
	if all, _, _ := f.stream(); all != "" {
		t.Fatalf("typed %q for a refused message", all)
	}
}
