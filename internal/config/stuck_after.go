package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultStuckAfter is how long a bead may sit before it reads stuck when
// initech.yaml does not say.
const DefaultStuckAfter = time.Hour

// DefaultIdleAfter is how long an in_progress bead's owner pane may sit idle
// before the bead reads idle-owner when initech.yaml does not say.
const DefaultIdleAfter = 20 * time.Minute

// beadsDuration is a duration key under beads: in initech.yaml. The text is
// kept as written, so a write that does not change the key leaves its bytes
// alone.
type beadsDuration struct {
	d   time.Duration
	raw string
}

// unmarshal fails the load on a value that is not a usable duration, naming
// beads.<key> and the line.
func (s *beadsDuration) unmarshal(n *yaml.Node, key string) error {
	d, err := ParseStuckAfter(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: beads.%s: %w", n.Line, key, err)
	}
	s.d, s.raw = d, n.Value
	return nil
}

// marshal writes the value as it was read.
func (s beadsDuration) marshal() (any, error) {
	if s.raw != "" {
		return s.raw, nil
	}
	return s.d.String(), nil
}

// StuckAfter is beads.stuck_after in initech.yaml: a duration such as 20m.
type StuckAfter struct{ beadsDuration }

// IdleAfter is beads.idle_after in initech.yaml: a duration such as 20m.
type IdleAfter struct{ beadsDuration }

// ParseStuckAfter reads a beads duration value (stuck_after, idle_after). It
// must be a Go duration above zero: zero would make every bead read the moment
// it moved or its owner paused.
func ParseStuckAfter(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 20m or 1h", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be more than zero", s)
	}
	return d, nil
}

// UnmarshalYAML fails the load on a value that is not a usable duration,
// naming the key and the line.
func (s *StuckAfter) UnmarshalYAML(n *yaml.Node) error { return s.unmarshal(n, "stuck_after") }

// MarshalYAML writes the value as it was read.
func (s StuckAfter) MarshalYAML() (any, error) { return s.marshal() }

// IsZero reports the key as absent, so a config without it never gains it.
func (s StuckAfter) IsZero() bool { return s.d == 0 }

// UnmarshalYAML fails the load on a value that is not a usable duration,
// naming the key and the line.
func (s *IdleAfter) UnmarshalYAML(n *yaml.Node) error { return s.unmarshal(n, "idle_after") }

// MarshalYAML writes the value as it was read.
func (s IdleAfter) MarshalYAML() (any, error) { return s.marshal() }

// IsZero reports the key as absent, so a config without it never gains it.
func (s IdleAfter) IsZero() bool { return s.d == 0 }

// Value is how long a bead may sit before it reads stuck: the yaml value, or
// DefaultStuckAfter when the key is absent.
func (b BeadsConfig) Value() time.Duration {
	if b.StuckAfter.d > 0 {
		return b.StuckAfter.d
	}
	return DefaultStuckAfter
}

// IdleValue is how long an in_progress bead's owner pane may sit idle before
// the bead reads idle-owner: the yaml value, or DefaultIdleAfter when absent.
func (b BeadsConfig) IdleValue() time.Duration {
	if b.IdleAfter.d > 0 {
		return b.IdleAfter.d
	}
	return DefaultIdleAfter
}
