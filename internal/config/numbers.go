package config

import (
	"fmt"
	"strconv"

	"github.com/nmelo/initech/internal/rignumbers"
	"gopkg.in/yaml.v3"
)

// NumbersConfig is the numbers: block in initech.yaml: the thresholds of the
// rig's numbers, one key per number and direction (gate_red_max: 1,
// idle_engineers_min: 2). A crossing sends super one line. Absent keys have
// no threshold. The block is kept as written, so a write that does not change
// it leaves its bytes alone.
type NumbersConfig struct {
	th   rignumbers.Thresholds
	node *yaml.Node
}

// UnmarshalYAML fails the load on an unknown key or a value that is not a
// number of zero or more, naming the key and the line.
func (c *NumbersConfig) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: numbers: want keys such as gate_red_max: 1", n.Line)
	}
	th := rignumbers.Thresholds{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if err := rignumbers.ValidKey(k.Value); err != nil {
			return fmt.Errorf("line %d: numbers.%s: %w", k.Line, k.Value, err)
		}
		f, err := strconv.ParseFloat(v.Value, 64)
		if err != nil || v.Kind != yaml.ScalarNode || f < 0 {
			return fmt.Errorf("line %d: numbers.%s: %q is not a number of zero or more", v.Line, k.Value, v.Value)
		}
		th[k.Value] = f
	}
	c.th, c.node = th, n
	return nil
}

// MarshalYAML writes the block as it was read.
func (c NumbersConfig) MarshalYAML() (any, error) {
	if c.node != nil {
		return c.node, nil
	}
	return map[string]float64(c.th), nil
}

// IsZero reports the block as absent, so a config without it never gains it.
func (c NumbersConfig) IsZero() bool { return len(c.th) == 0 }

// Thresholds are the block's thresholds; empty when the block is absent.
func (c NumbersConfig) Thresholds() rignumbers.Thresholds { return c.th }
