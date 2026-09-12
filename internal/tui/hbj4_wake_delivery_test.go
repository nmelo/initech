package tui

// Wake-on-message must deliver into a LISTENING process (ini-hbj4).
//
// Live-measured on the operator's fleet: a control send to a live pane
// delivers; the wake path lost the message 2/2 -- logged as delivered, never
// in the pane, never processed. resumePane drained right after waitForInit,
// which returns on the FIRST byte of output; Claude Code with a large
// --continue transcript is still rendering then and flushes stdin at raw-mode
// entry, eating the paste. Worse as transcripts grow.
//
// The fix (quiescence gate + event-truth split) was landed and reverted four
// times in August: linux/windows CI red on these cells, darwin green -- and
// the darwin greens were under a doubt named in the trail (the fixture's
// reader was plain cat, satisfiable by terminal echo). Those platforms are
// untested by ruling now (ini-ibsm); what remains is the ORIGINAL darwin loss,
// and the first job here is an instrument that can see it honestly.

import (
	"strings"
	"testing"
	"time"
)

// TestResumePane_FixtureDistinguishesDeliveredFromEchoed is super's first
// item, and every green in this file rests on it: PROVE, on this platform,
// that the assertion cannot be satisfied by terminal echo.
//
// The child turns tty echo ON deliberately, prints one line (so waitForInit
// returns on it instead of burning its 30s), and then never reads stdin. A
// delivered probe therefore appears on screen as ECHO -- exactly the confound
// -- but GOT: cannot, because nothing read the line to print it. Both halves
// are asserted: if the bare probe did NOT appear, echo would not be live and
// the cell would prove nothing about the confound.
//
// MEASURED while writing this: with no stty at all, the bare probe did NOT
// echo -- the pane's PTY starts with echo off before the child does anything.
// So the confound the trail feared is not the product's default state; this
// cell manufactures it on purpose, which is the only way to show the
// assertion survives it.
func TestResumePane_FixtureDistinguishesDeliveredFromEchoed(t *testing.T) {
	tui := newTestTUI()
	parked := NewParkedPane(PaneConfig{Name: "eng9", Command: []string{"sh", "-c", "stty echo; echo ready; sleep 30"}}, 20, 60)
	tui.panes = append(tui.panes, parked)
	parked.EnqueueMessage("PROBE-ECHO-ONLY", true)

	if err := tui.resumePane(parked, "test"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	np := tui.panes[0].(*Pane)
	defer np.Close()

	sawBare, sawGot := false, false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		np.renderMu.Lock()
		text := emulatorBottomTextBlocking(np.emu, np.emu.Height())
		np.renderMu.Unlock()
		sawBare = sawBare || strings.Contains(text, "PROBE-ECHO-ONLY")
		sawGot = sawGot || strings.Contains(text, "GOT:PROBE-ECHO-ONLY")
		if sawGot {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !sawBare {
		t.Fatal("the bare probe never appeared even with the child forcing tty echo ON and " +
			"never reading: echo cannot be made live on this PTY, so this cell cannot " +
			"demonstrate the confound it exists to rule out -- re-establish the " +
			"discriminator some other way before trusting any GOT: green in this file")
	}
	if sawGot {
		t.Fatal("GOT: appeared with NO reader: the prefix is being produced by something " +
			"other than the child reading the line, and the assertion is not confound-immune")
	}
}

// Live-measured (operator-requested verification, 2026-08-21): a control
// send to a live pane delivered; the wake path lost the message 2/2 —
// resumePane drained right after waitForInit, which fires on FIRST output,
// while Claude Code with a large --continue transcript is still booting and
// flushes stdin at raw-mode entry. The fixture reproduces that shape
// faithfully (real-workload doctrine): boot chatter long enough to make
// first-output readiness a lie, an explicit stdin flush at the readiness
// boundary, then a child that echoes what it reads.
func TestResumePane_QueuedMessageSurvivesSlowBoot(t *testing.T) {
	tui := newTestTUI()
	// THE READER IS THE INSTRUMENT (ini-hbj4 re-dispatch). The reverted
	// version's reader was plain `cat` and the assertion was "the probe
	// appeared" -- which TERMINAL ECHO satisfies with the child having read
	// nothing, if stty -echo no-ops on this PTY. Every darwin green in the
	// arc sat under that doubt. The reader now prepends GOT: and the
	// assertion requires it: content only a child that READ the line can
	// produce. TestResumePane_FixtureDistinguishesDeliveredFromEchoed proves
	// the discriminator on this platform before this cell is believed.
	// stty -echo stays as fidelity (Claude runs raw/no-echo); the assertion
	// no longer depends on it working.
	// The flush runs CONCURRENTLY with the chatter, deliberately: a child that
	// flushes AFTER going quiet cannot be defended by a quiescence gate at all
	// (quiescence would fire mid-flush), and that shape is not Claude's — Claude
	// flushes at raw-mode entry while still rendering. Modelling it concurrently
	// makes the margin STRUCTURAL (delivery lands resumeQuiesceStable after the
	// last output, the flush ended with it) instead of the ~200ms accident that
	// passed on darwin and lost the race on CI's runners.
	boot := `stty -echo
(for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do echo booting-$i; sleep 0.1; done) &
while read -t 1 -r junk; do :; done
wait
while read -r l; do echo "GOT:$l"; done`
	parked := NewParkedPane(PaneConfig{Name: "eng9", Command: []string{"bash", "-c", boot}}, 20, 60)
	tui.panes = append(tui.panes, parked)
	parked.EnqueueMessage("PROBE-SURVIVES-BOOT", true)

	if err := tui.resumePane(parked, "test"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	np := tui.panes[0].(*Pane)
	defer np.Close()

	// Generous window: CI runners are slower and loaded, and this test's claim
	// is "the message ARRIVES", not "within 8s" — a tight poll deadline turns a
	// slow runner into a false silent-loss report.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		np.renderMu.Lock()
		text := emulatorBottomTextBlocking(np.emu, np.emu.Height())
		np.renderMu.Unlock()
		if strings.Contains(text, "GOT:PROBE-SURVIVES-BOOT") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("queued message never reached the child — it was delivered into boot and flushed, " +
		"the silent-loss-through-the-recovery-path shape g7fl exists to end")
}

// The twin of the slow-boot cell, and the one that would have caught round 1
// in the FAST suite instead of an env-gated rig CI does not run (shipper's
// 9IMX bisect, ini-hbj4 round 2): a child that produces NO output has no boot
// to settle, so the quiescence gate must not apply to it. Round 1 waited the
// full 15s cap on silent children and pushed the auto-resume path past the
// suspend-guard rig's deadline. Asserted as a DEADLINE, since the defect was
// purely one of elapsed time — delivery always did happen, just too late.
func TestResumePane_SilentChildDeliversPromptly(t *testing.T) {
	// Costs waitForInit's full 30s: a mute child never produces the first byte
	// that ends it. That timeout is PRE-EXISTING and out of this bead's scope
	// — the assertion below is scoped to the gate's own contribution. Excluded
	// from -short so the fast suite stays fast; it runs in make test-full and
	// in CI's full-suite leg, which is already a wider net than the env-gated
	// rig that caught round 1.
	if testing.Short() {
		t.Skip("30s: waitForInit's timeout on a mute child; runs in the full suite")
	}
	tui := newTestTUI()
	// Mute until spoken to, then echo — the rig fixture's shape exactly.
	parked := NewParkedPane(PaneConfig{
		Name:    "eng9",
		Command: []string{"sh", "-c", `stty -echo; while read l; do echo "GOT:$l"; done`},
	}, 20, 60)
	tui.panes = append(tui.panes, parked)
	parked.EnqueueMessage("PROBE-SILENT", true)

	start := time.Now()
	if err := tui.resumePane(parked, "test"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	np := tui.panes[0].(*Pane)
	defer np.Close()

	// Poll well past the budget: the ASSERTION is the elapsed-time budget
	// below, so the poll window only needs to be long enough to observe
	// arrival on a loaded CI runner. A tight window reported "never received"
	// on CI's full-suite leg for a message that was merely late.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		np.renderMu.Lock()
		text := emulatorBottomTextBlocking(np.emu, np.emu.Height())
		np.renderMu.Unlock()
		if strings.Contains(text, "GOT:PROBE-SILENT") {
			// Scoped to the GATE's contribution: waitForInit's 30s is
			// pre-existing and unavoidable for a mute child, so the budget is
			// that plus a small margin. Round 1 (gate applied to silent
			// children) landed at ~45s and reds here; the fix lands at ~30s.
			budget := resumeTimeout + 5*time.Second
			if elapsed := time.Since(start); elapsed > budget {
				t.Fatalf("silent child delivered after %v (budget %v) — it burned the quiescence "+
					"cap it has no boot output to satisfy", elapsed, budget)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("silent child never received its queued message")
}

// TWO QUESTIONS, TWO ANSWERS (shipper's f5bab05 review finding): "did the
// child speak?" gates the quiescence WAIT; "did we deliver messages?" gates
// the resume EVENT's text. Round 2 applied one edit to both textually
// identical predicates, so a silent child's messages were delivered and the
// event stayed quiet about it — while the log line beside it still reported
// them, leaving two surfaces disagreeing about one wake. On the release whose
// notes say delivered means delivered, under-reporting a delivery is the
// wrong direction to be wrong in.
func TestResumePane_EventReportsDeliveryForSilentChild(t *testing.T) {
	if testing.Short() {
		t.Skip("30s: waitForInit's timeout on a mute child; runs in the full suite")
	}
	tui := newTestTUI()
	tui.agentEvents = make(chan AgentEvent, 64)
	parked := NewParkedPane(PaneConfig{
		Name:    "eng9",
		Command: []string{"sh", "-c", `stty -echo; while read l; do echo "GOT:$l"; done`},
	}, 20, 60)
	tui.panes = append(tui.panes, parked)
	parked.EnqueueMessage("PROBE-EVENT", true)

	if err := tui.resumePane(parked, "super"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	defer tui.panes[0].(*Pane).Close()

	select {
	case ev := <-tui.agentEvents:
		if !strings.Contains(ev.Detail, "delivered 1 queued") {
			t.Fatalf("resume event = %q; the message WAS delivered, so the event must say so — "+
				"a delivery the event stays quiet about is the same class of untruth as a "+
				"loss the log calls delivered", ev.Detail)
		}
	default:
		t.Fatal("no resume event emitted")
	}
}
