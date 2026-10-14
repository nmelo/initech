package config

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Typing hold defaults, in seconds.
const (
	DefaultTypingHoldQuietS = 2
	DefaultTypingHoldMaxS   = 15
)

// RawSeconds keeps a seconds value exactly as written, so a malformed value
// reaches TypingHold (which falls back with a warning) instead of failing the
// whole load.
type RawSeconds struct{ node yaml.Node }

// UnmarshalYAML stores the node as written; it never fails.
func (r *RawSeconds) UnmarshalYAML(n *yaml.Node) error {
	r.node = *n
	return nil
}

// MarshalYAML writes the value back as it was read.
func (r RawSeconds) MarshalYAML() (any, error) { return &r.node, nil }

// TypingHold returns the effective typing-hold settings in seconds and one
// warning per key that fell back. Absent keys take their defaults silently.
// A negative or non-integer value falls back to that key's default. A max
// below quiet falls back to the max default, and to quiet itself when the
// default is still below it. quiet 0 disables the hold.
func (p *Project) TypingHold() (quiet, max int, warnings []string) {
	quiet, w := holdSeconds("typing_hold_quiet_s", p.TypingHoldQuietS, DefaultTypingHoldQuietS)
	warnings = append(warnings, w...)
	max, w = holdSeconds("typing_hold_max_s", p.TypingHoldMaxS, DefaultTypingHoldMaxS)
	warnings = append(warnings, w...)
	if max < quiet {
		fallback := DefaultTypingHoldMaxS
		if fallback < quiet {
			fallback = quiet
		}
		warnings = append(warnings, fmt.Sprintf(
			"typing_hold_max_s: %d is below typing_hold_quiet_s %d; using %d", max, quiet, fallback))
		max = fallback
	}
	return quiet, max, warnings
}

// holdSeconds reads one whole-seconds key, falling back to def with a warning
// naming the key and the value when it is not a non-negative integer.
func holdSeconds(key string, r *RawSeconds, def int) (int, []string) {
	if r == nil {
		return def, nil
	}
	n := &r.node
	if n.Kind == yaml.ScalarNode && n.Tag == "!!int" {
		if v, err := strconv.Atoi(n.Value); err == nil && v >= 0 {
			return v, nil
		}
	}
	return def, []string{fmt.Sprintf("%s: %q is not a whole number of seconds (0 or more); using %d", key, n.Value, def)}
}
