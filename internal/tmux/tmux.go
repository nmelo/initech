// Package tmux owns tmux CLI interaction at runtime. It provides session
// inspection, window management, Claude process detection, and per-agent
// memory measurement.
//
// All functions take an exec.Runner as the first parameter, making the
// package fully testable without a real tmux installation. This package
// does not know about config, scaffold, or roles.
//
// Claude detection logic is ported from gastools (gasnudge/internal/tmux).
// It detects Claude by checking pane_current_command for "node", "claude",
// or a version pattern (e.g., "2.1.45"). When the pane command is a shell,
// it falls back to checking child processes via pgrep.
package tmux

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	iexec "github.com/nmelo/initech/internal/exec"
)

// Window represents a tmux window with its pane metadata.
type Window struct {
	Index   int
	Name    string
	PaneID  string
	PanePID string
	Command string // pane_current_command
}

// versionPattern matches Claude Code version numbers like "2.1.45".
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// shells we recognize when checking for Claude child processes.
var knownShells = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true, "tcsh": true, "ksh": true,
}

// SessionExists checks if a tmux session with the given name exists.
func SessionExists(runner iexec.Runner, name string) bool {
	_, err := runner.Run("tmux", "has-session", "-t", name)
	return err == nil
}

// ListWindows returns all windows in the specified session with their metadata.
func ListWindows(runner iexec.Runner, session string) ([]Window, error) {
	format := "#{window_index}|#{window_name}|#{pane_id}|#{pane_pid}|#{pane_current_command}"
	out, err := runner.Run("tmux", "list-windows", "-t", session, "-F", format)
	if err != nil {
		return nil, fmt.Errorf("list windows: %w", err)
	}

	var windows []Window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		idx, _ := strconv.Atoi(parts[0])
		windows = append(windows, Window{
			Index:   idx,
			Name:    parts[1],
			PaneID:  parts[2],
			PanePID: parts[3],
			Command: parts[4],
		})
	}
	return windows, nil
}

// IsClaudeRunning checks if Claude appears to be running in the window.
// Detection order:
//  1. Direct command match: "node" or "claude"
//  2. Version pattern match: e.g., "2.1.45" (Claude binary name on some systems)
//  3. Child process fallback: if pane command is a shell, check for claude/node
//     children via pgrep -P
func IsClaudeRunning(runner iexec.Runner, w Window) bool {
	cmd := w.Command

	if cmd == "node" || cmd == "claude" {
		return true
	}
	if versionPattern.MatchString(cmd) {
		return true
	}

	// Shell fallback: check child processes
	if knownShells[cmd] && w.PanePID != "" {
		return hasClaudeChild(runner, w.PanePID)
	}
	return false
}

// GetClaudePID returns the PID of the Claude process in a window, or empty
// string if Claude is not running. Used by GetProcessMemory to measure RSS.
func GetClaudePID(runner iexec.Runner, w Window) string {
	cmd := w.Command

	// If the pane command IS claude/node, the pane PID is the process
	if cmd == "node" || cmd == "claude" || versionPattern.MatchString(cmd) {
		return w.PanePID
	}

	// Shell fallback: find claude/node child
	if knownShells[cmd] && w.PanePID != "" {
		return findClaudeChildPID(runner, w.PanePID)
	}
	return ""
}

// GetProcessMemory returns the resident set size (RSS) in bytes for the Claude
// process tree in a window. Returns 0 if Claude is not running or memory
// cannot be determined.
func GetProcessMemory(runner iexec.Runner, w Window) uint64 {
	pid := GetClaudePID(runner, w)
	if pid == "" {
		return 0
	}

	// Get RSS of the main process and all descendants
	// ps -o rss= returns RSS in kilobytes
	out, err := runner.Run("ps", "-o", "rss=", "-p", pid)
	if err != nil {
		return 0
	}

	var totalKB uint64
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kb, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			continue
		}
		totalKB += kb
	}

	// Also sum child process memory
	childOut, err := runner.Run("pgrep", "-P", pid)
	if err == nil {
		for _, childPID := range strings.Split(strings.TrimSpace(childOut), "\n") {
			childPID = strings.TrimSpace(childPID)
			if childPID == "" {
				continue
			}
			rssOut, err := runner.Run("ps", "-o", "rss=", "-p", childPID)
			if err != nil {
				continue
			}
			kb, err := strconv.ParseUint(strings.TrimSpace(rssOut), 10, 64)
			if err != nil {
				continue
			}
			totalKB += kb
		}
	}

	return totalKB * 1024 // convert KB to bytes
}

// KillWindow kills a specific window in a session.
func KillWindow(runner iexec.Runner, session, window string) error {
	target := session + ":" + window
	_, err := runner.Run("tmux", "kill-window", "-t", target)
	if err != nil {
		return fmt.Errorf("kill window %s: %w", window, err)
	}
	return nil
}

// NewWindow creates a new window in a session with the given name.
func NewWindow(runner iexec.Runner, session, window string) error {
	_, err := runner.Run("tmux", "new-window", "-t", session, "-n", window)
	if err != nil {
		return fmt.Errorf("new window %s: %w", window, err)
	}
	return nil
}

// SendKeys sends text to a tmux target (session:window format).
// The text is sent in literal mode (-l) to handle special characters,
// followed by Enter.
func SendKeys(runner iexec.Runner, target, text string) error {
	if _, err := runner.Run("tmux", "send-keys", "-t", target, "-l", text); err != nil {
		return fmt.Errorf("send keys to %s: %w", target, err)
	}
	if _, err := runner.Run("tmux", "send-keys", "-t", target, "Enter"); err != nil {
		return fmt.Errorf("send Enter to %s: %w", target, err)
	}
	return nil
}

// hasClaudeChild checks if a process has a child running claude or node.
func hasClaudeChild(runner iexec.Runner, pid string) bool {
	return findClaudeChildPID(runner, pid) != ""
}

// findClaudeChildPID returns the PID of a claude/node child process, or empty string.
func findClaudeChildPID(runner iexec.Runner, parentPID string) string {
	out, err := runner.Run("pgrep", "-P", parentPID, "-l")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "PID name" e.g., "29677 node"
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name := parts[1]
			if name == "node" || name == "claude" {
				return parts[0]
			}
		}
	}
	return ""
}
