package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/hooks"
	"github.com/nmelo/initech/internal/tui"
	"golang.org/x/term"
)

// attentionConsentPrompt is the one-time consent copy (pm-owned).
//
// Rewritten for the OSC 777 decision (spec amendment 2026-08-12, 9357815). The
// original copy said the hook is what lets "the attention chime detect
// permission prompts exactly", which was true when the hook was the only live
// signal. It is not any more: OSC 777 is tier-1, always on, needs no consent,
// and covers both dialog kinds. So the copy must not imply the feature depends
// on this answer -- declining declines a redundant second witness, and saying
// otherwise would buy a Yes under false pretences on a prompt whose entire
// purpose is informed consent to writing files inside the operator's agents.

// PromptAttentionHooksConsent asks the one-time consent question and returns
// the answer. Anything other than an explicit yes is No: the operator-decided
// default, and the right default for a prompt about writing to their files.
func PromptAttentionHooksConsent(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, hooks.ConsentPrompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false // EOF / non-interactive: default No, never assume Yes.
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// EnsureAttentionHooksConsent runs the one-time consent flow if it has not been
// answered, records the answer, and applies it.
//
// Returns whether anything was written back to the config, so the caller can
// persist initech.yaml exactly once. Asking is gated on HooksAnswered, so a
// recorded No is never re-asked -- which is why the config field is a pointer.
func EnsureAttentionHooksConsent(proj *config.Project, in io.Reader, out io.Writer) (answered bool) {
	if proj == nil || proj.Attention.HooksAnswered() {
		return false
	}
	granted := PromptAttentionHooksConsent(in, out)
	proj.Attention.Hooks = &granted

	if !granted {
		fmt.Fprintln(out, "Skipped. No agent settings were written. Change with 'attention.hooks: true' in initech.yaml.")
		return true
	}
	res := InstallAttentionHooks(proj, out)
	fmt.Fprintf(out, "Installed for %d agent(s). Change with 'attention.hooks: false' in initech.yaml.\n", res)
	return true
}

// InstallAttentionHooks installs the Notification hook for every role, and
// reports how many agents ended up with it.
//
// Per-agent failures are reported and skipped rather than aborting the run: one
// agent with an unparseable settings.json must not deny the hook to the other
// twelve, and it must not abort startup either. An unparseable file is left
// exactly as found (hooks.ErrSettingsUnparseable) -- never rewritten.
func InstallAttentionHooks(proj *config.Project, out io.Writer) int {
	installed := 0
	for _, role := range proj.Roles {
		res, err := hooks.InstallNotificationHook(proj.Root, role)
		switch {
		case err == hooks.ErrSettingsUnparseable:
			fmt.Fprintf(out, "  %s: settings.json is not valid JSON — left untouched, hook NOT installed\n", role)
		case err != nil:
			fmt.Fprintf(out, "  %s: %v\n", role, err)
		case res.Unchanged:
			installed++ // Already present counts as installed.
		default:
			installed++
		}
	}
	return installed
}

// stdinIsTerminal reports whether stdin is an interactive terminal. Overridable
// so tests can drive both branches without a real TTY.
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// maybeAskAttentionConsent runs the one-time consent flow on the init path,
// and reports whether the config now needs writing.
//
// SKIPPED ENTIRELY when stdin is not a terminal. `initech init` is scriptable
// -- CI, a setup script, a Dockerfile -- and a blocking read there would hang
// the caller forever on a question nobody can see. Silence is the only safe
// default for a consent prompt with no human attached: unset stays unset, so
// the operator is asked later on a surface that can actually reach them (the
// in-TUI modal) rather than having a default silently recorded for them.
func maybeAskAttentionConsent(p *config.Project, out io.Writer) bool {
	if p == nil || p.Attention.HooksAnswered() {
		return false
	}
	if !stdinIsTerminal() {
		return false
	}
	return EnsureAttentionHooksConsent(p, os.Stdin, out)
}

// attentionConsentRecorder returns the callback the TUI invokes when the
// operator answers the in-TUI consent modal (ini-2x8.6).
//
// It persists the answer FIRST and installs second. If the write fails, the
// install is skipped: a session that installed hooks but failed to record the
// answer would ask again next startup and install again -- and the operator
// would have no way to tell that their "yes" had been remembered, because the
// evidence lives in the config that failed to save.
//
// Errors are logged rather than surfaced as a modal: the operator has just
// answered and dismissed a question, and a second popup reporting a config
// write failure is not what they asked for. The hook tier is redundancy, so a
// failure here costs a second witness, never detection.
func attentionConsentRecorder(cfgPath string, proj *config.Project) func(bool) {
	if proj == nil || cfgPath == "" {
		return nil
	}
	return func(granted bool) {
		if err := config.Write(cfgPath, proj); err != nil {
			tui.LogWarn("attention", "could not record hook consent", "err", err)
			return
		}
		if !granted {
			return
		}
		var buf strings.Builder
		n := InstallAttentionHooks(proj, &buf)
		tui.LogInfo("attention", "installed notification hook", "agents", n, "detail", buf.String())
	}
}
