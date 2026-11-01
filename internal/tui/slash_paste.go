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
// collapsed); confirmed for a slash command on Claude Code 2.1.316 (800
// shown, 801 collapsed). Counted in runes, as that measurement is.
const claudeSlashMaxRunes = collapsedOneLineRunes

// claudeSlashMaxLines is the most lines a short slash command can have and
// still run. MEASURED on Claude Code 2.1.316 (ini-hoy), Haiku, pasted and
// submitted: 2 and 3 lines showed as text and ran; 4 lines collapsed to
// "[Pasted text #1 +3 lines]"; 5, 10 and 20 collapsed and did not run.
// Lines are line breaks plus one, the count Claude's "+M lines" is based on
// (collapsedPasteCount), so a trailing line break counts.
const claudeSlashMaxLines = 3

// slashPasteWordsPrefix begins both refusals.
const slashPasteWordsPrefix = "a slash command "

// slashPasteRefusal is why a send of text to a pane of agentType must not be
// typed: it is a slash command that Claude Code would collapse into a pasted
// block and never run. "" when it may be typed: the pane is not Claude Code
// (an unknown type included: its paste behaviour is unmeasured), the text
// does not begin with "/", or it is short enough in characters and lines.
func slashPasteRefusal(agentType, text string) string {
	if config.NormalizeAgentType(agentType) != config.AgentTypeClaudeCode || !strings.HasPrefix(text, "/") {
		return ""
	}
	if n := utf8.RuneCountInString(text); n > claudeSlashMaxRunes {
		return fmt.Sprintf("%s of %d characters would paste, not run; Claude takes a command of at most %d", strings.TrimSpace(slashPasteWordsPrefix), n, claudeSlashMaxRunes)
	}
	if lines := strings.Count(text, "\n") + 1; lines > claudeSlashMaxLines {
		return fmt.Sprintf("%s of %d lines would paste, not run; Claude takes a command of at most %d lines", strings.TrimSpace(slashPasteWordsPrefix), lines, claudeSlashMaxLines)
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
