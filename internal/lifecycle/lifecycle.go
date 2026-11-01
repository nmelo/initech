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

// fixedSideStates are side states whatever status.custom says: a bead that
// waits on something named, or is parked on purpose (Nelson, 2026-11-01).
var fixedSideStates = []string{"blocked", "deferred"}

// Lifecycle is the bead lifecycle deliver walks, read from bd's status.custom.
type Lifecycle struct {
	// Chain is the walk in directional order: open, in_progress, then
	// status.custom up to and including qa_passed. Super closes a bead
	// with bd close; deliver never does.
	Chain []string
	// Side are the states that are not steps: blocked, deferred, and every
	// status.custom entry after qa_passed, so a status added after it is a
	// side state by default. Deliver refuses a bead in one (ini-q2k).
	Side []string
	// NoQAPassed is true when status.custom has no qa_passed: the chain is
	// then the custom list bracketed by open, in_progress and closed, as it
	// was before qa_passed ended it.
	NoQAPassed bool
}

// IsSide reports whether status is a side state.
func (l Lifecycle) IsSide(status string) bool {
	for _, s := range l.Side {
		if s == status {
			return true
		}
	}
	return false
}

// LoadLifecycle reads bd's status.custom into the chain and the side states.
//
// Hard-fails if bd is unreachable or returns an error: the walker cannot make
// a meaningful next/prev decision without the configured middle section.
// (ini-6e54 Q4.)
//
// Example: status.custom="ready_for_qa,in_qa,qa_passed,ready_to_ship,waiting_on_nelson"
// gives Chain [open in_progress ready_for_qa in_qa qa_passed] and Side
// [blocked deferred ready_to_ship waiting_on_nelson]. An empty status.custom
// gives Chain [open in_progress closed], Side [blocked deferred], NoQAPassed.
func LoadLifecycle() (Lifecycle, error) {
	raw, err := ConfigGetFn("status.custom")
	if err != nil {
		return Lifecycle{}, fmt.Errorf("read bd status.custom: %w (is bd installed and the project configured?)", err)
	}
	var custom []string
	if raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(p); s != "" {
				custom = append(custom, s)
			}
		}
	}
	l := Lifecycle{Chain: []string{"open", "in_progress"}, Side: append([]string{}, fixedSideStates...)}
	end := -1
	for i, s := range custom {
		if s == qaPassedState {
			end = i
			break
		}
	}
	if end < 0 {
		l.Chain = append(append(l.Chain, custom...), "closed")
		l.NoQAPassed = true
		return l, nil
	}
	l.Chain = append(l.Chain, custom[:end+1]...)
	l.Side = append(l.Side, custom[end+1:]...)
	return l, nil
}

// LoadChain is LoadLifecycle's chain.
func LoadChain() ([]string, error) {
	l, err := LoadLifecycle()
	return l.Chain, err
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
