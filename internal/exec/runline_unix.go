//go:build !windows

package exec

import (
	osexec "os/exec"
	"syscall"
)

func setProcessGroup(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup stops the line and everything it started.
func killProcessGroup(cmd *osexec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
