package machine

import (
	"os"
	"syscall"
)

// Load5 is the machine's 5-minute load average.
func Load5() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	return parseLoad5(string(data))
}

// PIDAlive reports whether a process with pid exists (signal 0).
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func readMeminfo() (string, error) {
	data, err := os.ReadFile("/proc/meminfo")
	return string(data), err
}

// ReclaimableKB is the memory the system can hand out now, in KB: the
// kernel's MemAvailable.
func ReclaimableKB() (int64, error) {
	out, err := readMeminfo()
	if err != nil {
		return 0, err
	}
	return parseMeminfoReclaimable(out)
}

// TotalKB is the machine's total memory in KB.
func TotalKB() (int64, error) {
	out, err := readMeminfo()
	if err != nil {
		return 0, err
	}
	return meminfoKB(out, "MemTotal")
}

// SwapUsedKB is the swap in use in KB.
func SwapUsedKB() (int64, error) {
	out, err := readMeminfo()
	if err != nil {
		return 0, err
	}
	return parseMeminfoSwapUsed(out)
}
