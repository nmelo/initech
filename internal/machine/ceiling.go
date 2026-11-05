// Package machine is the machine ceiling: what the whole Mac can carry,
// across every rig on it. One file, machine.yaml in the user's initech
// config folder, sets three numbers (awake Claude sessions, free memory and
// the 5-minute load); each rig's window 1 writes its own awake count to the
// fleet folder beside it once a minute; and a Gate reads the sum with the
// system's numbers and says whether one more agent may start or wake.
//
// The package holds no rig state and imports nothing from the TUI: the
// numbers come in as a Reading, the decision goes out as a bool and words.
package machine

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Ceiling is the three numbers a machine may not reach. At or above
// Sessions awake sessions, at or below FreeMemoryGB free, or at or above
// Load on the 5-minute average, no agent starts or wakes.
type Ceiling struct {
	Sessions     int     `yaml:"sessions"`
	FreeMemoryGB float64 `yaml:"free_memory_gb"`
	Load         float64 `yaml:"load"`
	// FreeSource names where FreeMemoryGB came from, for the fleet line:
	// SourceDefault, "20% of <n> GB", or SourceFile.
	FreeSource string `yaml:"-"`
}

const (
	// DefaultFreeCapGB is the most the free memory default can be.
	DefaultFreeCapGB = 8.0
	// DefaultFreePercent is the share of total memory the free memory
	// default is, on a machine where that is less than the cap.
	DefaultFreePercent = 20
	// SourceDefault says the free memory value is the default cap.
	SourceDefault = "default"
	// SourceFile says the free memory value was set in machine.yaml.
	SourceFile = FileName
)

// CeilingFor is the ceiling with no machine.yaml on a machine of totalGB:
// 30 sessions, load 30, and free memory the lower of DefaultFreeCapGB and
// DefaultFreePercent of total. An unknown total (0 or less) keeps the cap.
func CeilingFor(totalGB float64) Ceiling {
	c := DefaultCeiling
	if share := totalGB * DefaultFreePercent / 100; totalGB > 0 && share < DefaultFreeCapGB {
		c.FreeMemoryGB = math.Round(share*10) / 10
		c.FreeSource = fmt.Sprintf("%d%% of %s GB", DefaultFreePercent, num(math.Round(totalGB*10)/10))
	}
	return c
}

// DefaultCeiling is the ceiling when machine.yaml is absent or leaves a
// key out.
var DefaultCeiling = Ceiling{Sessions: 30, FreeMemoryGB: DefaultFreeCapGB, Load: 30, FreeSource: SourceDefault}

// FileName is the machine-level file's name inside Dir.
const FileName = "machine.yaml"

// Dir is the machine-level initech folder: the user's config folder plus
// "initech" (~/Library/Application Support/initech on macOS). It follows
// HOME, so a test or a QA rig with a scratch HOME never touches the
// operator's file.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("machine folder: %w", err)
	}
	return filepath.Join(base, "initech"), nil
}

// LoadCeiling reads dir/machine.yaml on a machine of totalGB. An absent file
// is CeilingFor(totalGB); a key left out keeps that value; a key set is used
// as written; an unknown key or a bad value fails the load with an error
// naming the key.
func LoadCeiling(dir string, totalGB float64) (Ceiling, error) {
	c := CeilingFor(totalGB)
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return c, nil // an empty file is the defaults
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return c, fmt.Errorf("%s: must be a map with one key, ceiling:", path)
	}
	var ceiling *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if k := top.Content[i].Value; k != "ceiling" {
			return c, fmt.Errorf("%s: %s: not a machine key (the file holds ceiling:)", path, k)
		}
		ceiling = top.Content[i+1]
	}
	if ceiling == nil {
		return c, nil
	}
	if ceiling.Kind != yaml.MappingNode {
		return c, fmt.Errorf("%s: ceiling: must be a map of sessions, free_memory_gb and load", path)
	}
	for i := 0; i+1 < len(ceiling.Content); i += 2 {
		key, val := ceiling.Content[i].Value, ceiling.Content[i+1]
		switch key {
		case "sessions":
			var n int
			if val.ShortTag() != "!!int" || val.Decode(&n) != nil || n <= 0 {
				return c, fmt.Errorf("%s: ceiling.sessions: %q is not a whole number above 0", path, val.Value)
			}
			c.Sessions = n
		case "free_memory_gb":
			var f float64
			if val.Decode(&f) != nil || f < 0 {
				return c, fmt.Errorf("%s: ceiling.free_memory_gb: %q is not a number of GB, 0 or more", path, val.Value)
			}
			c.FreeMemoryGB, c.FreeSource = f, SourceFile
		case "load":
			var f float64
			if val.Decode(&f) != nil || f <= 0 {
				return c, fmt.Errorf("%s: ceiling.load: %q is not a number above 0", path, val.Value)
			}
			c.Load = f
		default:
			return c, fmt.Errorf("%s: ceiling.%s: not a ceiling key (sessions, free_memory_gb, load)", path, key)
		}
	}
	return c, nil
}
