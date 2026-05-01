//go:build !windows

package tui

import (
	"fmt"
	"os"
	"os/exec"
)

func buildPaneCmd(cfg PaneConfig, rows, cols int) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var cmd *exec.Cmd
	if len(cfg.Command) == 0 {
		cmd = exec.Command(shell, "-l")
	} else if containsArg(cfg.Command, "--continue") {
		primary := shellQuoteArgs(cfg.Command)
		fallback := shellQuoteArgs(removeArg(cfg.Command, "--continue"))
		cmd = exec.Command("/bin/sh", "-l", "-c", primary+" || "+fallback)
	} else {
		quoted := shellQuoteArgs(cfg.Command)
		cmd = exec.Command(shell, "-l", "-c", "exec "+quoted)
	}
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("LINES=%d", rows),
		fmt.Sprintf("COLUMNS=%d", cols),
	)
	cmd.Env = append(cmd.Env, cfg.Env...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	return cmd
}
