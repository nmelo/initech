package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-evdn: a viewer's "main PID | started | age" line is hidden by default
// and toggled per window by Option+w. Row
// 42 of a 44-row screen is the layout spacer (h-2) the line draws into.

func evdnScreen(t *testing.T, window string) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	s.SetSize(130, 44)
	u := newTestTUI()
	u.screen = s
	u.windowID = window
	u.viewerConnected = true
	u.viewerAuthority = &AuthorityIdentity{PID: 123, StartedAt: time.Now().Add(-time.Minute).UTC()}
	return u, s
}

func evdnOptionW() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModAlt) }

func TestEvdnIdentityLine_HiddenByDefaultReturnsThePreZ5uwFooter(t *testing.T) {
	u, s := evdnScreen(t, "window-2")
	u.render()
	if text := readScreenRect(s, 0, 0, 130, 44); strings.Contains(text, "main PID") {
		t.Fatalf("default viewer footer names the main:\n%s", text)
	}
	if row := strings.TrimSpace(readScreenRect(s, 0, 42, 130, 1)); row != "" {
		t.Fatalf("spacer row h-2 is not the plain spacer: %q", row)
	}
	// Toasts sit where window 1's do, not lifted over a line that is not drawn.
	u.notifications = []notification{{event: AgentEvent{Detail: "newest notice"}}}
	u.render()
	if !strings.Contains(readScreenRect(s, 0, 42, 130, 1), "newest notice") {
		t.Fatal("viewer toast is still lifted above an identity line that is hidden")
	}
}

func TestEvdnIdentityLine_OptionWShowsThenHidesInAViewer(t *testing.T) {
	u, s := evdnScreen(t, "window-2")
	u.handleKey(evdnOptionW())
	u.render()
	if row := readScreenRect(s, 0, 42, 130, 1); !strings.Contains(row, "main PID 123 | started ") || !strings.Contains(row, "| age ") {
		t.Fatalf("Option+w did not show the identity line in row h-2: %q", row)
	}
	u.handleKey(evdnOptionW())
	u.render()
	if text := readScreenRect(s, 0, 0, 130, 44); strings.Contains(text, "main PID") {
		t.Fatalf("second Option+w left the line on screen:\n%s", text)
	}
	if row := strings.TrimSpace(readScreenRect(s, 0, 42, 130, 1)); row != "" {
		t.Fatalf("hiding the line did not give the row back: %q", row)
	}
}

func TestEvdnIdentityLine_MainWindowOptionWExplainsAndChangesNothing(t *testing.T) {
	u, s := evdnScreen(t, WindowOne)
	u.handleKey(evdnOptionW())
	const want = "identity line is for viewer windows"
	if u.cmd.error != want {
		t.Fatalf("notice = %q, want %q", u.cmd.error, want)
	}
	if u.identityLineShown {
		t.Fatal("Option+w in the main window changed the toggle state")
	}
	u.render()
	if footer := readScreenRect(s, 0, 43, 130, 1); !strings.Contains(footer, want) {
		t.Fatalf("notice not on the footer row: %q", footer)
	}
	if strings.Contains(readScreenRect(s, 0, 0, 130, 43), "main PID") {
		t.Fatal("the main window drew the identity line")
	}
}

func TestEvdnIdentityLine_HelpListsTheChordForViewers(t *testing.T) {
	for _, l := range getHelpLines() {
		if strings.Contains(l, modKey+"+w") && strings.Contains(l, "(viewer windows)") {
			return
		}
	}
	t.Fatal("help overlay does not list Option+w for viewer windows")
}
