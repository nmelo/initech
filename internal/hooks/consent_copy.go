package hooks

// ConsentPrompt is the one-time consent copy (pm-owned wording).
//
// It lives here, not in cmd, because TWO surfaces show it (ini-2x8.6): the
// init stdin flow and the in-TUI first-run modal. One constant read by both is
// what stops them drifting into saying different things about the same
// question -- a forked string is how one surface keeps a claim the other has
// already corrected.
//
// Rewritten for the OSC 777 decision (spec amendment 2026-08-12, 9357815). The
// original copy said the hook is what lets "the attention chime detect
// permission prompts exactly" -- true when the hook was the only live signal,
// false now that OSC 777 is tier-1, always on, and needs no consent. Declining
// costs redundancy, not the feature, and the copy must say so: a prompt that
// overstates the cost of No buys consent under false pretences, on the one
// question whose entire purpose is informed consent.
//
// Wording is swappable; the honesty PROPERTIES are guarded by test.
const ConsentPrompt = `Add a Claude notification hook to each agent's settings?

initech already detects agent dialogs on its own; this adds a second,
independent signal for permission prompts. Declining costs redundancy,
not the feature.

It writes a Notification hook into <agent>/.claude/settings.json for each
agent, preserving anything already there. [y/N]: `

// ConsentPromptLines splits the copy for a windowed renderer that draws line
// by line, so the modal and the stdin prompt cannot diverge in wording.
func ConsentPromptLines() []string {
	var out []string
	line := ""
	for _, r := range ConsentPrompt {
		if r == '\n' {
			out = append(out, line)
			line = ""
			continue
		}
		line += string(r)
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
