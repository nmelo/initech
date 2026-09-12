package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	iexec "github.com/nmelo/initech/internal/exec"
)

// Existing proof fixtures have no Claude child. Give them a completed failed
// lookup so they stay independent of whichever CLI is installed on the host.
func measuredOnlyVersionProbe() *claudeVersionProbe {
	p := newClaudeVersionProbe(nil)
	p.once.Do(func() { close(p.done) })
	return p
}

func awaitVersionSurface(t *testing.T, events <-chan AgentEvent) AgentEvent {
	t.Helper()
	select {
	case ev := <-events:
		if ev.Type != EventAgentStalled || !strings.Contains(ev.Detail, "NOT delivered") {
			t.Fatalf("not an undelivered report: %+v", ev)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("undelivered report blocked on version lookup")
		return AgentEvent{}
	}
}

func TestClaudeVersion_OffsetMismatchSurfacesBothVersions(t *testing.T) {
	p, _, events := proofPane(t)
	runner := &iexec.FakeRunner{Output: "2.1.240 (Claude Code)"}
	p.claudeVersion = newClaudeVersionProbe(func(_ context.Context, name string, args ...string) (string, error) {
		return runner.Run(name, args...)
	})
	armWithheld(p, longBody(60))
	// A different CLI convention uses offset 2. Our measured offset 1 is
	// now mismatched: the real proof must reject this literal 60-line/58 pair.
	const changedConvention = "[Pasted text #1 +58 lines]"
	p.emu.Write([]byte("❯ " + changedConvention))
	if composerProvesOurPaste(p, changedConvention, p.pendingSubmit) {
		t.Fatal("fixture did not reach the failed ownership proof")
	}
	p.maybeRetryWithheldSubmit()
	ev := awaitVersionSurface(t, events)
	for _, want := range []string{"measured against 2.1.233", "running 2.1.240", "re-send it:"} {
		if !strings.Contains(ev.Detail, want) {
			t.Fatalf("missing %q: %s", want, ev.Detail)
		}
	}
	if len(runner.Calls) != 1 || runner.Calls[0] != "|claude --version" {
		t.Fatal(runner.Calls)
	}
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(80, 24)
	u := newTestTUI()
	u.screen = s
	u.notifications = []notification{{event: ev}}
	u.renderNotifications()
	visible := readScreenRect(s, 0, 22, 80, 1)
	for _, version := range []string{"2.1.233", "2.1.240"} {
		if !strings.Contains(visible, version) {
			t.Fatalf("toast clipped %s: %s", version, visible)
		}
	}
	// The resolver's compare-and-clear prevents duplicate reporting or lookup.
	p.maybeRetryWithheldSubmit()
	select {
	case ev := <-events:
		t.Fatalf("duplicate report: %+v", ev)
	default:
	}
}

func TestClaudeVersion_SuccessfulProofNeverQueriesVersion(t *testing.T) {
	p, submits, _ := proofPane(t)
	var calls atomic.Int32
	p.claudeVersion = newClaudeVersionProbe(func(context.Context, string, ...string) (string, error) {
		calls.Add(1)
		return "2.1.240", nil
	})
	armWithheld(p, longBody(60))
	p.emu.Write([]byte("❯ [Pasted text #1 +59 lines]"))
	p.maybeRetryWithheldSubmit()
	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("positive proof did not submit")
	}
	if calls.Load() != 0 {
		t.Fatal("version lookup ran on successful delivery")
	}
	select {
	case <-p.claudeVersion.done:
		t.Fatal("cache was started on the happy path")
	default:
	}
}

func TestClaudeVersion_ConcurrentFailuresShareOneSessionLookup(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	probe := newClaudeVersionProbe(func(ctx context.Context, _ string, _ ...string) (string, error) {
		calls.Add(1)
		select {
		case <-release:
			return "2.1.240 (Claude Code)", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	events := make(chan AgentEvent, 8)
	for i := 0; i < 4; i++ {
		p := &Pane{name: "eng", eventCh: events, claudeVersion: probe}
		ps := newPendingSubmit("a message that cannot be proven", "bracketed", "")
		p.pendingSubmit = ps
		p.surfaceUndeliveredSubmit(ps, "proof failed")
	}
	close(release)
	for i := 0; i < 4; i++ {
		if !strings.Contains(awaitVersionSurface(t, events).Detail, "running 2.1.240") {
			t.Fatal("cached version missing")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("executed %d times", calls.Load())
	}
}

func TestClaudeVersion_MissingBinaryIsCachedAndReportsMeasuredOnly(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var calls atomic.Int32
	probe := newClaudeVersionProbe(func(ctx context.Context, name string, args ...string) (string, error) {
		calls.Add(1)
		return (&iexec.DefaultRunner{}).RunContext(ctx, name, args...)
	})
	for i := 0; i < 2; i++ {
		p, _, events := proofPane(t)
		p.claudeVersion = probe
		armWithheld(p, longBody(60))
		p.emu.Write([]byte("❯ [Pasted text #1 +58 lines]"))
		p.maybeRetryWithheldSubmit()
		ev := awaitVersionSurface(t, events)
		if !strings.Contains(ev.Detail, "measured against 2.1.233") || strings.Contains(ev.Detail, "running ") {
			t.Fatal(ev.Detail)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("missing binary retried %d times", calls.Load())
	}
}

func TestClaudeVersion_HungLookupDoesNotHoldSendLockAndTimesOut(t *testing.T) {
	p, _, events := proofPane(t)
	started := make(chan struct{})
	p.claudeVersion = newClaudeVersionProbe(func(ctx context.Context, _ string, _ ...string) (string, error) {
		close(started)
		<-ctx.Done()
		return "2.1.240", ctx.Err() // Partial output after timeout is not a version result.
	})
	armWithheld(p, longBody(60))
	p.emu.Write([]byte("❯ [Pasted text #1 +58 lines]"))
	returned := make(chan struct{})
	go func() { p.maybeRetryWithheldSubmit(); close(returned) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("lookup not reached")
	}
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("readLoop stalled behind lookup")
	}
	if !p.sendMu.TryLock() {
		t.Fatal("lookup retained the send lock")
	}
	p.sendMu.Unlock()
	ev := awaitVersionSurface(t, events)
	if !strings.Contains(ev.Detail, "measured against 2.1.233") || strings.Contains(ev.Detail, "running ") {
		t.Fatal(ev.Detail)
	}
}

func TestClaudeVersion_FailedOrUnparseableOutputDoesNotInventVersion(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
	}{
		{"2.1.240", errors.New("failed")}, {"unexpected output", nil}, {"", nil}, {"2.1.240\x1b[31m", nil},
	} {
		probe := newClaudeVersionProbe(func(context.Context, string, ...string) (string, error) { return tc.output, tc.err })
		got := make(chan string, 1)
		probe.report(func(s string) { got <- s })
		select {
		case s := <-got:
			if s != "Claude measured against 2.1.233" {
				t.Fatal(s)
			}
		case <-time.After(time.Second):
			t.Fatal("lookup did not report")
		}
	}
}
