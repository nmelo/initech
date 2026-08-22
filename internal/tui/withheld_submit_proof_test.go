package tui

// DELIBERATELY UNTAGGED, and NOT because another platform runs these: as of
// ini-ibsm macOS is the only tested platform, so a !windows tag would cost no
// CI run today. It is untagged because os.Pipe and the emulator are portable
// and no cell here touches a tty -- a build constraint that nothing requires
// is a false statement about where the code works, and it hides the file from
// the cross-platform census that would otherwise notice the asymmetry. If a
// platform leg ever returns, these run unchanged.

// What a repainted composer PROVES (ini-vpwg repair, eng1 on eng2's design).
//
// Every cell here is a defect that was reproduced against the landed code
// before it was fixed. The design's shape -- arm on withhold, resolve on
// output, three branches, submit-only-never-body -- is eng2's and unchanged;
// these are repairs to the PROOF.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// proofPane builds a pane whose composer and submit channel a test can watch.
// The submit channel carries a POSITIVE CONTROL requirement: a cell asserting
// "no submit happened" is worthless unless the same instrument is shown to see
// a submit when one does happen (eng2's double-delivery test survived its
// mutant for want of exactly this, on a canonical-mode tty).
func proofPane(t *testing.T) (*Pane, <-chan []byte, <-chan AgentEvent) {
	t.Helper()
	p, _ := paneWithPipe(t)
	p.name = "eng2"
	p.alive = true
	p.emu = vt.NewSafeEmulator(120, 40)
	events := make(chan AgentEvent, 8)
	p.eventCh = events

	submits := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := p.emu.Read(buf)
			if n > 0 {
				submits <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	return p, submits, events
}

func armWithheld(p *Pane, body string) {
	p.mu.Lock()
	p.pendingSubmit = &pendingSubmit{
		marker:         submitMarker(body),
		lines:          countBodyLines(body),
		preview:        "preview",
		mode:           "bracketed",
		tailAtWithhold: "",
		withheldAt:     time.Now(),
		deadline:       time.Now().Add(time.Minute),
	}
	p.mu.Unlock()
}

func longBody(lines int) string {
	var b []string
	for i := 0; i < lines; i++ {
		b = append(b, "line number "+strings.Repeat("x", 40))
	}
	return strings.Join(b, "\n")
}

// TestProof_CollapsedPasteIsOurs is finding 1. Claude COLLAPSES a large paste,
// so our text is nowhere on the tail row; the landed proof read that as
// "somebody else's" and surfaced EVERY long message.
//
// The count is the measured convention: a 60-line body renders "+59 lines".
func TestProof_CollapsedPasteIsOurs(t *testing.T) {
	p, submits, _ := proofPane(t)
	body := longBody(60)
	armWithheld(p, body)

	p.emu.Write([]byte("❯ [Pasted text #1 +59 lines]"))
	p.maybeRetryWithheldSubmit()

	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("our own paste, collapsed by Claude, was not recognised as ours.\n\n" +
			"Every message past the collapse threshold takes the surface path and is " +
			"never delivered -- which is most of what this fleet sends.")
	}
}

// TestProof_CollapsedPasteOfAnotherSizeIsNotOurs keeps the corroboration
// honest: the placeholder proves A paste landed, the count proves it was OURS.
func TestProof_CollapsedPasteOfAnotherSizeIsNotOurs(t *testing.T) {
	p, submits, events := proofPane(t)
	armWithheld(p, longBody(60))

	// Somebody else's much larger paste is sitting in the composer.
	p.emu.Write([]byte("❯ [Pasted text #2 +400 lines]"))
	p.maybeRetryWithheldSubmit()

	select {
	case b := <-submits:
		t.Fatalf("submitted (%q) into a composer holding a paste of a different size", string(b))
	case <-time.After(400 * time.Millisecond):
	}
	assertSurfaced(t, events)
}

// TestProof_ShortFinalLineDoesNotForgeASubmit is finding 2, in the shape eng2
// narrowed it to: the trigger is a short FINAL LINE, not a short message, and
// fleet messages routinely end with a short sign-off on its own line.
func TestProof_ShortFinalLineDoesNotForgeASubmit(t *testing.T) {
	p, submits, events := proofPane(t)
	armWithheld(p, "here is the long body of the message\nthanks")

	// The operator is typing their own line, which happens to contain "thanks".
	p.emu.Write([]byte("❯ no thanks, I will do it myself"))
	p.maybeRetryWithheldSubmit()

	select {
	case b := <-submits:
		t.Fatalf("FORGED SUBMIT (%q): the operator's half-written line was sent because "+
			"it contained our short final line. That is the harm the belt exists to "+
			"prevent, produced by the fix for a dropped message.", string(b))
	case <-time.After(400 * time.Millisecond):
	}
	assertSurfaced(t, events)
}

// TestProof_ShortFinalLineStillDeliversWhenItIsTheWholeTail is the other half:
// the floor must not silently stop delivering brief messages. A short marker
// is held to a STRICTER test, not refused.
func TestProof_ShortFinalLineStillDeliversWhenItIsTheWholeTail(t *testing.T) {
	p, submits, _ := proofPane(t)
	armWithheld(p, "here is the long body of the message\nthanks")

	p.emu.Write([]byte("❯ thanks"))
	p.maybeRetryWithheldSubmit()

	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("a brief message whose text IS the whole composer was not delivered; the " +
			"marker floor has turned into a refusal to deliver short messages")
	}
}

// TestProof_RequiresTheTransition pins that a placeholder already present when
// we withheld is not evidence of our paste.
func TestProof_RequiresTheTransition(t *testing.T) {
	p, submits, _ := proofPane(t)
	body := longBody(60)
	p.mu.Lock()
	p.pendingSubmit = &pendingSubmit{
		marker: submitMarker(body), lines: countBodyLines(body),
		preview: "preview", mode: "bracketed",
		tailAtWithhold: "[Pasted text #1 +59 lines]", // already there when we withheld
		withheldAt:     time.Now(),
		deadline:       time.Now().Add(time.Minute),
	}
	p.mu.Unlock()

	p.emu.Write([]byte("❯ [Pasted text #1 +59 lines]"))
	p.maybeRetryWithheldSubmit()

	select {
	case b := <-submits:
		t.Fatalf("submitted (%q) on a composer that never changed; a leftover placeholder "+
			"is not evidence that OUR paste landed", string(b))
	case <-time.After(400 * time.Millisecond):
	}
}

// TestProof_HeldSendLockDefersRatherThanStalling is finding 3's decision: the
// retry takes sendMu across check AND write, and uses TryLock so a busy sender
// defers the retry instead of stalling the readLoop.
func TestProof_HeldSendLockDefersRatherThanStalling(t *testing.T) {
	p, submits, _ := proofPane(t)
	body := longBody(60)
	armWithheld(p, body)
	p.emu.Write([]byte("❯ [Pasted text #1 +59 lines]"))

	p.sendMu.Lock() // a concurrent sender is mid-paste
	done := make(chan struct{})
	go func() { p.maybeRetryWithheldSubmit(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		p.sendMu.Unlock()
		t.Fatal("the retry BLOCKED on sendMu from the readLoop path; that stalls the " +
			"emulator feed and freezes the pane's rendering for as long as a sender holds " +
			"the lock")
	}
	select {
	case b := <-submits:
		p.sendMu.Unlock()
		t.Fatalf("submitted (%q) while another sender held sendMu -- the submit can land "+
			"inside somebody else's half-written body", string(b))
	case <-time.After(300 * time.Millisecond):
	}

	// POSITIVE CONTROL: the same instrument must see a submit once the lock is
	// free, or "no submit" above proves nothing about the code.
	p.sendMu.Unlock()
	p.maybeRetryWithheldSubmit()
	select {
	case <-submits:
	case <-time.After(2 * time.Second):
		t.Fatal("instrument check failed: no submit was seen even with the lock free, so " +
			"the absence assertions above measured nothing")
	}
}

func assertSurfaced(t *testing.T, events <-chan AgentEvent) {
	t.Helper()
	select {
	case ev := <-events:
		if !strings.Contains(ev.Detail, "NOT delivered") {
			t.Errorf("event reads %q; the operator must be told the message was not sent", ev.Detail)
		}
	case <-time.After(time.Second):
		t.Error("neither submitted nor surfaced: the message is held with no holder, which " +
			"is the defect this bead exists to close")
	}
}

// TestProof_QuietPaneStillResolves is finding 4 (eng2, against their own
// code), and it is the cell my reachability reproduction structurally could
// not have produced: that measurement is a BUSY agent, which keeps producing
// output and therefore keeps re-entering the resolver. The hole is only
// visible when the pane says nothing at all.
//
// It drives t.modalMaintenance, not the sweep directly. The claim is "a quiet
// pane resolves", which is false unless the sweep is actually WIRED to a tick
// that runs without output -- calling the sweep by hand would assert the
// function body and leave the claim untested.
func TestProof_QuietPaneStillResolves(t *testing.T) {
	p, submits, events := proofPane(t)
	tui := &TUI{panes: []PaneView{p}, quitCh: make(chan struct{})}

	body := longBody(60)
	p.mu.Lock()
	p.pendingSubmit = &pendingSubmit{
		marker: submitMarker(body), lines: countBodyLines(body),
		preview: "preview", mode: "bracketed", tailAtWithhold: "",
		withheldAt: time.Now(),
		deadline:   time.Now().Add(30 * time.Second),
	}
	p.mu.Unlock()

	// The pane produces NO output from here on. Not one byte.

	// Before the deadline the tick must leave it alone: a bound that fires
	// early steals messages the composer could still prove.
	tui.modalMaintenance(time.Now())
	select {
	case ev := <-events:
		t.Fatalf("resolved before its deadline: %q", ev.Detail)
	case <-time.After(200 * time.Millisecond):
	}

	// Past the deadline, with the pane still silent.
	tui.modalMaintenance(time.Now().Add(31 * time.Second))
	select {
	case ev := <-events:
		if !strings.Contains(ev.Detail, "NOT delivered") {
			t.Fatalf("event reads %q; expected the undelivered report", ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A QUIET PANE HELD THE MESSAGE FOREVER.\n\n" +
			"The deadline is only evaluated when output arrives, so an agent that " +
			"stalls, is killed, or simply goes idle after the withhold is never " +
			"delivered to AND never reported -- which is the defect this bead exists " +
			"to close, reintroduced by its own resolution path.")
	}

	// And it must not have guessed a submit out of a silent composer.
	select {
	case b := <-submits:
		t.Fatalf("submitted (%q) into a pane that produced no output at all; there is "+
			"no proof available on a silent pane, only a report", string(b))
	default:
	}
}

// TestProof_OneMessageIsResolvedOnce closes the race the second resolver
// creates: the sweep and the output hook must not both act on one message, or
// the operator is told a message failed while it is being delivered.
func TestProof_OneMessageIsResolvedOnce(t *testing.T) {
	p, _, events := proofPane(t)
	tui := &TUI{panes: []PaneView{p}, quitCh: make(chan struct{})}

	body := longBody(60)
	armWithheld(p, body)
	p.mu.Lock()
	ps := p.pendingSubmit
	ps.deadline = time.Now().Add(-time.Second) // already past
	p.mu.Unlock()

	p.emu.Write([]byte("❯ [Pasted text #1 +59 lines]")) // deliverable
	tui.modalMaintenance(time.Now())                    // sweep surfaces it
	p.maybeRetryWithheldSubmit()                        // output hook, same message

	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("instrument check: no event at all, so the count below proves nothing")
	}
	select {
	case ev := <-events:
		t.Fatalf("one message produced a second resolution (%q): the operator is told a "+
			"message failed while the other path delivers it", ev.Detail)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestProof_AMessageIsClaimedByExactlyOneResolver pins the compare-and-clear
// itself.
//
// Written because the end-to-end cell above could NOT see it: once the sweep
// clears pendingSubmit, the output hook returns at its own nil check, so a
// mutant that made the claim always succeed survived. That is the reachability
// rule -- a mutant only dies where the test actually arrives -- so this cell
// arrives at the claim directly, holding the stale pointer both racing
// resolvers would be holding.
func TestProof_AMessageIsClaimedByExactlyOneResolver(t *testing.T) {
	p, _, events := proofPane(t)
	armWithheld(p, longBody(60))
	p.mu.Lock()
	ps := p.pendingSubmit
	p.mu.Unlock()

	if !p.takePendingSubmit(ps) {
		t.Fatal("the first resolver could not claim a message it holds")
	}
	if p.takePendingSubmit(ps) {
		t.Error("two resolvers both claimed one message: one delivers it while the other " +
			"tells the operator it failed")
	}

	// And the surface path must honour the claim rather than re-report.
	p.surfaceUndeliveredSubmit(ps, "second resolver, already-claimed message")
	select {
	case ev := <-events:
		t.Errorf("a claimed message was reported again: %q", ev.Detail)
	case <-time.After(300 * time.Millisecond):
	}

	// POSITIVE CONTROL: the same instrument must see a report for a message
	// that IS still held, or the silence above proves nothing.
	armWithheld(p, longBody(60))
	p.mu.Lock()
	live := p.pendingSubmit
	p.mu.Unlock()
	p.surfaceUndeliveredSubmit(live, "control")
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("instrument check failed: no report even for a held message")
	}
}
