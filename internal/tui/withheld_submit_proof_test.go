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
	drainDone := make(chan struct{})
	t.Cleanup(func() { close(drainDone) })
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-drainDone:
				return
			default:
			}
			n, err := p.emu.Read(buf)
			if n > 0 {
				select {
				case submits <- append([]byte(nil), buf[:n]...):
				case <-drainDone:
					return
				}
			}
			if err != nil {
				return
			}
			if n == 0 {
				// AN EMPTY READ MUST NOT SPIN, and this drain must not
				// OUTLIVE its test. Both were true here and cost a red main
				// (ini-a9d8): every cell passed alone, and the accumulated
				// drains from earlier cells were still running during a
				// timing-sensitive eviction test later in the package, which
				// went red with nobody having touched it.
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	return p, submits, events
}

func armWithheld(p *Pane, body string) {
	p.mu.Lock()
	p.pendingSubmit = newPendingSubmit(body, "bracketed", "")
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

// TestProof_ShortFinalLineAloneIsNotProof REPLACES a cell that asserted the
// opposite, and the replacement is the finding (eng2, against 1529dca).
//
// That cell required the composer to hold our short final line and NOTHING
// else, and called the result delivery. But our own paste of "thanks" and the
// operator typing "thanks" are byte-identical on that row -- strictness is not
// disambiguation. The old cell was passing on an assumption, so it had to be
// replaced rather than softened: with only six runes of our body visible there
// is no proof available, and the honest act is to surface.
func TestProof_ShortFinalLineAloneIsNotProof(t *testing.T) {
	p, submits, events := proofPane(t)
	armWithheld(p, "here is the long body of the message\nthanks")

	p.emu.Write([]byte("❯ thanks")) // could be ours; could be the operator's

	p.maybeRetryWithheldSubmit()
	select {
	case b := <-submits:
		t.Fatalf("FORGED SUBMIT (%q). The belt withheld because it suspected a DIALOG "+
			"swallowed our paste, so this is the belt answering a picker on the "+
			"operator's behalf with whatever short word was on the row.", string(b))
	case <-time.After(400 * time.Millisecond):
	}
	assertSurfaced(t, events)
}

// TestProof_TheWholeBodyOnScreenIsProof is the other half, and the reason the
// fix is multi-row corroboration rather than refusing short final lines: when
// the composer shows enough of our body, a coincidence stops being a plausible
// explanation and the message is delivered.
func TestProof_TheWholeBodyOnScreenIsProof(t *testing.T) {
	p, submits, _ := proofPane(t)
	armWithheld(p, "here is the long body of the message\nthanks")

	// Claude puts the first line on the prompt row and the rest beneath it.
	p.emu.Write([]byte("❯ here is the long body of the message\r\nthanks"))

	p.maybeRetryWithheldSubmit()
	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("a composer visibly holding our entire body was not accepted as proof; " +
			"corroborating more than one row is what makes short sign-offs deliverable " +
			"at all, and without it every such message surfaces")
	}
}

// TestProof_ABodyTooShortToProveSurfaces is super's fallback: where there is
// no evidence to corroborate WITH -- a body that is just "ok" -- multi-row
// proof degrades silently back into the assumption. That case surfaces.
//
// It costs delivery on the briefest messages, and that is the trade taken
// deliberately: the operator is told, and told what to re-send.
func TestProof_ABodyTooShortToProveSurfaces(t *testing.T) {
	p, submits, events := proofPane(t)
	armWithheld(p, "ok")

	p.emu.Write([]byte("❯ ok"))

	p.maybeRetryWithheldSubmit()
	select {
	case b := <-submits:
		t.Fatalf("submitted (%q) on a two-rune body: there is nothing on that screen "+
			"that our paste can produce and the operator cannot", string(b))
	case <-time.After(400 * time.Millisecond):
	}
	assertSurfaced(t, events)
}

// TestProof_RequiresTheTransition pins that a placeholder already present when
// we withheld is not evidence of our paste.
func TestProof_RequiresTheTransition(t *testing.T) {
	p, submits, _ := proofPane(t)
	// The placeholder was ALREADY there when we withheld.
	p.mu.Lock()
	p.pendingSubmit = newPendingSubmit(longBody(60), "bracketed", "[Pasted text #1 +59 lines]")
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

	p.mu.Lock()
	p.pendingSubmit = newPendingSubmit(longBody(60), "bracketed", "")
	p.pendingSubmit.deadline = time.Now().Add(30 * time.Second)
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

// TestProof_EvidenceBelowThePromptRowCounts is what makes the multi-row read
// load-bearing rather than decorative.
//
// Claude puts the first line of a body on the prompt row and the rest beneath
// it, so a body that opens with a short line -- a greeting, a name, a bead id
// -- has almost none of its evidence on that row. Reading one row would
// surface it; reading the block proves it. Every other delivery cell here
// would pass with the block reader gutted, because their first line is long
// enough on its own.
func TestProof_EvidenceBelowThePromptRowCounts(t *testing.T) {
	p, submits, _ := proofPane(t)
	armWithheld(p, "hi\nhere is the long body of the message")

	p.emu.Write([]byte("❯ hi\r\nhere is the long body of the message"))

	p.maybeRetryWithheldSubmit()
	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("a body whose evidence sits BELOW the prompt row was not proven; any " +
			"message opening with a short line is undeliverable when only one row is read")
	}
}

// TestProof_AWrappedLineIsStillOurLine pins why the rows are joined with no
// separator: wrapping splits a line across rows without inserting anything, so
// concatenating rebuilds it. Joining with a newline would break every message
// containing a line longer than the pane is wide -- paths, URLs, commands.
func TestProof_AWrappedLineIsStillOurLine(t *testing.T) {
	p, submits, _ := proofPane(t)
	long := "/Users/nmelo/Desktop/Projects/initech/" + strings.Repeat("deep-path-segment/", 12)
	armWithheld(p, long)

	p.emu.Write([]byte("❯ " + long)) // 120-column pane: this wraps

	p.maybeRetryWithheldSubmit()
	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("a line that WRAPPED was not recognised as ours; the composer is showing " +
			"our text and the proof cannot see it because the rows were rejoined wrong")
	}
}

// TestProof_ALineThatWrapsOnASpaceIsStillOurLine is eng2's finding against
// bc92fdb, and it is the one wrap that the no-separator join does NOT survive.
//
// Wrapping splits a line without inserting anything -- true -- but composerBlock
// must TrimSpace every row, because RowText pads to the pane width. When the
// split lands exactly on a space, the trim then DELETES a character the wrap
// left in place, and concatenation no longer rebuilds the line. Mid-word wraps
// are unaffected, which is why every other cell here passes.
//
// It fails safe, and it still had to be fixed: provenRunes is all-or-nothing
// per line, so a SINGLE-LINE body that wraps this way proves zero runes and
// surfaces -- telling the operator a message was not delivered while it sits
// plainly on their screen. They re-send, and the agent now has it twice. One
// long line, far under the collapse threshold, is most of what this fleet
// sends; the messages in this review are that shape.
func TestProof_ALineThatWrapsOnASpaceIsStillOurLine(t *testing.T) {
	// The pane is 120 columns and the prompt takes two, so the 119th rune of
	// the body lands at the wrap boundary. Put the space there.
	body := strings.Repeat("a", 118) + " tail-words-after-the-wrap"
	for _, pad := range []int{0, 1} { // straddle any off-by-one in the wrap
		p, submits, _ := proofPane(t)
		b := strings.Repeat("a", 118+pad) + " tail-words-after-the-wrap"
		if pad == 0 {
			b = body
		}
		armWithheld(p, b)
		p.emu.Write([]byte("❯ " + b))

		p.maybeRetryWithheldSubmit()
		select {
		case <-submits:
		case <-time.After(time.Second):
			t.Fatalf("a single-line body that wrapped ON A SPACE (pad %d) proved nothing, "+
				"so the operator is told a message failed while it is visibly in the "+
				"composer -- and a re-send delivers it twice", pad)
		}
	}
}
