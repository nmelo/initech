package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/nmelo/initech/internal/usage"
)

// Commented examples initech init writes into initech.yaml (ini-vby5.12).
//
// The trained fleets set these keys by hand on every new project. They are
// written commented out, in a generic form with placeholders -- no model name,
// host, path or token -- so a new operator sees what exists without any of it
// taking effect. They are text, not fields on the Project, because Write
// regenerates the file from the struct and keeps no comments.

// examplesHeader opens the appended block.
const examplesHeader = `
# ── Settings a trained fleet sets (uncomment and edit what you need) ──
# See "What a trained fleet sets" in docs/operator-guide.md.
`

// configExample is one commented-out example and the top-level key it shows.
type configExample struct {
	key  string
	text string
	// nested says key sits under another key, so it is looked for at any
	// indent, live or commented, not only at the top level.
	nested bool
}

// configExamples are appended in this order, each only when its key is not
// already in the file (live or commented).
var configExamples = []configExample{
	{key: "claude_command", text: `
# Wrap the agent command, e.g. to set an environment variable for every agent:
# claude_command: [env, SOME_VAR=1, claude]
`},
	{key: "claude_args", text: `
# Flags for every Claude agent: resume the last session, skip permission prompts:
# claude_args: [--continue, --dangerously-skip-permissions]
`},
	{key: "resource", text: `
# Park idle agents when memory runs short (off when this block is absent):
# resource:
#   auto_suspend: true
#   pressure_threshold: <percent>
`},
	{key: "running_pane_tint", text: `
# Background tint behind running panes; the trained fleets use a lighter grey than the default:
# running_pane_tint: '#1e2022'
`},
	{key: "typing_hold_quiet_s", text: `
# A message to a pane you are typing in waits until you pause (0 turns the hold off):
# typing_hold_quiet_s: 2   # seconds without a keystroke before it goes
# typing_hold_max_s: 15    # longest a message waits, then it goes anyway
`},
	{key: "role_overrides", text: `
# Per-role flags REPLACE claude_args above for that role, so repeat them; a model, or a per-agent settings file:
# role_overrides:
#   <role>:
#     claude_args: [--continue, --dangerously-skip-permissions, --model, <model>]
#   <other-role>:
#     claude_args: [--continue, --dangerously-skip-permissions, --settings, <path>]
`},
	{key: "stuck_after", nested: true, text: `
# How long a bead may sit before it reads stuck (one hour when absent), under the beads: key:
# beads:
#   stuck_after: 20m
`},
	{key: "telemetry", text: "\n# " + usage.Notice + "\n# telemetry: false\n"},
}

// hasTopLevelKey reports whether text sets key at the top level, live or
// commented out ("key:" or "# key:" at the start of a line). At most one
// space after the '#': a deeper one is a nested line inside a commented-out
// block (the role_overrides example's own claude_args), not the key itself.
func hasTopLevelKey(text, key string) bool {
	re := regexp.MustCompile(`(?m)^(#[ \t]?)?` + regexp.QuoteMeta(key) + `[ \t]*:`)
	return re.MatchString(text)
}

// present reports whether text already has this example's key, live or
// commented.
func (ex configExample) present(text string) bool {
	if ex.nested {
		return regexp.MustCompile(`(?m)^[#\s]*` + regexp.QuoteMeta(ex.key) + `[ \t]*:`).MatchString(text)
	}
	return hasTopLevelKey(text, ex.key)
}

// MissingExamples returns the example block for every key text does not
// already have, or "" when none is missing.
func MissingExamples(text string) string {
	var b strings.Builder
	for _, ex := range configExamples {
		if !ex.present(text) {
			b.WriteString(ex.text)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return examplesHeader + b.String()
}

// AppendMissingExamples appends the commented examples missing from the
// config at path, and reports how many keys it added. It only ever appends:
// every byte already in the file is kept, so an operator's values and
// comments survive. Running it again adds nothing.
func AppendMissingExamples(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	text := string(data)
	block := MissingExamples(text)
	if block == "" {
		return 0, nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		block = "\n" + block
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	if _, err := f.WriteString(block); err != nil {
		f.Close()
		return 0, fmt.Errorf("append config examples: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	n := 0
	for _, ex := range configExamples {
		if !ex.present(text) {
			n++
		}
	}
	return n, nil
}
