package machine

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Load5 is the machine's 5-minute load average.
func Load5() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, err
	}
	return parseLoad5(string(out))
}

// PIDAlive reports whether a process with pid exists (signal 0).
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// ReclaimableKB is the memory the system can hand out now, in KB: free,
// inactive and speculative pages.
func ReclaimableKB() (int64, error) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, err
	}
	return parseVMStat(string(out))
}

// TotalKB is the machine's total memory in KB.
func TotalKB() (int64, error) {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, err
	}
	return n / 1024, nil
}

// SwapUsedKB is the swap in use in KB.
func SwapUsedKB() (int64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.swapusage").Output()
	if err != nil {
		return 0, err
	}
	return parseSwapUsage(string(out))
}
