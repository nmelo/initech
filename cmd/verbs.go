package cmd

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// RegisteredVerbs returns every verb this binary answers to: each registered
// command's name plus its aliases, sorted.
//
// Exported for the template-verb census (ini-j0er), which asserts that every
// `initech <verb>` an agent's role template teaches resolves to a command the
// binary actually has. The census asks the COMMAND TREE rather than grepping
// cmd/*.go for `Use:` lines, deliberately: grep is a second derivation of a
// fact the tree already holds, and it misses aliases -- `hire` and `fire` are
// aliases of add-agent and delete-agent, so a grep-based list reports two
// false failures. (Measured while designing the census: a hand-typed list of
// registered commands produced a false positive on delete-agent within a
// minute of being written. Derive the inventory; do not retype it.)
func RegisteredVerbs() []string {
	seen := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if name := strings.Fields(sub.Use); len(name) > 0 {
				seen[name[0]] = true
			}
			for _, a := range sub.Aliases {
				seen[a] = true
			}
			walk(sub)
		}
	}
	walk(rootCmd)

	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
