package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Claude's notification channel, injected rather than left to terminal
// auto-detection (ini-2fd, operator decision 2026-08-13).
//
// THE CLASS THIS ENDS. Claude picks its notification channel by resolving one
// terminal name from a fixed list of env vars, and only the ghostty branch
// emits the OSC 777 that tier-1 attention detection reads. ini-g0h pinned
// TERM_PROGRAM; ini-m2e then measured four OTHER variables that Claude
// consults BEFORE TERM_PROGRAM, three of which carry real non-terminal freight
// (git askpass, Apple tooling, MSBuild) and so could not be scrubbed. Cursor-,
// Windsurf-, JetBrains- and Visual Studio-hosted operators were left with
// silently dead tier-1. Enumerating those inputs is a losing game: the
// resolution order belongs to Claude and can grow at any time.
//
// preferredNotifChannel is read BEFORE terminal auto-detection, so setting it
// bypasses the whole resolution order and ends the class rather than playing
// another round of it.
//
// MEASURED at Claude Code 2.1.231, macOS arm64, one variable at a time,
// observable = which OSC the child actually emits (see
// notif_channel_probe_test.go for the rig):
//
//	project settings.json channel=iterm2, no flag   -> OSC 9    (files are honored)
//	settings.local.json channel=iterm2, no flag     -> OSC 9    (local tier honored)
//	project file iterm2 + injected ghostty flag     -> OSC 777  (THE FLAG OUTRANKS THE FILE)
//	injected ghostty flag alone                     -> OSC 777 in 11.3s
//	3 shadow markers + injected flag                -> OSC 777, all three rescued
//	jetbrains marker, no flag                       -> silent 75s, dialog reached
//
// The third row is why absence-checking below is load-bearing rather than
// merely polite: --settings beats the operator's own settings files, so
// Claude will NOT protect a user who set a channel deliberately. Initech has
// to, or the injection silently overrides a choice the operator made and the
// release notes promised to respect.
const notifChannelSettingKey = "preferredNotifChannel"

// notifChannelSettings is the JSON blob injected via --settings. The channel
// is pinnedTermProgram, not a second "ghostty" literal: the channel and the
// pinned terminal identity are the same claim about the PTY initech hands the
// agent, and a literal here could drift from pane_env.go without anything
// failing.
func notifChannelSettings() string {
	blob, err := json.Marshal(map[string]string{notifChannelSettingKey: pinnedTermProgram})
	if err != nil {
		return ""
	}
	return string(blob)
}

// NotifChannelArgs returns the --settings arguments to append to a Claude
// agent's command line, or nil when initech must not inject.
//
// workDir is the agent pane's working directory, the root of the project
// settings search. existingArgs is the fully resolved argument list initech
// would otherwise run.
//
// It returns nil in two cases, both measured rather than assumed:
//
//   - The operator already passes their own --settings. Two --settings flags
//     do NOT merge per key: the LATER one replaces the earlier wholesale
//     (measured -- injecting {"preferredNotifChannel":"iterm2"} before an
//     operator blob carrying no channel produced ghostty auto-detection, not
//     iterm2). So appending ours would discard the operator's entire settings
//     blob -- their permissions, hooks, everything -- to set one key, and
//     prepending ours would have it discarded in turn. Neither composes, so
//     initech stands down and the operator's flag is left as the only one.
//
//   - An explicit preferredNotifChannel already exists anywhere in the
//     operator's settings resolution. That is the operator decision: an
//     explicit user value always wins, even at the cost of a dead chime.
func NotifChannelArgs(workDir string, existingArgs []string) []string {
	if hasSettingsFlag(existingArgs) {
		return nil
	}
	if operatorSetNotifChannel(workDir) {
		return nil
	}
	blob := notifChannelSettings()
	if blob == "" {
		return nil
	}
	return []string{"--settings", blob}
}

// hasSettingsFlag reports whether the arguments already carry a --settings
// flag, in either the separate-argument or the --settings=VALUE form.
func hasSettingsFlag(args []string) bool {
	for _, a := range args {
		if a == "--settings" || strings.HasPrefix(a, "--settings=") {
			return true
		}
	}
	return false
}

// operatorSetNotifChannel reports whether an explicit preferredNotifChannel
// exists anywhere in the operator's own Claude settings resolution.
//
// It checks every tier Claude resolves, not one file: the managed/policy
// files, the user file, the server-managed cache, and every project and local
// settings file from workDir up to the filesystem root (Claude resolves
// project settings relative to the repo root, which is an ANCESTOR of the
// per-role directory initech runs agents in -- checking only workDir would
// miss the settings file of the very repo the agent works in).
//
// It fails CLOSED: an unreadable or malformed settings file counts as "the
// operator set one", so initech declines to inject. The two error directions
// are not symmetric. Wrongly believing a channel exists costs a chime that was
// probably dead anyway -- the status quo. Wrongly believing none exists
// overrides a deliberate user preference, which is the one outcome the
// operator decision forbids.
func operatorSetNotifChannel(workDir string) bool {
	paths, ok := notifSettingsPaths(workDir, runtime.GOOS)
	if !ok {
		// The user tier could not be located at all, so absence cannot be
		// verified. Same asymmetry as an unreadable file: decline to inject.
		return true
	}
	for _, p := range paths {
		set, err := settingsFileSetsNotifChannel(p)
		if err != nil {
			return true // fail closed
		}
		if set {
			return true
		}
	}
	return false
}

// homeEnvVar is the environment variable that names the home directory on the
// given OS. It mirrors Go's os.UserHomeDir AND Node's os.homedir, which agree:
// Windows reads USERPROFILE and consults HOME not at all.
//
// This is measured, not assumed, and it corrects the obvious guess. The
// Windows CI failure that reopened ini-2fd looked like initech resolving the
// user tier in a POSIX-shaped way, but Claude Code resolves it as
// CLAUDE_CONFIG_DIR || join(homedir(), ".claude") -- and on Windows that
// homedir is USERPROFILE, exactly what Go returns. The two agree on every
// platform. What was actually broken was the TEST, which set HOME and so
// relocated nothing on Windows, plus the three fail-open holes below.
func homeEnvVar(goos string) string {
	switch goos {
	case "windows":
		return "USERPROFILE"
	case "plan9":
		return "home"
	default:
		return "HOME"
	}
}

// managedSettingsPath is the OS-specific managed/policy settings file.
// Measured from the shipped 2.1.231 bundle, which selects:
//
//	macos -> /Library/Application Support/ClaudeCode
//	windows -> C:\Program Files\ClaudeCode
//	default -> /etc/claude-code
//
// The Windows arm was missing entirely before ini-2fd's reopen.
func managedSettingsPath(goos string) string {
	switch goos {
	case "darwin":
		return filepath.Join("/Library/Application Support/ClaudeCode", "managed-settings.json")
	case "windows":
		return filepath.Join(`C:\Program Files\ClaudeCode`, "managed-settings.json")
	default:
		return filepath.Join("/etc/claude-code", "managed-settings.json")
	}
}

// userConfigDir resolves the directory Claude reads USER settings from, the
// way the shipped bundle does: CLAUDE_CONFIG_DIR when set, otherwise
// <home>/.claude. When CLAUDE_CONFIG_DIR is set it IS the config directory,
// not the parent of one.
//
// The bool is false when the directory cannot be determined, which callers
// MUST treat as "the operator may have set a channel we cannot see". Returning
// a best-effort path here, or silently skipping the tier as the first version
// did, fails OPEN: the injection then lands on top of a user-set channel,
// which is the one outcome the operator decision forbids.
func userConfigDir(goos string) (string, bool) {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return d, true
	}
	home := strings.TrimSpace(os.Getenv(homeEnvVar(goos)))
	if home == "" {
		return "", false
	}
	return filepath.Join(home, ".claude"), true
}

// notifSettingsPaths lists every settings file Claude's resolution honors, for
// the given agent working directory. ok is false when the user tier cannot be
// located, so the caller can fail closed rather than check a short list and
// conclude absence from it.
func notifSettingsPaths(workDir, goos string) ([]string, bool) {
	paths := []string{managedSettingsPath(goos)}

	cfgDir, ok := userConfigDir(goos)
	if !ok {
		return nil, false
	}
	paths = append(paths,
		filepath.Join(cfgDir, "settings.json"),
		filepath.Join(cfgDir, "remote-settings.json"),
	)

	if workDir != "" {
		if abs, err := filepath.Abs(workDir); err == nil {
			for dir := abs; ; {
				paths = append(paths,
					filepath.Join(dir, ".claude", "settings.json"),
					filepath.Join(dir, ".claude", "settings.local.json"),
				)
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
	}
	return paths, true
}

// settingsFileSetsNotifChannel reports whether one settings file sets a
// non-empty preferredNotifChannel. A missing file is not an error and is not
// a value; anything present but unreadable or unparseable IS an error, so the
// caller can fail closed rather than treat a broken file as consent.
func settingsFileSetsNotifChannel(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false, err
	}
	v, ok := parsed[notifChannelSettingKey]
	if !ok {
		return false, nil
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != "", nil
}
