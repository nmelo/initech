package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

// ini-foft: the first key into a fresh window must reach what the operator
// sees. Two first-launch overlays can be due at once (the consent question and
// the welcome hints); only the one on screen may answer a key, the welcome's
// dismissing key is then handled as if the welcome were not up, and a viewer
// window never asks the session-level consent question.

// keyCapture records the bytes a pane's emulator encodes for its program.
type keyCapture struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *keyCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// firstKeyTUI is a window-1 TUI whose focused pane "a" records every byte the
// operator's keys produce, with a project that has not answered consent.
func firstKeyTUI(t *testing.T, peerName string) (*TUI, *keyCapture, *int) {
	t.Helper()
	tui, _ := newTestTUIWithScreen("a")
	cap := &keyCapture{}
	emu := vt.NewSafeEmulator(40, 10)
	go func() {
		b := make([]byte, 256)
		for {
			n, err := emu.Read(b)
			if n > 0 {
				cap.mu.Lock()
				cap.buf.Write(b[:n])
				cap.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	tui.panes[0] = &Pane{name: "a", emu: emu, alive: true, visible: true, region: Region{W: 60, H: 20}}
	tui.layoutState.Focused = "a"
	tui.project = &config.Project{Name: "test", Roles: []string{"a"}, PeerName: peerName}
	recorded := 0
	tui.onAttentionConsent = func(bool) { recorded++ }
	return tui, cap, &recorded
}

func runeKey(r rune) *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, r, 0) }

// waitForBytes polls the capture until it holds want or a second passes. The
// emulator encodes on its own goroutine, so a read straight after handleKey
// can race it.
func waitForBytes(c *keyCapture, want string) string {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(c.String(), want) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c.String()
}

func TestFirstLaunch_ConsentAndWelcomeDue_ScreenShowsOnlyTheConsentQuestion(t *testing.T) {
	tui, _, _ := firstKeyTUI(t, "")
	tui.startFirstLaunchOverlays(true)
	tui.render()
	out := screenText(t, tui)
	if !strings.Contains(out, "ask me next time") {
		t.Fatalf("consent question is not on screen:\n%s", out)
	}
	if strings.Contains(out, "Welcome to initech") {
		t.Errorf("welcome is drawn while the consent question is open; a key would answer a question it hides:\n%s", out)
	}
}

func TestFirstLaunch_KeyWhileBothDue_AnswersOnlyTheConsentQuestion(t *testing.T) {
	tui, cap, recorded := firstKeyTUI(t, "")
	tui.startFirstLaunchOverlays(true)

	tui.handleKey(runeKey('n'))
	if *recorded != 1 || tui.project.Attention.Hooks == nil || *tui.project.Attention.Hooks {
		t.Fatalf("n did not decline the consent question on screen (recorded=%d)", *recorded)
	}
	if !tui.welcome.active {
		t.Fatal("welcome did not appear once the consent question closed")
	}
	if got := cap.String(); got != "" {
		t.Errorf("the consent answer reached the pane: %q", got)
	}
}

func TestFirstLaunch_WelcomeTimerStartsWhenConsentCloses(t *testing.T) {
	tui, _, _ := firstKeyTUI(t, "")
	tui.startFirstLaunchOverlays(true)
	time.Sleep(20 * time.Millisecond)
	closedAt := time.Now()
	tui.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !tui.welcome.active {
		t.Fatal("welcome did not appear once the consent question closed")
	}
	if tui.welcome.expiresAt.Before(closedAt.Add(welcomeDuration)) {
		t.Errorf("welcome expires %v after consent closed, want its full %v", tui.welcome.expiresAt.Sub(closedAt), welcomeDuration)
	}
}

func TestFirstLaunch_SecondaryWindowNeverAsksOrRecords(t *testing.T) {
	// Run calls startFirstLaunchOverlays before it resolves windowID, so the
	// viewer's identity at that moment is only the project's peer name, which
	// --window N sets. windowID is deliberately left at its zero value here.
	tui, _, recorded := firstKeyTUI(t, "window-2")
	tui.startFirstLaunchOverlays(true)
	if tui.attentionConsent.active {
		t.Fatal("a --window 2 viewer raised the session-level consent question")
	}
	tui.handleKey(runeKey('y'))
	if *recorded != 0 || tui.project.Attention.Hooks != nil {
		t.Errorf("a viewer recorded a consent answer (recorded=%d)", *recorded)
	}
}

func TestWelcome_DismissingKeyReachesTheFocusedPane(t *testing.T) {
	tui, cap, _ := firstKeyTUI(t, "")
	tui.startFirstLaunchOverlays(true)
	tui.handleKey(runeKey('n')) // answer consent; the welcome is now up
	for _, r := range "seq 3" {
		tui.handleKey(runeKey(r))
	}
	if tui.welcome.active {
		t.Error("the first key did not dismiss the welcome")
	}
	if got := waitForBytes(cap, "seq 3"); got != "seq 3" {
		t.Errorf("pane received %q, want %q: the key that dismissed the welcome was lost", got, "seq 3")
	}
}

func TestWelcome_EscapeOnlyDismisses(t *testing.T) {
	tui, cap, _ := firstKeyTUI(t, "")
	tui.project.Attention.Hooks = new(bool) // consent already answered
	tui.startFirstLaunchOverlays(true)
	tui.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tui.welcome.active {
		t.Fatal("Escape did not dismiss the welcome")
	}
	tui.handleKey(runeKey('x'))
	if got := waitForBytes(cap, "x"); got != "x" {
		t.Errorf("pane received %q, want only %q: Escape must dismiss, never reach the pane", got, "x")
	}
}

func TestWelcome_DismissingShortcutDoesItsAction(t *testing.T) {
	tui, cap, _ := firstKeyTUI(t, "")
	tui.project.Attention.Hooks = new(bool)
	tui.startFirstLaunchOverlays(true)
	tui.handleKey(runeKey('`'))
	if !tui.cmd.active {
		t.Error("backtick dismissed the welcome but did not open the command bar")
	}
	if got := cap.String(); got != "" {
		t.Errorf("a TUI shortcut reached the pane: %q", got)
	}
}

func TestFirstLaunch_NothingDue_RaisesNothingAndKeysReachThePane(t *testing.T) {
	tui, cap, recorded := firstKeyTUI(t, "")
	tui.project = nil // no config: consent cannot be asked
	tui.startFirstLaunchOverlays(false)
	if tui.attentionConsent.active || tui.welcome.active || tui.welcome.waiting {
		t.Fatalf("overlays raised with nothing due: consent=%v welcome=%+v", tui.attentionConsent.active, tui.welcome)
	}
	tui.handleKey(runeKey('s'))
	if got := waitForBytes(cap, "s"); got != "s" || *recorded != 0 {
		t.Errorf("pane received %q (recorded=%d), want %q", got, *recorded, "s")
	}
}

// TestFirstLaunch_IfBothAreEverUp_TheConsentQuestionIsOnTop pins the draw
// order behind the one-at-a-time rule: the consent question takes keys first,
// so if both are ever active it must be the one the operator can read.
func TestFirstLaunch_IfBothAreEverUp_TheConsentQuestionIsOnTop(t *testing.T) {
	tui, _, _ := firstKeyTUI(t, "")
	tui.attentionConsent.active = true
	tui.welcome = welcomeOverlay{active: true, expiresAt: time.Now().Add(welcomeDuration)}
	tui.render()
	if out := screenText(t, tui); !strings.Contains(out, "ask me next time") {
		t.Errorf("the consent question is covered by the welcome:\n%s", out)
	}
}

// welcomePasteRecorder records each paste flushed to the pane; everything else is
// the real pane's.
type welcomePasteRecorder struct {
	*Pane
	pastes []string
}

func (p *welcomePasteRecorder) FlushPaste(b []byte) { p.pastes = append(p.pastes, string(b)) }

func welcomePasteTUI(t *testing.T, hooksAnswered bool) (*TUI, *welcomePasteRecorder) {
	t.Helper()
	tui, _, _ := firstKeyTUI(t, "")
	if hooksAnswered {
		tui.project.Attention.Hooks = new(bool)
	}
	rec := &welcomePasteRecorder{Pane: tui.panes[0].(*Pane)}
	tui.panes[0] = rec
	return tui, rec
}

// pasteThrough drives a bracketed paste through handleEvent, the path tcell's real
// events take.
func pasteThrough(tui *TUI, text string) {
	tui.handleEvent(tcell.NewEventPaste(true))
	for _, r := range text {
		tui.handleEvent(runeKey(r))
	}
	tui.handleEvent(tcell.NewEventPaste(false))
}

func TestWelcome_PasteDismissesItAndReachesThePaneWhole(t *testing.T) {
	tui, rec := welcomePasteTUI(t, true)
	tui.startFirstLaunchOverlays(true)
	pasteThrough(tui, "seq 3")
	if tui.welcome.active {
		t.Error("the paste did not dismiss the welcome")
	}
	if len(rec.pastes) != 1 || rec.pastes[0] != "seq 3" {
		t.Errorf("pane got pastes %q, want exactly one %q", rec.pastes, "seq 3")
	}
}

func TestConsentQuestion_PasteNeverReachesThePane(t *testing.T) {
	tui, rec := welcomePasteTUI(t, false)
	tui.startFirstLaunchOverlays(true)
	pasteThrough(tui, "y")
	if len(rec.pastes) != 0 {
		t.Errorf("a paste made while the consent question was open reached the pane: %q", rec.pastes)
	}
	if !tui.attentionConsent.active || tui.project.Attention.Hooks != nil {
		t.Error("a paste answered or closed the consent question")
	}
}
