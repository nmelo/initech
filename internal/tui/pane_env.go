package tui

import (
	"os"
	"strings"
)

// The agent pane's terminal identity, pinned rather than inherited (ini-g0h).
//
// TERM has always been pinned here. TERM_PROGRAM is pinned for the same reason
// and one sharper one: Claude Code chooses its notification channel by resolving
// a single terminal name and switching on it, and only ONE branch emits the
// OSC 777 that tier-1 attention detection reads.
//
//	Apple_Terminal -> terminal bell     iTerm.app -> OSC 9
//	kitty          -> OSC 99            ghostty   -> OSC 777   <- the one we parse
//	anything else  -> no notification at all
//
// MEASURED with the live canary (Claude Code 2.1.229, macOS), child identity
// varied one variable at a time:
//
//	ghostty                       -> OSC 777 in 10.2s   (also 11.3s with no _VERSION)
//	tmux                          -> nothing in 90s     (qa1, the original finding)
//	absent                        -> nothing in 90s     (qa1 + eng2, both rigs)
//	iTerm.app                     -> nothing in 90s; it emitted OSC 9 instead
//	ghostty + JetBrains marker    -> nothing in 90s     (see overriddenTerminalEnv)
//
// The iTerm row is why this is a PIN and not "pass the host through unless it is
// a known suppressor". iTerm is a recognised terminal that notifies perfectly
// well -- on a channel we do not parse. Passthrough would have left tier-1 dead
// for every iTerm2, Apple Terminal, VS Code and Alacritty operator, silently,
// while looking fixed on a ghostty rig: the original bug with a smaller
// audience. Pinning gives every operator the configuration that was only ever
// working by luck for ghostty hosts.
//
// Pinning "ghostty" is not a claim about the operator's window. It is a claim
// about the PTY initech hands the agent, and it is the identity initech's own
// emulator has been serving for the project's whole life -- every ghostty-gated
// behaviour in Claude (the kitty-keyboard assumption behind the Shift+Enter
// workaround at pane.go:409, synchronized output, the geometric-shape and
// cmd-click quirks) is the status quo on the maintainer's host, not a new
// protocol contract taken on blind.
//
// TERM_PROGRAM_VERSION is deliberately left UNSET rather than pinned: OSC 777
// does not depend on it (measured above, both with and without), while several
// extras ARE version-gated -- ghostty >= 1.2.0 turns on progress reporting, for
// one. Absent version keeps those off, which is the conservative direction for
// an emulator that is not really Ghostty.
const (
	pinnedTerm        = "xterm-256color"
	pinnedTermProgram = "ghostty"
)

// overriddenTerminalEnv are the host-terminal identity variables stripped from
// the inherited environment before the pinned identity is applied. Every one of
// them is a statement about a terminal the agent is not running in.
//
// TERMINAL_EMULATOR earns its place by measurement, not by symmetry: Claude
// resolves it BEFORE TERM_PROGRAM (a JetBrains marker resolves to "pycharm"),
// so an operator running initech from a JetBrains terminal would have the pin
// silently shadowed and tier-1 would stay dead -- measured above, 90s of output
// with no notification of any kind. Unlike the other variables that resolve
// early (CURSOR_TRACE_ID, VSCODE_GIT_ASKPASS_MAIN, __CFBundleIdentifier,
// VisualStudioVersion) it is pure terminal identity with no second job, so
// removing it costs nothing. Those others shadow the pin too and are tracked
// separately; each carries real non-terminal freight (git askpass, MSBuild) and
// needs its own decision rather than a silent add-on here.
//
// TMUX is deliberately NOT in this list -- see agentBaseEnv.
var overriddenTerminalEnv = []string{
	"TERM",
	"TERM_PROGRAM",
	"TERM_PROGRAM_VERSION",
	"TERMINAL_EMULATOR",
}

// agentBaseEnv returns the environment for an agent pane: the process
// environment with host-terminal identity removed and initech's own pinned
// identity applied. It is the single owner of the terminal-facing environment,
// so the pane path and the live OSC probe cannot drift apart.
//
// TMUX is left alone on purpose. It is not terminal identity -- it is the
// address of a tmux server socket -- and an agent that shells out to tmux (this
// project's own operators do) would lose the ability to talk to it. tmux's
// suppression travels via TERM_PROGRAM=tmux, which is overridden above, so
// removing TMUX would buy nothing and break something real. If a capture ever
// shows TMUX itself suppressing, it belongs in overriddenTerminalEnv with that
// measurement cited.
func agentBaseEnv() []string {
	return append(scrubEnv(os.Environ(), overriddenTerminalEnv),
		"TERM="+pinnedTerm,
		"TERM_PROGRAM="+pinnedTermProgram,
	)
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
