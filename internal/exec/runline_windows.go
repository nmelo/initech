//go:build windows

package exec

import osexec "os/exec"

func setProcessGroup(*osexec.Cmd) {}

// killProcessGroup stops the line; Windows has no process group to signal here.
func killProcessGroup(cmd *osexec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
