package tui

import "github.com/charmbracelet/x/ansi/parser"

// keepC1STInStrings stops the escape parser from ending an OSC or DCS string
// on the 8-bit String Terminator byte 0x9C (ini-xe43).
//
// charmbracelet/x/ansi ends OSC and DCS strings on 0x9C, including when that
// byte sits inside a UTF-8 character. Claude Code sets its window title to
// "✳ <session title>" when a reply finishes, and ✳ encodes as e2 9c b3: the
// parser stopped at 0x9c, went back to plain text and printed the rest of the
// title at the cursor, which Claude keeps in its prompt box. The operator saw
// the session title sitting in the box after typing over a suggestion and
// took it for the suggestion that failed to clear.
//
// Every child initech runs speaks UTF-8, where the 8-bit C1 controls are not
// used (xterm ignores them in UTF-8 mode); strings still end on ESC \ (the
// 7-bit ST), and OSC on BEL, as before. The table is the parser package's
// own exported variable, so this is process-wide by construction and needs
// no fork of x/ansi. Applied from init so no parser runs before it.
func keepC1STInStrings(t parser.TransitionTable) {
	t.AddOne(0x9C, parser.OscStringState, parser.PutAction, parser.OscStringState)
	t.AddOne(0x9C, parser.DcsStringState, parser.PutAction, parser.DcsStringState)
}

func init() { keepC1STInStrings(parser.Table) }
