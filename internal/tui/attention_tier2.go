package tui

// attention_tier2.go is the coverage tier: screen-content detection for panes
// that have no tier-1 signal at all (ini-2x8.5).
//
// TIER-2 IS LIST-ONLY AND CANNOT EARN A CHIME BY ANY AMOUNT OF PATTERN WORK.
// That is not caution, it is what the capture measured: a 13987-byte codex
// capture with TWO blocking dialogs open in it contains zero OSC 777 and zero
// OSC 9. There is nothing for tier-1 to hear from codex, so its state is
// inferred rather than declared -- and inference does not get to interrupt the
// operator. The row says exactly "dialog detected", because claiming more than
// the heuristic knows would be a lie in a box whose whole job is being trusted.
//
// READS RENDERED ROWS, NEVER RAW PTY BYTES. Codex positions each WORD
// separately, so its dialog phrases are not contiguous in the byte stream --
// measured: "Do you trust", "trust the contents", "Press enter to continue" and
// "Yes, continue" all fail a raw-byte substring test while every one of them
// passes against the emulator's rendered rows. Anyone rewriting this into a
// raw-stream scan for speed would silently break codex detection, which is why
// there is a test pinning it.

import "strings"

// tier2DialogPatterns are lowercased, whitespace-compacted substrings that mark
// a blocking dialog on a pane with no tier-1 signal.
//
// EVERY ENTRY HERE HAS A CAPTURED SPECIMEN behind it, recorded on ini-2x8 or
// ini-2x8.5 at a named version. The legacy modal_detect.go list is deliberately
// NOT inherited wholesale: of its five patterns, only three have ever been seen
// in a real captured dialog, and carrying the other two here would dress
// guesswork up as coverage.
var tier2DialogPatterns = []string{
	// codex 0.145.0 directory-trust gate. Captured verbatim; this one wedges a
	// fresh codex pane before it does any work at all.
	"do you trust the contents of this directory",
	"1. yes, continue",

	// codex 0.145.0 hook-review gates, both surfaces.
	"hooks need review",
	"continue without trusting",
	"press t to trust",

	// Generic confirm footers, each with a specimen: codex trust says
	// "Press enter to continue", codex hook review says "Press enter to
	// confirm or esc to go back".
	"press enter to continue",
	"press enter to confirm",

	// Claude Code's two dialog kinds, captured at 2.1.229. Present so a Claude
	// pane still surfaces in the list if its tier-1 signal ever goes missing --
	// list-only and silent, which is the honest degradation.
	"do you want to proceed",
	"to navigate",
}

// tier2PreviewText is what a tier-2 row says. A fixed string on purpose: the
// heuristic knows that SOMETHING is blocking, and nothing more. The spec names
// this exact wording.
const tier2PreviewText = "dialog detected"

// hasTier2Dialog reports whether rendered screen text carries a blocking-dialog
// signature. text must come from the emulator's rendered rows -- see the file
// comment for why raw PTY bytes do not work.
func hasTier2Dialog(text string) bool {
	compacted := compactPromptText(strings.ToLower(strings.ReplaceAll(text, "’", "'")))
	for _, pattern := range tier2DialogPatterns {
		if strings.Contains(compacted, compactPromptText(pattern)) {
			return true
		}
	}
	return false
}

// tier2Eligible reports whether this pane's waiting state may be driven by the
// screen heuristic.
//
// Panes WITH a tier-1 signal are excluded, and the exclusion is the interesting
// half: for a Claude pane, OSC 777 is authoritative about both edges, so letting
// the screen also raise it would put an inferred signal into the chime path
// through the back door.
func tier2Eligible(p *Pane) bool {
	if p == nil {
		return false
	}
	return !p.hasTier1Signal()
}

// refreshTier2WaitingState drives the waiting state for a tier-2 pane. Called
// per render tick for panes that have no tier-1 signal.
func (p *Pane) refreshTier2WaitingState() {
	if !tier2Eligible(p) {
		return
	}
	if !p.alivePane() {
		p.ClearWaitingInput()
		return
	}

	if paneHasTier2Dialog(p) {
		// Silent tier, explicitly. Passing WaitingTierListOnly rather than
		// relying on the zero value keeps the intent legible at the call site.
		p.SetWaitingInputTier(tier2PreviewText, WaitingTierListOnly)
		return
	}

	// Symmetric with tier-1's rule: the screen raised this row, so the screen is
	// entitled to retire it. No confirmation gate is needed here because the same
	// patterns govern both edges -- if they could see it to raise it, they can
	// see it go.
	if waiting, _, _ := p.WaitingInput(); waiting {
		p.ClearWaitingInput()
	}
}

// paneHasTier2Dialog reads the pane's rendered bottom rows and tests them.
// Uses emulatorBottomText (SafeEmulator.RowText, which copies under the lock)
// rather than CellAt, which returns a pointer into the live buffer and races a
// concurrent Write.
func paneHasTier2Dialog(p *Pane) bool {
	if p == nil || p.emu == nil {
		return false
	}
	return hasTier2Dialog(emulatorBottomText(p.emu, modalScanRows))
}
