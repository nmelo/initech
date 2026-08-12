package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/hooks"
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
const attentionConsentPrompt = `Add a Claude notification hook to each agent's settings?

initech already detects agent dialogs on its own; this adds a second,
independent signal for permission prompts. Declining costs redundancy,
not the feature.

It writes a Notification hook into <agent>/.claude/settings.json for each
agent, preserving anything already there. [y/N]: `

// PromptAttentionHooksConsent asks the one-time consent question and returns
// the answer. Anything other than an explicit yes is No: the operator-decided
// default, and the right default for a prompt about writing to their files.
func PromptAttentionHooksConsent(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, attentionConsentPrompt)
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
