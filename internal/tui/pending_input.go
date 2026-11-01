package tui

import (
	"strconv"
	"strings"
)

// queuedMessagesHint is what Claude Code prints in the input box while
// messages it has queued wait for the turn to end (MEASURED, 2.1.312, ini-dmi):
// its own words, not text anyone typed.
const queuedMessagesHint = "Press up to edit queued messages"

// pendingInputText returns the first line of text sitting unsent in the
// input box, and whether there is any (ini-b5w): a delivered message whose
// Enter was not taken, a half-typed line, a paste waiting.
//
// THE PROMPT ROW MUST BE THE INPUT BOX'S OWN. Claude's option pickers use the
// same ❯ as their selection cursor ("❯ 1. Yes", and the trust dialog's
// "❯ No, exit", which is not numbered), so a ❯ row anywhere on screen is not
// evidence. The row read is the one right under the box's top border, found
// by composerFrame, the frame the footer, the dialog guard and the send
// verdict share: any pane width, a plain or a named border. A dialog draws
// no input box, so it fails here before any word is read. A numbered option
// (numberedOption) and Claude's queued-messages hint are excluded on top.
//
// MEASURED (Claude Code 2.1.312, Haiku, 32 and 80 columns): an empty box is
// a bare "❯"; a box holding text reads "❯", a no-break space, then the text,
// for a typed line and a pasted block alike. Only the box's first row is
// read: a multi-line paste starts there.
//
// The caller rules out a dialog on screen or latched; this reads the box
// only.
func pendingInputText(rows []string, width int) (string, bool) {
	top, _, ok := composerFrame(rows, width)
	if !ok {
		return "", false
	}
	row := strings.TrimLeft(rows[top+1], " ")
	// TrimSpace takes the no-break space too (unicode.IsSpace).
	text := strings.TrimSpace(strings.TrimPrefix(row, "❯"))
	if text == "" || numberedOption(row) || text == queuedMessagesHint {
		return "", false
	}
	return text, true
}

// observePendingInput records the text left unsent in the input box at this
// tick, or none (ini-b5w). Nothing is pending while a dialog is on screen or
// latched, or while a banner (a trust or login prompt) is up: those panes
// keep their own reading.
func (p *Pane) observePendingInput(rows []string, cols int) {
	text, ok := pendingInputText(rows, cols)
	if ok && (screenShowsModal(rows) || p.dialogLatched() || bannerKind(rows, false) != bannerNone) {
		text = ""
	}
	p.mu.Lock()
	p.pendingInput = text
	p.mu.Unlock()
}

// PendingInput returns the first line of text left unsent in the pane's
// input box at the last maintenance tick, "" when none (ini-b5w).
func (p *Pane) PendingInput() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pendingInput
}

// LeftInBoxPrefix opens the note a delivered verdict carries when the box
// still holds other text (ini-b5w); the CLI prints the note only when it
// starts so, since a woken agent's delivered answer carries its own data.
const LeftInBoxPrefix = "the box still holds other text: "

// leftInBoxNote is what a delivered verdict adds when, once our message has
// left the box, the box holds other text by the same reading the pane state
// uses (ini-b5w): a restored draft, a half-typed line. The pane then reads
// pending input, and the sender is told rather than left to find it. "" when
// the box is empty or a dialog is open.
//
// NEVER WAITS ON THE PANE (ini-psjt): the screen and its width come through
// tryScreenRows and emuSize, the readers that give up when the emulator lock
// is held. A note skipped that way is the one cost: the pane itself reads
// pending input at the next maintenance tick.
func leftInBoxNote(p *Pane) string {
	if paneHasModal(p) {
		return ""
	}
	rows, read := tryScreenRows(p)
	if !read {
		return ""
	}
	cols, _ := p.emuSize()
	text, ok := pendingInputText(rows, cols)
	if !ok {
		return ""
	}
	return LeftInBoxPrefix + strconv.Quote(logSnippet(text))
}
