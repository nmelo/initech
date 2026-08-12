package tui

import (
	"os"
	"strings"
)

// suppressedTerminalEnv are host-terminal identity variables that must never
// reach an agent (ini-g0h).
//
// initech already pins TERM for agent panes, but TERM_PROGRAM passed straight
// through -- and Claude Code changes behaviour based on it. MEASURED (qa1,
// Claude Code 2.1.229): with TERM_PROGRAM=tmux in its environment Claude never
// emits OSC 777 at all (90s, 18k bytes of real output, zero emissions), while
// the same run in a raw PTY emits in 9-10s. tmux >= 3.2 stamps TERM_PROGRAM
// into every pane, so ANY operator running initech inside tmux got agents whose
// tier-1 detection was dead, silently, with no error anywhere -- the exact
// failure class the attention feature exists to end.
//
// The principle is broader than the one variable: an agent pane is a PTY
// initech owns, not the operator's host terminal, so the terminal-facing
// environment should be DETERMINISTIC rather than inherited. Anything that
// tells the agent what terminal it is in is a lie here, and one of those lies
// already cost us a whole detection tier.
//
// TMUX is deliberately NOT in this list -- see agentBaseEnv.
var suppressedTerminalEnv = []string{
	"TERM_PROGRAM",
	"TERM_PROGRAM_VERSION",
}

// agentBaseEnv returns the process environment with host-terminal identity
// stripped, as the base for every agent pane.
//
// TMUX is left alone on purpose. It is not terminal identity -- it is the
// address of a tmux server socket -- and an agent that shells out to tmux (this
// project's own operators do) would lose the ability to talk to it. The
// measured suppressor is TERM_PROGRAM specifically; removing TMUX would be an
// unmeasured change with real breakage potential, so it stays until something
// measures it. If a future capture shows TMUX also suppresses, it belongs in
// suppressedTerminalEnv with that measurement cited.
func agentBaseEnv() []string {
	return scrubEnv(os.Environ(), suppressedTerminalEnv)
}

// scrubEnv removes the named variables from a KEY=VALUE environment slice.
// Exported shape kept simple so tests can drive it with a synthetic env
// instead of mutating the test process's own.
func scrubEnv(env []string, drop []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		suppressed := false
		for _, d := range drop {
			if name == d {
				suppressed = true
				break
			}
		}
		if !suppressed {
			out = append(out, kv)
		}
	}
	return out
}
