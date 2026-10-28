//go:build !windows

package tui

import "os/exec"

// readProcTable is every process's pid, parent, elapsed time and comm, from
// one ps call. Its text is parsed, never printed (proc_tree.go).
func readProcTable() (string, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "etime=", "-o", "comm=").Output()
	return string(out), err
}
