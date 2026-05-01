//go:build windows

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func buildPaneCmd(cfg PaneConfig, rows, cols int) *exec.Cmd {
	var cmd *exec.Cmd
	if len(cfg.Command) == 0 {
		cmd = exec.Command("cmd.exe")
	} else if containsArg(cfg.Command, "--continue") {
		primary := strings.Join(cfg.Command, " ")
		fallback := strings.Join(removeArg(cfg.Command, "--continue"), " ")
		cmd = exec.Command("cmd.exe", "/c", primary+" || "+fallback)
	} else {
		cmd = exec.Command(cfg.Command[0], cfg.Command[1:]...)
	}
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("LINES=%d", rows),
		fmt.Sprintf("COLUMNS=%d", cols),
	)
	cmd.Env = append(cmd.Env, cfg.Env...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	return cmd
}
