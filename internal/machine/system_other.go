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

// ReclaimableKB is unreadable here; it never engages the ceiling.
func ReclaimableKB() (int64, error) { return 0, errors.New("memory: not read on this platform") }

// TotalKB is unreadable here; the free memory default stays at its cap.
func TotalKB() (int64, error) { return 0, errors.New("memory: not read on this platform") }

// SwapUsedKB is unreadable here; the fleet line prints it as ?.
func SwapUsedKB() (int64, error) { return 0, errors.New("swap: not read on this platform") }
