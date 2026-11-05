package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// cleanMessage makes a message's body safe to type into a pane, and counts
// what it removed. It is the one definition of what a message may carry, and
// the send path applies it before it opens the paste (ini-47vy): a body that
// held ESC[201~ would otherwise end the paste early and the rest would arrive
// as keystrokes.
//
// Removed: ESC and every C0 control but tab and newline, DEL, the C1 range
// and invalid UTF-8 bytes, one count each; a whole CSI (ESC[ or CSI, through
// its final byte) and a whole OSC, DCS, SOS, PM or APC string (through BEL or
// ST), one count each sequence. An unterminated sequence loses only its
// introducer; what follows stays as text. Kept: tab, newline and every
// printable rune.
func cleanMessage(text string) (string, int) {
	var b strings.Builder
	removed := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			removed++
			i++
			continue
		case r == '\n' || r == '\t':
		case r == 0x1b && i+1 < len(text):
			removed++
			i += 1 + escapeTail(text[i+1:])
			continue
		case r == 0x9b:
			removed++
			i += size + csiTail(text[i+size:])
			continue
		case r == 0x9d || r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f:
			removed++
			i += size + stringTail(text[i+size:])
			continue
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			removed++
			i += size
			continue
		}
		b.WriteString(text[i : i+size])
		i += size
	}
	if removed == 0 {
		return text, 0
	}
	return b.String(), removed
}

// escapeTail is how many bytes after an ESC belong to its sequence: a whole
// CSI or control string, or none (the ESC goes alone).
func escapeTail(rest string) int {
	switch rest[0] {
	case '[':
		if n := csiTail(rest[1:]); n > 0 {
			return 1 + n
		}
	case ']', 'P', 'X', '^', '_':
		if n := stringTail(rest[1:]); n > 0 {
			return 1 + n
		}
	}
	return 0
}

// csiTail is the length of a CSI's parameters, intermediates and final byte,
// or 0 when no final byte ends it.
func csiTail(rest string) int {
	i := 0
	for i < len(rest) && rest[i] >= 0x30 && rest[i] <= 0x3f {
		i++
	}
	for i < len(rest) && rest[i] >= 0x20 && rest[i] <= 0x2f {
		i++
	}
	if i < len(rest) && rest[i] >= 0x40 && rest[i] <= 0x7e {
		return i + 1
	}
	return 0
}

// stringTail is the length of a control string through its terminator (BEL,
// ESC backslash or ST), or 0 when nothing terminates it.
func stringTail(rest string) int {
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == 0x07:
			return i + 1
		case rest[i] == 0x1b && i+1 < len(rest) && rest[i+1] == '\\':
			return i + 2
		case strings.HasPrefix(rest[i:], "\u009c"):
			return i + len("\u009c")
		}
	}
	return 0
}

// ControlsRemovedNote is what a send's line adds for a message that lost n
// characters to the send path's cleaning (ini-47vy): "" for none, else
// "; n control characters removed".
func ControlsRemovedNote(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("; %d control characters removed", n)
}

// onlyControlsReason refuses a message that held nothing but control
// characters: an empty paste and Enter would submit whatever the box holds.
func onlyControlsReason(n int) string {
	return fmt.Sprintf("the message held only control characters (%d removed)", n)
}

// onlyControlsRefusal is the refusal for a non-empty text that cleaning
// leaves empty, or "" for any other text (an empty body included).
func onlyControlsRefusal(text string) string {
	if clean, n := cleanMessage(text); clean == "" && n > 0 {
		return onlyControlsReason(n)
	}
	return ""
}
