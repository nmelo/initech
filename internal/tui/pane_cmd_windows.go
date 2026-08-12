//go:build windows

package tui

import (
	"fmt"
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
	// agentBaseEnv, not os.Environ(): the terminal identity an agent sees is
	// initech's own emulator on every platform, so it is pinned on every
	// platform (ini-g0h / ini-m2e). This path used os.Environ() and therefore
	// had no pin at all -- the host's TERM_PROGRAM passed straight through and
	// tier-1 attention detection was dead on Windows for the original g0h
	// reason. Nothing about the emulator is platform-specific, so nothing about
	// the identity should be either.
	//
	// UNMEASURED HALF, stated rather than assumed: the environment contract is
	// now identical on all three platforms and tested on all three, but the live
	// OSC canary has only ever run on macOS. That Claude-on-Windows emits OSC 777
	// given this identity is inferred from the resolver being platform-independent
	// JS in the same binary, not observed. It needs a Windows rig to become a
	// measurement (ini-m2e).
	cmd.Env = append(agentBaseEnv(),
		fmt.Sprintf("LINES=%d", rows),
		fmt.Sprintf("COLUMNS=%d", cols),
	)
	cmd.Env = append(cmd.Env, cfg.Env...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	return cmd
}
