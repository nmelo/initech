// paste.go implements buffered paste handling for the TUI. When the terminal
// delivers a bracketed paste (EventPaste start, N x EventKey, EventPaste end),
// characters are accumulated in pasteBuf and written to the focused pane's PTY
// in a single call. This turns O(N) renders into O(1) for large pastes.
package tui

import (
	"github.com/gdamore/tcell/v2"
)

// handlePaste processes EventPaste events. On start, it begins buffering.
// On end, it flushes the accumulated buffer to the focused pane's PTY.
func (t *TUI) handlePaste(start bool) {
	if start {
		t.pasting = true
		t.pasteBuf = t.pasteBuf[:0]
		return
	}

	// Paste end: flush and reset.
	t.pasting = false
	defer func() { t.pasteBuf = t.pasteBuf[:0] }()

	if len(t.pasteBuf) == 0 {
		return
	}

	// Drop paste if a modal is active. Modals expect typed input, not bulk paste.
	if t.modalActive() {
		return
	}

	fp := t.focusedPane()
	if fp == nil {
		return
	}
	lp, ok := fp.(*Pane)
	if !ok {
		// Remote panes don't support paste flush (no local ptmx).
		return
	}

	lp.FlushPaste(t.pasteBuf)
}

// bufferPasteKey appends a key event's character to the paste buffer.
// Called from handleEvent when pasting is true.
func (t *TUI) bufferPasteKey(ev *tcell.EventKey) {
	if ev.Key() == tcell.KeyRune {
		// Encode the rune as UTF-8.
		t.pasteBuf = appendRune(t.pasteBuf, ev.Rune())
		return
	}
	// Non-rune keys that appear in paste content.
	switch ev.Key() {
	case tcell.KeyEnter:
		t.pasteBuf = append(t.pasteBuf, '\r')
	case tcell.KeyTab:
		t.pasteBuf = append(t.pasteBuf, '\t')
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		t.pasteBuf = append(t.pasteBuf, '\x7f')
	case tcell.KeyEscape:
		t.pasteBuf = append(t.pasteBuf, '\x1b')
	}
	// Other special keys (arrows, function keys) are unusual in paste
	// content and are silently dropped.
}

// modalActive returns true if any modal overlay is currently intercepting input.
func (t *TUI) modalActive() bool {
	return t.welcome.active ||
		t.help.active ||
		t.eventLogM.active ||
		t.top.active ||
		t.mcpM.active ||
		t.webM.active ||
		t.agents.active ||
		t.cmd.active
}

// appendRune appends a rune as UTF-8 bytes to buf.
func appendRune(buf []byte, r rune) []byte {
	if r < 0x80 {
		return append(buf, byte(r))
	}
	var tmp [4]byte
	n := encodeRune(tmp[:], r)
	return append(buf, tmp[:n]...)
}

// encodeRune writes a rune as UTF-8 into p, returning the number of bytes written.
func encodeRune(p []byte, r rune) int {
	switch {
	case r < 0x80:
		p[0] = byte(r)
		return 1
	case r < 0x800:
		p[0] = byte(0xC0 | (r >> 6))
		p[1] = byte(0x80 | (r & 0x3F))
		return 2
	case r < 0x10000:
		p[0] = byte(0xE0 | (r >> 12))
		p[1] = byte(0x80 | ((r >> 6) & 0x3F))
		p[2] = byte(0x80 | (r & 0x3F))
		return 3
	default:
		p[0] = byte(0xF0 | (r >> 18))
		p[1] = byte(0x80 | ((r >> 12) & 0x3F))
		p[2] = byte(0x80 | ((r >> 6) & 0x3F))
		p[3] = byte(0x80 | (r & 0x3F))
		return 4
	}
}
