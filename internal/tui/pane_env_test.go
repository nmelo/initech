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

// TestPaneEnv_DropsFreightFreeShadowingMarkers covers markers that outrank the
// pin in Claude's terminal resolution and cost nothing to remove (ini-m2e).
// CURSOR_TRACE_ID is a trace-correlation id with no consumer in an agent pane,
// and MEASURED with the pin already in place it suppressed OSC 777 completely.
func TestPaneEnv_DropsFreightFreeShadowingMarkers(t *testing.T) {
	t.Setenv("CURSOR_TRACE_ID", "7f3a9c21-0000-4000-8000-abcdefabcdef")

	if v, ok := envValue(paneEnv(t), "CURSOR_TRACE_ID"); ok {
		t.Errorf("agent pane env carries CURSOR_TRACE_ID=%q; it outranks TERM_PROGRAM in "+
			"Claude's terminal resolution and silently defeats the pin", v)
	}
}

// TestPaneEnv_KeepsShadowingMarkersThatCarryFreight pins three DELIBERATE
// non-removals, so a future reader does not "complete" ini-m2e by scrubbing
// what merely looks symmetrical (ini-m2e).
//
// All three shadow the pin -- measured, 90s of silence each -- and all three are
// kept anyway, because shadowing is necessary but not sufficient: removal also
// has to be free, and none of these is.
//
//   - VSCODE_GIT_ASKPASS_MAIN: the GIT_ASKPASS shim reads it, so scrubbing costs
//     credential prompts. And it only shadows for Cursor/Windsurf-flavoured
//     paths -- a plain VS Code path emitted OSC 777 in 10.7s -- so a blanket
//     scrub would break every VS Code-family operator to fix a subset.
//   - __CFBundleIdentifier: launchd-set process provenance read by Apple tooling.
//   - VisualStudioVersion: consumed by MSBuild.
//
// The measured alternative for these is forcing Claude's notification channel
// outright rather than enumerating an input list we do not own. If that lands,
// this test is the right place to revisit -- with the measurement cited.
func TestPaneEnv_KeepsShadowingMarkersThatCarryFreight(t *testing.T) {
	kept := map[string]string{
		"VSCODE_GIT_ASKPASS_MAIN": "/Applications/Cursor.app/Contents/Resources/app/extensions/git/dist/askpass-main.js",
		"__CFBundleIdentifier":    "com.jetbrains.pycharm",
		"VisualStudioVersion":     "17.0",
	}
	for key, value := range kept {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)

			if _, ok := envValue(paneEnv(t), key); !ok {
				t.Errorf("%s was scrubbed. It does shadow the pin, but removing it breaks a "+
					"real consumer (askpass / Apple tooling / MSBuild). Scrubbing it is a "+
					"measured decision, not a symmetry argument -- see ini-m2e", key)
			}
		})
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
	// Set rather than assumed: these exist on every platform only because the
	// test puts them there. Windows CI has no HOME.
	t.Setenv("HOME", "/home/agent")
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/sh")

	env := paneEnv(t)

	// Assert the contract itself rather than a hand-picked list of variables:
	// EVERY inherited variable that is not on a drop list must survive. The
	// earlier version named HOME and PATH explicitly and failed on Windows CI,
	// which has no HOME at all -- the test was asserting the host's shape, not
	// initech's behaviour. This form is platform-independent and strictly
	// stronger: it catches an over-broad filter whatever it eats.
	dropped := map[string]bool{}
	for _, name := range append(append([]string{}, overriddenTerminalEnv...), shadowingIdentityEnv...) {
		dropped[name] = true
	}
	for _, kv := range os.Environ() {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if dropped[name] {
			continue
		}
		if _, ok := envValue(env, name); !ok {
			t.Errorf("the override removed %q, which is not terminal identity", name)
		}
	}
	// The sweep alone is not enough, and the gap is worth naming: it derives its
	// expectation FROM the drop lists, so it passes for ANY list -- including
	// one that grew to eat something load-bearing. (Verified by mutation: adding
	// SHELL to the drop list left the sweep green.) So the names that must never
	// be dropped are also asserted against a fixed list the test owns, and are
	// SET here rather than assumed present, because the host's shape differs by
	// platform -- Windows CI has no HOME, which is what broke the earlier form.
	for _, key := range []string{"HOME", "PATH", "SHELL", "TERM_PROGRAM_EXTRA"} {
		if _, ok := envValue(env, key); !ok {
			t.Errorf("the override removed %q. It is not terminal identity, and an agent "+
				"needs it -- an over-broad filter breaks agents far more visibly than the "+
				"bug it was added to fix", key)
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
