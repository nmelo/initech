package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// ini-hoy: the TUI refuses a slash command to a Claude pane that would
// paste, not run. hoyRefusal is that refusal as the TUI answers it.
func hoyRefusal(words string) tui.IPCResponse {
	return tui.IPCResponse{Error: "not delivered to a: " + words, Delivery: tui.DeliveryNotDelivered, Data: words}
}

const hoyLong = "a slash command of 1309 characters would paste, not run; Claude takes a command of at most 800"

// AC1: compact with a long keep text says it did not compact, exit 1, and
// watches nothing; from the agent's own pane too, where a short compact says
// "queued".
func TestCompact_ASlashCommandThatWouldPasteIsNotCompacted(t *testing.T) {
	for _, c := range []struct{ name, words, self string }{
		{"long", hoyLong, ""},
		{"four lines", "a slash command of 4 lines would paste, not run; Claude takes a command of at most 3 lines", ""},
		{"from its own pane", hoyLong, "a"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sf5Window(t, 5*time.Second, 10*time.Millisecond)
			f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: hoyRefusal(c.words), figure: func(int) *int { return sf5Pct(60) }}
			startSf5TUI(t, f)
			out, err := sf5Run("a", "/compact "+strings.Repeat("k", 1300), c.self)
			if v69Exit(err) != 1 {
				t.Fatalf("exit %d (%v), want 1", v69Exit(err), err)
			}
			if want := "not compacted: " + c.words; err.Error() != want {
				t.Errorf("error %q, want %q", err, want)
			}
			if out != "" {
				t.Errorf("printed %q, want nothing on success's channel (no queued line)", out)
			}
			if f.lists.Load() != 1 {
				t.Errorf("%d list reads: it watched after a refusal", f.lists.Load())
			}
		})
	}
}

// AC1: initech send prints the TUI's words, exit 1.
func TestSend_ASlashCommandThatWouldPasteIsRefusedExit1(t *testing.T) {
	f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: hoyRefusal(hoyLong)}
	startSf5TUI(t, f)
	var out strings.Builder
	err := deliverText(&out, "a", "", "/compact "+strings.Repeat("k", 1300), true, false)
	if v69Exit(err) != 1 || err.Error() != "not delivered to a: "+hoyLong {
		t.Fatalf("exit %d, error %v; want 1 and the TUI's words", v69Exit(err), err)
	}
}

// AC1: POST /v1/send carries the refusal as it carries every verdict:
// not_delivered with initech send's own words for the same reply.
func TestAppSend_ASlashCommandThatWouldPasteIsNotDelivered(t *testing.T) {
	f := &r0q9TUI{resp: hoyRefusal(hoyLong)}
	startR0q9TUI(t, f)
	got := (&appBackend{}).Send(appserve.Rig{Name: "r", Socket: os.Getenv("INITECH_SOCKET")}, "a", "/compact "+strings.Repeat("k", 1300), true)
	if got.Verdict != tui.DeliveryNotDelivered || got.Words != "not delivered to a: "+hoyLong {
		t.Errorf("service %+v, want not_delivered with the words", got)
	}
}
