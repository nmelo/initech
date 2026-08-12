package tui

import (
	"os"
	"strings"
	"testing"
)

// Tests for ini-g0h: the agent pane's terminal-facing environment is
// deterministic, not inherited -- and the deterministic value is one that
// MEASURABLY produces the OSC 777 tier-1 detection reads.
//
// This is the second of two guards with DIFFERENT failure owners. The live OSC
// canary (attention_emitter_probe_test.go) catches Claude ceasing to emit --
// their change. This catches US changing the identity we hand the agent -- our
// change. Neither covers the other: the canary would go red for a reason nobody
// could act on locally, and this test would stay green while the feature was
// dead upstream.
//
// The first version of this fix SCRUBBED TERM_PROGRAM to nothing. That passed a
// test with the same rigor as these and was still wrong: absence suppresses
// emission exactly like tmux does, so it converted "broken under tmux" into
// "broken everywhere" (qa1, measured, both directions). A test can only be as
// right as the contract it pins -- so the contract here is stated as the
// measurement that backs it, not as a shape that looks tidy.

// envValue reports the EFFECTIVE value of key: the last occurrence wins,
// matching what os/exec does to cmd.Env on Start.
func envValue(env []string, key string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			value, found = strings.TrimPrefix(kv, key+"="), true
		}
	}
	return value, found
}

func envCount(env []string, key string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			n++
		}
	}
	return n
}

func paneEnv(t *testing.T) []string {
	t.Helper()
	return buildPaneCmd(PaneConfig{Name: "eng1", Command: []string{"/bin/sh"}}, 24, 80).Env
}

// TestPaneEnv_PinsTheEmittingIdentity is the contract, driven through the real
// buildPaneCmd across the host environments that actually occur -- including
// the two MEASURED silent ones (tmux stamps its own name into every pane;
// absent is what a bare launchd/ssh session gives you) and one that is
// recognised, notifies correctly, and still never emits OSC 777 (iTerm.app,
// measured: it sends OSC 9 instead).
func TestPaneEnv_PinsTheEmittingIdentity(t *testing.T) {
	hosts := map[string]string{
		"tmux, the measured suppressor":            "tmux",
		"absent, which suppresses just as totally": "",
		"iTerm.app, recognised but wrong channel":  "iTerm.app",
		"ghostty, already correct":                 "ghostty",
		"some terminal that does not exist yet":    "Ferrous-Terminal",
	}
	for name, host := range hosts {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", host)
			if host == "" {
				os.Unsetenv("TERM_PROGRAM") // t.Setenv already registered the restore
			}

			env := paneEnv(t)

			got, ok := envValue(env, "TERM_PROGRAM")
			if !ok {
				t.Fatalf("agent pane env carries no TERM_PROGRAM; measured: with none, "+
					"Claude Code emits no OSC 777 at all and tier-1 detection is dead "+
					"(host was %q)", host)
			}
			// The literal is deliberate, not a missed constant. pinnedTermProgram
			// is the thing under test: comparing it to itself would pass for any
			// value, including the measured-silent ones. "ghostty" is the value
			// the canary observed emitting OSC 777, so the test names it.
			if got != "ghostty" {
				t.Errorf("TERM_PROGRAM = %q, want the measured-emitting \"ghostty\" -- iTerm.app "+
					"routes to OSC 9, kitty to OSC 99, and everything else to no notification "+
					"at all, none of which tier-1 detection reads (host was %q)", got, host)
			}
			if n := envCount(env, "TERM_PROGRAM"); n != 1 {
				t.Errorf("TERM_PROGRAM appears %d times; the host's value must be removed, "+
					"not merely shadowed", n)
			}
		})
	}
}

// TestPaneEnv_LeavesTermProgramVersionUnset pins the deliberate half-decision:
// identity yes, version no. OSC 777 is version-independent (measured with and
// without), while other behaviour is version-gated -- ghostty >= 1.2.0 enables
// progress reporting, for one -- and initech's emulator is not really Ghostty.
// Leaking the host's version would also be a second, unmeasured claim.
func TestPaneEnv_LeavesTermProgramVersionUnset(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TERM_PROGRAM_VERSION", "1.3.1")

	if v, ok := envValue(paneEnv(t), "TERM_PROGRAM_VERSION"); ok {
		t.Errorf("agent pane env carries TERM_PROGRAM_VERSION=%q; the pin claims an identity, "+
			"not a version, and version-gated behaviour should stay off", v)
	}
}

// TestPaneEnv_DropsShadowingTerminalMarkers covers the variable that would have
// made the pin decorative. Claude resolves TERMINAL_EMULATOR BEFORE
// TERM_PROGRAM, so a JetBrains-hosted initech kept the JetBrains identity and
// emitted nothing at all -- MEASURED: 90s, no OSC 777, no OSC 9, no channel.
func TestPaneEnv_DropsShadowingTerminalMarkers(t *testing.T) {
	t.Setenv("TERMINAL_EMULATOR", "JetBrains-JediTerm")

	if v, ok := envValue(paneEnv(t), "TERMINAL_EMULATOR"); ok {
		t.Errorf("agent pane env carries TERMINAL_EMULATOR=%q; it outranks TERM_PROGRAM in "+
			"Claude's terminal resolution and silently defeats the pin", v)
	}
}

// TestPaneEnv_StillPinsTerm guards the older half of the terminal-identity
// story: the rework must not have disturbed the TERM pin, and TERM must be the
// pinned value rather than the host's, exactly once.
func TestPaneEnv_StillPinsTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("TERM_PROGRAM", "tmux")

	env := paneEnv(t)

	if got, _ := envValue(env, "TERM"); got != pinnedTerm {
		t.Errorf("effective TERM = %q, want the pinned %q", got, pinnedTerm)
	}
	if n := envCount(env, "TERM"); n != 1 {
		t.Errorf("TERM appears %d times, want 1", n)
	}
}

// TestPaneEnv_PreservesUnrelatedVariables is the blast-radius guard: the
// override must replace terminal identity and nothing else. An over-broad
// filter would break agents in ways far more visible than the bug it fixed.
func TestPaneEnv_PreservesUnrelatedVariables(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	t.Setenv("INITECH_AGENT", "eng1")
	t.Setenv("PATH_LIKE_THING", "keepme")
	t.Setenv("TERM_PROGRAM_EXTRA", "keepme") // shares a prefix with a replaced name

	env := paneEnv(t)

	for _, key := range []string{"INITECH_AGENT", "PATH_LIKE_THING", "TERM_PROGRAM_EXTRA", "HOME", "PATH"} {
		if _, ok := envValue(env, key); !ok {
			t.Errorf("the override removed %q, which is not terminal identity", key)
		}
	}
}

// TestPaneEnv_KeepsTmux pins the deliberate NON-decision to scrub TMUX, so a
// future reader does not "complete" the fix by adding it without a measurement.
//
// TMUX is not terminal identity -- it is a tmux server socket address, and an
// agent that shells out to tmux would lose the ability to reach it. tmux's
// measured suppression travels via TERM_PROGRAM=tmux, which is now overridden,
// so dropping TMUX would buy nothing and break something real. If a capture
// ever shows TMUX itself suppressing, this test is the right place to invert,
// with that measurement cited.
func TestPaneEnv_KeepsTmux(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,12345,0")
	t.Setenv("TERM_PROGRAM", "tmux")

	if _, ok := envValue(paneEnv(t), "TMUX"); !ok {
		t.Error("TMUX was scrubbed; it is a server address, not terminal identity, and agents may legitimately use it")
	}
}

// TestScrubEnv_HandlesValuelessAndPrefixedNames covers the parsing edges a
// naive strings.Contains filter would get wrong: a bare name with no '=', and
// a DIFFERENT variable that merely starts with a suppressed name.
func TestScrubEnv_HandlesValuelessAndPrefixedNames(t *testing.T) {
	in := []string{
		"TERM_PROGRAM=tmux",
		"TERM_PROGRAM_EXTRA=keep", // not in the drop list; must survive
		"TERM_PROGRAM",            // valueless form
		"KEEP=1",
	}
	got := scrubEnv(in, overriddenTerminalEnv)

	for _, kv := range got {
		if kv == "TERM_PROGRAM=tmux" || kv == "TERM_PROGRAM" {
			t.Errorf("scrub left %q behind", kv)
		}
	}
	if _, ok := envValue(got, "TERM_PROGRAM_EXTRA"); !ok {
		t.Error("scrub removed TERM_PROGRAM_EXTRA, which only shares a prefix with a suppressed name")
	}
	if _, ok := envValue(got, "KEEP"); !ok {
		t.Error("scrub removed an unrelated variable")
	}
}
