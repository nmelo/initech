package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nmelo/initech/internal/config"
)

// claudeSlashMaxRunes is the longest slash command Claude Code reads as a
// command when it arrives as a paste. A longer one-line message collapses to
// "[Pasted text #N]" and is sent as content, so the command never runs.
// MEASURED: see collapsedOneLineRunes (Claude Code 2.1.312: 800 shown, 801
// collapsed); confirmed for a slash command on the installed Claude Code by
// the live run recorded on ini-hoy. Counted in runes, as that measurement is.
const claudeSlashMaxRunes = collapsedOneLineRunes

// slashPasteLineBreakWords is the refusal for a slash command with a line
// break: Claude collapses any multi-line paste, at any length.
const slashPasteLineBreakWords = "a slash command with a line break would paste, not run"

// slashPasteWordsPrefix begins both refusals.
const slashPasteWordsPrefix = "a slash command "

// slashPasteRefusal is why a send of text to a pane of agentType must not be
// typed: it is a slash command that Claude Code would collapse into a pasted
// block and never run. "" when it may be typed: the pane is not Claude Code
// (an unknown type included: its paste behaviour is unmeasured), the text
// does not begin with "/", or it is short enough and on one line.
func slashPasteRefusal(agentType, text string) string {
	if config.NormalizeAgentType(agentType) != config.AgentTypeClaudeCode || !strings.HasPrefix(text, "/") {
		return ""
	}
	if strings.ContainsAny(text, "\r\n") {
		return slashPasteLineBreakWords
	}
	if n := utf8.RuneCountInString(text); n > claudeSlashMaxRunes {
		return fmt.Sprintf("%s of %d characters would paste, not run; Claude takes a command of at most %d", strings.TrimSpace(slashPasteWordsPrefix), n, claudeSlashMaxRunes)
	}
	return ""
}

// IsSlashPasteRefusal reports whether msg, a send's error or reason, is the
// slash-command refusal, so a caller can name its own outcome ("not
// compacted") without matching the words itself.
func IsSlashPasteRefusal(msg string) bool {
	if i := strings.Index(msg, ": "+slashPasteWordsPrefix); i >= 0 {
		msg = msg[i+2:]
	}
	return strings.HasPrefix(msg, slashPasteWordsPrefix) && strings.Contains(msg, "would paste, not run")
}
