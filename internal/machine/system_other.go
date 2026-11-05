//go:build !darwin && !linux

package machine

import (
	"errors"
	"os"
)

// Load5 is unreadable here; an unreadable load never engages the ceiling.
func Load5() (float64, error) { return 0, errors.New("load average: not read on this platform") }

// PIDAlive reports whether a process with pid can be found.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
