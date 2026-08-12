// Package hooks installs initech's Notification hook into per-agent Claude
// settings (ini-2x8.4), the consent-gated redundancy tier of the attention
// system.
//
// TARGET, decided 2026-08-12: per-role <projectRoot>/<role>/.claude/settings.json
// ONLY -- the directory scaffold already creates. initech NEVER writes
// <projectRoot>/.claude/settings.json: that file is the operator's own tooling
// (it carries tokf and bd hooks today) and governs Claude sessions well beyond
// initech, which makes it the highest-cost file in the tree and precisely the
// one initech should not touch.
//
// The write is CREATE-OR-MERGE. The spec's premise that initech merges into
// settings it already scaffolds is false -- scaffold creates the .claude/
// directory and no settings.json -- so the create case is the common one today
// and gets the same care as the merge case.
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// HookCommand is the command Claude Code runs on a Notification event. It
// reads the payload on stdin; agent identity comes from the INITECH_AGENT and
// INITECH_SOCKET the hook inherits from its parent agent process (verified
// empirically, not assumed -- the payload itself carries no agent identity).
const HookCommand = "initech attention-hook"

// notificationEvent is the settings key Claude Code uses for this hook family.
const notificationEvent = "Notification"

// Result reports what an install did, so callers can tell the operator the
// truth rather than a generic success.
type Result struct {
	Path      string
	Created   bool // A settings.json did not exist and was created.
	Merged    bool // An existing settings.json was preserved and extended.
	Unchanged bool // The hook was already present; nothing was written.
}

// ErrSettingsUnparseable is returned when an existing settings.json cannot be
// parsed. The file is left EXACTLY as found and nothing is installed.
//
// This is the ini-9ka.9 rule applied to a foreign file: a corrupt config is not
// an absent one. Rewriting it would replace the operator's real -- merely
// unparseable -- settings with a near-empty file carrying only our hook,
// converting a loud, recoverable JSON error into silent destruction of their
// tooling. Not installing is always recoverable; overwriting is not.
var ErrSettingsUnparseable = fmt.Errorf("agent settings file is not valid JSON; the Notification hook was NOT installed and the file was left untouched")

// AgentSettingsPath returns the per-role settings file path.
func AgentSettingsPath(projectRoot, role string) string {
	return filepath.Join(projectRoot, role, ".claude", "settings.json")
}

// InstallNotificationHook creates or merges initech's Notification hook into
// one agent's settings.
//
// Merge preserves EVERYTHING it does not own. The file is decoded into a
// generic map rather than a typed struct: a typed struct silently drops every
// field it does not model, which is how a merge clobbers without ever looking
// like one -- the failure is invisible at the diff and only shows up when the
// operator's unrelated tooling stops running.
//
// Idempotent: installing twice leaves exactly one entry.
func InstallNotificationHook(projectRoot, role string) (Result, error) {
	path := AgentSettingsPath(projectRoot, role)
	res := Result{Path: path}

	raw, readErr := os.ReadFile(path)
	settings := map[string]any{}
	switch {
	case readErr == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return res, ErrSettingsUnparseable
		}
		res.Merged = true
	case os.IsNotExist(readErr):
		res.Created = true
	default:
		return res, fmt.Errorf("read agent settings: %w", readErr)
	}

	hooks, err := asMap(settings["hooks"])
	if err != nil {
		return res, ErrSettingsUnparseable
	}
	events, err := asSlice(hooks[notificationEvent])
	if err != nil {
		return res, ErrSettingsUnparseable
	}

	if hasInitechHook(events) {
		res.Unchanged = true
		return res, nil // Already installed: write nothing at all.
	}

	events = append(events, map[string]any{
		"matcher": "",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": HookCommand,
		}},
	})
	hooks[notificationEvent] = events
	settings["hooks"] = hooks

	if err := writeSettingsAtomic(path, settings); err != nil {
		return res, err
	}
	return res, nil
}

// RemoveNotificationHook deletes initech's entry, leaving every other hook and
// key intact. Used when consent is withdrawn.
func RemoveNotificationHook(projectRoot, role string) (Result, error) {
	path := AgentSettingsPath(projectRoot, role)
	res := Result{Path: path}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			res.Unchanged = true
			return res, nil
		}
		return res, fmt.Errorf("read agent settings: %w", err)
	}
	settings := map[string]any{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return res, ErrSettingsUnparseable
	}

	hooks, err := asMap(settings["hooks"])
	if err != nil {
		return res, ErrSettingsUnparseable
	}
	events, err := asSlice(hooks[notificationEvent])
	if err != nil {
		return res, ErrSettingsUnparseable
	}

	kept := make([]any, 0, len(events))
	for _, e := range events {
		if !entryIsInitech(e) {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(events) {
		res.Unchanged = true
		return res, nil
	}
	if len(kept) == 0 {
		// Remove the empty family rather than leaving "Notification": [].
		delete(hooks, notificationEvent)
	} else {
		hooks[notificationEvent] = kept
	}
	settings["hooks"] = hooks
	res.Merged = true
	return res, writeSettingsAtomic(path, settings)
}

// hasInitechHook reports whether our entry is already present.
func hasInitechHook(events []any) bool {
	for _, e := range events {
		if entryIsInitech(e) {
			return true
		}
	}
	return false
}

// entryIsInitech matches on the COMMAND rather than on position or matcher, so
// a hand-reordered or hand-edited settings file is still recognized and does
// not gain a duplicate entry.
func entryIsInitech(entry any) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	inner, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); cmd == HookCommand {
			return true
		}
	}
	return false
}

// asMap coerces a settings value to a map, treating absent as empty and a
// wrong-typed value as unparseable -- a "hooks" key holding a string is a file
// we do not understand, and we must not overwrite what we cannot read.
func asMap(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected type")
	}
	return m, nil
}

func asSlice(v any) ([]any, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected type")
	}
	return s, nil
}

// writeSettingsAtomic writes via temp+rename so a crash mid-write cannot leave
// the operator with a truncated settings file. Indented output because this is
// a file humans read and hand-edit.
func writeSettingsAtomic(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create .claude/: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent settings: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp agent settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename agent settings: %w", err)
	}
	return nil
}
