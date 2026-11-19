package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// codexIdentityKeys are the pane identity variables (agentIdentityEnv) a Codex
// seat is started with as explicit policy, and the only values that ever go
// on its argv.
var codexIdentityKeys = []string{"INITECH_SOCKET", "INITECH_AGENT", "BEADS_ACTOR"}

// codexPolicyPrefix is Codex's config key for a variable set in every command
// it runs; Codex applies it over inherited values and its shell snapshot.
const codexPolicyPrefix = "shell_environment_policy.set."

// codexPreflightTimeout bounds the check that a seat's Codex takes the guard.
const codexPreflightTimeout = 10 * time.Second

// codexGuardArgv is argv with the identity guard right after the codex
// executable: --no-daemon, so the seat runs its commands in its own process
// and not in an app-server daemon another rig's seat started (that daemon's
// environment is the rig that started it), and one
// -c shell_environment_policy.set.<KEY>=<value> per identity variable in env
// (the last value wins), so the commands carry this pane's own identity
// whatever shell snapshot Codex restores. Every other argument keeps its
// place; a --no-daemon or identity -c already present is not added twice, and
// an identity value the command carries is replaced by the pane's own. A
// command with no codex executable is refused: the guard has nowhere to go.
func codexGuardArgv(argv, env []string) ([]string, error) {
	at := -1
	for i, a := range argv {
		if b := filepath.Base(a); b == "codex" || b == "codex.exe" {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("its command %q has no codex executable to start with the identity guard", strings.Join(argv, " "))
	}
	own := map[string]string{}
	for _, kv := range env {
		for _, k := range codexIdentityKeys {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				own[k] = v
			}
		}
	}
	// The command after the executable, without the guard's own pieces.
	var rest []string
	for i := at + 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--no-daemon" {
			continue
		}
		if (a == "-c" || a == "--config") && i+1 < len(argv) && isIdentityPolicy(argv[i+1]) {
			i++
			continue
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok && isIdentityPolicy(v) {
			continue
		}
		if v, ok := strings.CutPrefix(a, "-c="); ok && isIdentityPolicy(v) {
			continue
		}
		rest = append(rest, a)
	}
	out := append([]string{}, argv[:at+1]...)
	out = append(out, "--no-daemon")
	for _, k := range codexIdentityKeys {
		if v, ok := own[k]; ok {
			out = append(out, "-c", codexPolicyPrefix+k+"="+v)
		}
	}
	return append(out, rest...), nil
}

// isIdentityPolicy reports whether a -c value sets one of the identity keys.
func isIdentityPolicy(v string) bool {
	for _, k := range codexIdentityKeys {
		if strings.HasPrefix(v, codexPolicyPrefix+k+"=") {
			return true
		}
	}
	return false
}

// codexPreflight runs the guarded command up to and including its guard with
// --version, so a Codex that does not take the guard refuses the seat before
// it starts instead of starting it unguarded. A test swaps it.
var codexPreflight = func(argv []string) error {
	end := 0
	for i, a := range argv {
		if a == "--no-daemon" {
			end = i + 1
		}
		if a == "-c" && i+1 < len(argv) && isIdentityPolicy(argv[i+1]) {
			end = i + 2
		}
	}
	probe := append(append([]string{}, argv[:end]...), "--version")
	ctx, cancel := context.WithTimeout(context.Background(), codexPreflightTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, probe[0], probe[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		why := strings.TrimSpace(strings.SplitN(strings.TrimSpace(stderr.String()), "\n", 2)[0])
		if why == "" {
			why = err.Error()
		}
		return fmt.Errorf("%s", why)
	}
	return nil
}

// guardCodexSeat applies the identity guard to a Codex pane's command and
// checks its Codex takes it; any failure is one line and the seat does not
// start.
func guardCodexSeat(cfg PaneConfig) ([]string, error) {
	argv, err := codexGuardArgv(cfg.Command, cfg.Env)
	if err == nil {
		err = codexPreflight(argv)
	}
	if err != nil {
		return nil, fmt.Errorf("codex seat %s not started: it must run with the identity guard (--no-daemon and its own %s as %s*), and %s",
			cfg.Name, strings.Join(codexIdentityKeys, ", "), codexPolicyPrefix, strings.ReplaceAll(err.Error(), "\n", " "))
	}
	return argv, nil
}
