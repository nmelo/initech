// Package lifecycle constructs and walks the bead lifecycle chain.
//
// bd treats statuses as a flat enumeration with categories (active, wip,
// frozen, done) rather than a directional chain. The directional flow
// "advance the bead one step on success, walk back one step on failure"
// is an initech convention layered on top of bd's primitives.
//
// The lifecycle chain is constructed dynamically from bd's configuration:
//
//	chain := []string{"open", "in_progress"} + customStates + []string{"closed"}
//
// where customStates comes from `bd config get status.custom`, an
// operator-defined ordered list (e.g. "ready_for_qa,in_qa,qa_passed").
//
// The wrapper invariant ([open, in_progress] at start, [closed] at terminal)
// uses bd's built-in category labels: open is the default active state,
// in_progress is the WIP entry point, closed is the only "done" terminal.
// Side-state built-ins (blocked, deferred, pinned, hooked) deliberately do
// NOT participate in the linear chain — they branch off, they don't
// advance through. (ini-6e54.)
//
// This package does not import cmd/, does not know about deliver, and only
// shells out to bd via the ConfigGetFn function variable (tests stub it).
package lifecycle

import (
	"fmt"
	"os/exec"
	"strings"
)

// ConfigGetFn fetches a single bd config value (e.g., "status.custom"). The
// default implementation shells out to `bd config get <key>`. Tests reassign
// this variable to inject canned values without subprocess overhead.
//
// Returns the trimmed string value on success. Errors propagate as-is so
// callers can distinguish bd-unavailable from bd-returned-error from
// trim-and-empty-result.
var ConfigGetFn = func(key string) (string, error) {
	out, err := exec.Command("bd", "config", "get", key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// LoadChain returns the full bead lifecycle chain in directional order.
// Reads bd's status.custom config to populate the project-defined middle
// section, then brackets with the standard wrapper [open, in_progress] at
// the start and [closed] at the terminal.
//
// Hard-fails if bd is unreachable or returns an error: the lifecycle walker
// cannot make a meaningful next/prev decision without the configured
// middle section. (ini-6e54 Q4.)
//
// Example return values:
//   - empty status.custom -> ["open", "in_progress", "closed"]
//   - status.custom="ready_for_qa,in_qa,qa_passed,ready_to_ship" ->
//     ["open", "in_progress", "ready_for_qa", "in_qa", "qa_passed", "ready_to_ship", "closed"]
func LoadChain() ([]string, error) {
	raw, err := ConfigGetFn("status.custom")
	if err != nil {
		return nil, fmt.Errorf("read bd status.custom: %w (is bd installed and the project configured?)", err)
	}

	var custom []string
	if raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(p); s != "" {
				custom = append(custom, s)
			}
		}
	}

	chain := make([]string, 0, len(custom)+3)
	chain = append(chain, "open", "in_progress")
	chain = append(chain, custom...)
	chain = append(chain, "closed")
	return chain, nil
}

// NextState returns the state after current in the chain, plus a bool
// indicating whether the advance is possible. Returns ("", false) when
// current is at or past the terminal, or when current is not in the chain.
//
// Callers should treat the (false) case as "no-op + warn the user the bead
// is already at the terminal state."
func NextState(chain []string, current string) (string, bool) {
	for i, s := range chain {
		if s == current && i+1 < len(chain) {
			return chain[i+1], true
		}
	}
	return "", false
}

// PrevState returns the state before current in the chain, plus a bool
// indicating whether walking back is possible. Returns ("", false) when
// current is at the initial state or not in the chain.
//
// Callers should treat the (false) case as "no-op + warn the user the bead
// cannot be regressed further."
func PrevState(chain []string, current string) (string, bool) {
	for i, s := range chain {
		if s == current && i > 0 {
			return chain[i-1], true
		}
	}
	return "", false
}
