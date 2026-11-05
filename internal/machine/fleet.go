package machine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FleetDir is the folder inside Dir where each rig's window 1 writes its
// awake count.
const FleetDir = "fleet"

// WriteEvery is how often window 1 writes its count and reads the others.
const WriteEvery = time.Minute

// StaleAfter is how old a rig's file may be and still count: a rig that quit
// or crashed stops writing, and its file drops out by itself.
const StaleAfter = 3 * time.Minute

// FleetFile is one rig's count, as window 1 writes it.
type FleetFile struct {
	Rig   string    `json:"rig"`
	PID   int       `json:"pid"`
	Time  time.Time `json:"time"`
	Awake int       `json:"awake"`
}

// fleetName is the file a rig writes: its name with anything that is not a
// letter, digit, dot, dash or underscore replaced, so a rig name can never
// leave the folder.
func fleetName(rig string) string {
	var b strings.Builder
	for _, r := range rig {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		name = "rig"
	}
	return name + ".json"
}

// WriteFleet writes f to dir/fleet/<rig>.json: the folder 0700, the file
// 0600, through a temporary file and a rename so a reader never sees half
// of it.
func WriteFleet(dir string, f FleetFile) error {
	folder := filepath.Join(dir, FleetDir)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return fmt.Errorf("fleet folder: %w", err)
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(folder, ".tmp-*")
	if err != nil {
		return fmt.Errorf("fleet file: %w", err)
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("fleet file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("fleet file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("fleet file: %w", err)
	}
	if err := os.Rename(name, filepath.Join(folder, fleetName(f.Rig))); err != nil {
		os.Remove(name)
		return fmt.Errorf("fleet file: %w", err)
	}
	return nil
}

// SumOthers is the awake count of every rig but rig (every rig when rig is
// ""): the files in
// dir/fleet whose pid is alive and whose time is under StaleAfter old. A
// missing folder sums to 0; an unreadable or malformed file is skipped. The
// caller adds its own count, which it knows without a file.
func SumOthers(dir, rig string, now time.Time, alive func(pid int) bool) int {
	folder := filepath.Join(dir, FleetDir)
	entries, err := os.ReadDir(folder)
	if err != nil {
		return 0
	}
	own := ""
	if rig != "" {
		own = fleetName(rig)
	}
	total := 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") || strings.HasPrefix(n, ".") || n == own {
			continue
		}
		data, err := os.ReadFile(filepath.Join(folder, n))
		if err != nil {
			continue
		}
		var f FleetFile
		if json.Unmarshal(data, &f) != nil || f.Awake < 0 {
			continue
		}
		age := now.Sub(f.Time)
		if age >= StaleAfter || age < -StaleAfter || f.PID <= 0 || !alive(f.PID) {
			continue
		}
		total += f.Awake
	}
	return total
}
