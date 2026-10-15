package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/usage"
)

// ini-62tn: the hook runs in the background, never blocks the caller, never
// runs in a viewer, and a panic in it is dropped instead of crashing the TUI.
func TestStartAfterFirstFrame_ReturnsAtOnceAndRunsInBackground(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ran := make(chan struct{})
	returned := make(chan bool, 1)
	go func() { returned <- startAfterFirstFrame(func() { close(ran); <-release }, false) }()
	select {
	case started := <-returned:
		if !started {
			t.Fatal("not started for window 1")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startAfterFirstFrame blocked on the hook; the event loop would freeze")
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("hook never ran")
	}
}

func TestStartAfterFirstFrame_ViewerNeverRunsIt(t *testing.T) {
	ran := make(chan struct{}, 1)
	if startAfterFirstFrame(func() { ran <- struct{}{} }, true) {
		t.Fatal("started in a viewer")
	}
	select {
	case <-ran:
		t.Fatal("hook ran in a viewer")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStartAfterFirstFrame_PanicIsDropped(t *testing.T) {
	done := make(chan struct{})
	startAfterFirstFrame(func() { defer close(done); panic("boom") }, false)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hook never ran")
	}
	// Reaching here without the test binary dying is the assertion: an
	// unrecovered panic in a goroutine would abort the whole run.
	time.Sleep(50 * time.Millisecond)
}

// ini-62tn: the first-launch welcome box carries the usage notice, wrapped
// inside the box, every word on screen.
func TestWelcome_ShowsTheUsageNotice(t *testing.T) {
	tui, _, _ := firstKeyTUI(t, "")
	tui.welcome = welcomeOverlay{active: true, expiresAt: time.Now().Add(time.Minute)}
	tui.render()
	out := screenText(t, tui)
	if !strings.Contains(out, "Welcome to initech") {
		t.Fatalf("welcome box not drawn:\n%s", out)
	}
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, usage.Notice) {
		t.Fatalf("welcome box lacks the usage notice %q:\n%s", usage.Notice, out)
	}
	for _, seg := range wrapToWidth(usage.Notice, welcomeTextW) {
		if len([]rune(seg)) > welcomeTextW {
			t.Errorf("wrapped line wider than the box: %q", seg)
		}
	}
}
