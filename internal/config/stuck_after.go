package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultStuckAfter is how long a bead may sit before it reads stuck when
// initech.yaml does not say.
const DefaultStuckAfter = time.Hour

// StuckAfter is beads.stuck_after in initech.yaml: a duration such as 20m.
// The text is kept as written, so a write that does not change the key leaves
// its bytes alone.
type StuckAfter struct {
	d   time.Duration
	raw string
}

// ParseStuckAfter reads a stuck-after value. It must be a Go duration above
// zero: zero would make every bead stuck the moment it moved.
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
func (s *StuckAfter) UnmarshalYAML(n *yaml.Node) error {
	d, err := ParseStuckAfter(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: beads.stuck_after: %w", n.Line, err)
	}
	s.d, s.raw = d, n.Value
	return nil
}

// MarshalYAML writes the value as it was read.
func (s StuckAfter) MarshalYAML() (any, error) {
	if s.raw != "" {
		return s.raw, nil
	}
	return s.d.String(), nil
}

// IsZero reports the key as absent, so a config without it never gains it.
func (s StuckAfter) IsZero() bool { return s.d == 0 }

// Value is how long a bead may sit before it reads stuck: the yaml value, or
// DefaultStuckAfter when the key is absent.
func (b BeadsConfig) Value() time.Duration {
	if b.StuckAfter.d > 0 {
		return b.StuckAfter.d
	}
	return DefaultStuckAfter
}
