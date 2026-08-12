//go:build !windows

package tui

import (
	"strings"
	"testing"
)

// Tests for ini-g0h: the agent pane's terminal-facing environment is
// deterministic, not inherited.
//
// This is the second of two guards with DIFFERENT failure owners. The live OSC
// canary (attention_emitter_probe_test.go) catches Claude ceasing to emit --
// their change. This catches US re-introducing a suppressor into the pane env
// -- our change. Neither covers the other: the canary would go red for a reason
// nobody could act on locally, and this test would stay green while the feature
// was dead upstream.

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"="), true
		}
	}
	return "", false
}

// TestPaneEnv_NeverCarriesTermProgram is the contract, driven through the real
// buildPaneCmd with the host env carrying exactly what tmux stamps.
func TestPaneEnv_NeverCarriesTermProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	t.Setenv("TERM_PROGRAM_VERSION", "3.5a")

	cmd := buildPaneCmd(PaneConfig{Name: "eng1", Command: []string{"/bin/sh"}}, 24, 80)

	if v, ok := envValue(cmd.Env, "TERM_PROGRAM"); ok {
		t.Errorf("agent pane env carries TERM_PROGRAM=%q; measured to kill Claude's OSC 777 emission entirely", v)
	}
	if v, ok := envValue(cmd.Env, "TERM_PROGRAM_VERSION"); ok {
		t.Errorf("agent pane env carries TERM_PROGRAM_VERSION=%q", v)
	}
}

// TestPaneEnv_StillPinsTerm guards the other half of the terminal-identity
// story: scrubbing must not have disturbed the existing TERM pin, and TERM must
// be the pinned value rather than the host's.
func TestPaneEnv_StillPinsTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("TERM_PROGRAM", "tmux")

	cmd := buildPaneCmd(PaneConfig{Name: "eng1", Command: []string{"/bin/sh"}}, 24, 80)

	// The pin is appended last, so the effective value is the final occurrence.
	var last string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "TERM=") {
			last = strings.TrimPrefix(kv, "TERM=")
		}
	}
	if last != "xterm-256color" {
		t.Errorf("effective TERM = %q, want the pinned xterm-256color", last)
	}
}

// TestPaneEnv_PreservesUnrelatedVariables is the blast-radius guard: the scrub
// must remove terminal identity and nothing else. An over-broad filter would
// break agents in ways far more visible than the bug it fixed.
func TestPaneEnv_PreservesUnrelatedVariables(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	t.Setenv("INITECH_AGENT", "eng1")
	t.Setenv("PATH_LIKE_THING", "keepme")

	cmd := buildPaneCmd(PaneConfig{Name: "eng1", Command: []string{"/bin/sh"}}, 24, 80)

	for _, key := range []string{"INITECH_AGENT", "PATH_LIKE_THING", "HOME", "PATH"} {
		if _, ok := envValue(cmd.Env, key); !ok {
			t.Errorf("scrub removed %q, which is not terminal identity", key)
		}
	}
}

// TestPaneEnv_KeepsTmux pins the deliberate NON-decision to scrub TMUX, so a
// future reader does not "complete" the fix by adding it without a measurement.
//
// TMUX is not terminal identity -- it is a tmux server socket address, and an
// agent that shells out to tmux would lose the ability to reach it. The
// measured suppressor is TERM_PROGRAM; removing TMUX would be an unmeasured
// change with real breakage potential. If a capture ever shows TMUX also
// suppresses, this test is the right place to invert, with that measurement
// cited.
func TestPaneEnv_KeepsTmux(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,12345,0")
	t.Setenv("TERM_PROGRAM", "tmux")

	cmd := buildPaneCmd(PaneConfig{Name: "eng1", Command: []string{"/bin/sh"}}, 24, 80)

	if _, ok := envValue(cmd.Env, "TMUX"); !ok {
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
	got := scrubEnv(in, suppressedTerminalEnv)

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
