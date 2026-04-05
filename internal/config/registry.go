package config

import "strings"

// FieldMeta describes a single config field for the config CLI subcommands.
// The Registry slice is the single source of truth for field names, types,
// defaults, and descriptions. All config CLI commands read from this registry
// rather than hardcoding field knowledge.
type FieldMeta struct {
	Key         string // Dot-notation path (e.g., "beads.prefix", "remotes.<name>.addr").
	Type        string // "string", "int", "bool", "[]string", "map".
	Default     string // Human-readable default value.
	Description string // One-line description for list/show output.
	EnvVar      string // Override env var name (e.g., "INITECH_MCP_TOKEN"). Empty if none.
	Secret      bool   // Mask value in show/get output unless --reveal.
	Restart     bool   // True if changing this field requires TUI restart.
}

// Registry contains metadata for every config field in initech.yaml. Ordering
// determines list output order. Template keys use angle-bracket placeholders
// for map-typed fields (e.g., remotes.<name>.addr).
var Registry = []FieldMeta{
	// Top-level project fields.
	{Key: "project", Type: "string", Description: "Project name used for session identification and socket paths"},
	{Key: "root", Type: "string", Description: "Absolute path to the project root directory"},
	{Key: "roles", Type: "[]string", Description: "Agent roles to create workspaces for", Restart: true},
	{Key: "claude_command", Type: "[]string", Default: `["claude"]`, Description: "Base command to launch Claude Code agents"},
	{Key: "claude_args", Type: "[]string", Description: "Default CLI arguments appended to every agent launch"},
	{Key: "peer_name", Type: "string", Description: "This instance's identity for cross-machine addressing (e.g., \"laptop\")"},
	{Key: "mode", Type: "string", Default: "tui", Description: "Run mode: empty string for TUI, \"headless\" for daemon"},
	{Key: "listen", Type: "string", Description: "TCP listen address for headless mode (e.g., \"0.0.0.0:7391\")", Restart: true},
	{Key: "token", Type: "string", Description: "Shared auth token for cross-machine coordination", Secret: true},

	// Web companion.
	{Key: "web_port", Type: "int", Default: "0 (disabled)", Description: "Web companion server port. 0 or omitted disables it", Restart: true},
	{Key: "webhook_url", Type: "string", Description: "HTTP endpoint for agent event webhook POSTs. Empty disables"},
	{Key: "announce_url", Type: "string", Description: "Agent Radio webhook URL for TTS announcements. Empty disables"},

	// MCP server.
	{Key: "mcp_port", Type: "int", Default: "9200", Description: "MCP server port. 0 disables", Restart: true},
	{Key: "mcp_token", Type: "string", Description: "Bearer token for MCP server auth. Auto-generated if empty", EnvVar: "INITECH_MCP_TOKEN", Secret: true},
	{Key: "mcp_bind", Type: "string", Default: "0.0.0.0", Description: "MCP server bind address"},

	// Beads issue tracker.
	{Key: "beads.enabled", Type: "bool", Default: "true", Description: "Enable beads issue tracker integration"},
	{Key: "beads.prefix", Type: "string", Description: "Bead ID prefix (e.g., \"ini\" produces ini-abc)"},

	// Resource management.
	{Key: "resource.auto_suspend", Type: "bool", Default: "false", Description: "Auto-suspend agents under memory pressure"},
	{Key: "resource.pressure_threshold", Type: "int", Default: "85", Description: "RSS percentage (0-100) that triggers auto-suspend"},

	// Slack chat integration.
	{Key: "slack.app_token", Type: "string", Description: "Slack app-level token (xapp-...) for Socket Mode", EnvVar: "INITECH_SLACK_APP_TOKEN", Secret: true, Restart: true},
	{Key: "slack.bot_token", Type: "string", Description: "Slack bot token (xoxb-...) for Web API calls", EnvVar: "INITECH_SLACK_BOT_TOKEN", Secret: true, Restart: true},
	{Key: "slack.allowed_users", Type: "[]string", Description: "Slack user IDs allowed to dispatch commands. Empty allows all"},
	{Key: "slack.response_mode", Type: "string", Default: "thread", Description: "Reply mode: \"thread\" (default) or \"channel\""},
	{Key: "slack.thread_context", Type: "bool", Default: "true", Description: "Fetch thread history and prepend as context for dispatched messages"},

	// Repos.
	{Key: "repos", Type: "[]object", Description: "Code repositories cloned as agent workspace submodules"},
	{Key: "repos[].url", Type: "string", Description: "Git clone URL for agent submodule"},
	{Key: "repos[].name", Type: "string", Description: "Local directory name for the repo submodule"},

	// Remotes (template: one entry per named remote peer).
	{Key: "remotes.<name>.addr", Type: "string", Description: "Host:port of the remote peer"},
	{Key: "remotes.<name>.token", Type: "string", Description: "Auth token for this remote (overrides project-level token)", Secret: true},

	// Role overrides (template: one entry per role name).
	{Key: "role_overrides.<role>.command", Type: "[]string", Description: "Override the agent launch command entirely"},
	{Key: "role_overrides.<role>.claude_args", Type: "[]string", Description: "Additional CLI arguments for this role's agent"},
	{Key: "role_overrides.<role>.agent_type", Type: "string", Default: "claude-code", Description: "Agent type: \"claude-code\", \"codex\", \"opencode\", or \"generic\""},
	{Key: "role_overrides.<role>.dir", Type: "string", Description: "Custom working directory for this role"},
	{Key: "role_overrides.<role>.auto_approve", Type: "bool", Description: "Auto-approve matching permission prompts for this role"},
	{Key: "role_overrides.<role>.submit_key", Type: "string", Default: "enter", Description: "Submit key: \"enter\" (default) or \"ctrl+enter\""},
	{Key: "role_overrides.<role>.no_bracketed_paste", Type: "bool", Default: "false", Description: "Use raw typed input instead of bracketed paste"},
	{Key: "role_overrides.<role>.tech_stack", Type: "string", Description: "Tech stack hint injected into the role template"},
	{Key: "role_overrides.<role>.build_cmd", Type: "string", Description: "Build command injected into the role template"},
	{Key: "role_overrides.<role>.test_cmd", Type: "string", Description: "Test command injected into the role template"},
	{Key: "role_overrides.<role>.repo_name", Type: "string", Description: "Repo submodule name when multiple repos are configured"},
}

// AllFields returns the full registry. Callers should not modify the slice.
func AllFields() []FieldMeta {
	return Registry
}

// LookupField finds metadata by key. Exact matches are tried first, then
// template patterns where angle-bracket segments (<name>, <role>) match any
// value. Returns the metadata and true if found, or zero value and false.
func LookupField(key string) (FieldMeta, bool) {
	// Exact match first.
	for _, f := range Registry {
		if f.Key == key {
			return f, true
		}
	}
	// Template match: split both key and template by dots, compare segment
	// by segment. A template segment wrapped in <> matches any value.
	keyParts := strings.Split(key, ".")
	for _, f := range Registry {
		tmplParts := strings.Split(f.Key, ".")
		if matchTemplate(keyParts, tmplParts) {
			return f, true
		}
	}
	return FieldMeta{}, false
}

// IsSecret returns true if the field should be masked in output.
func IsSecret(key string) bool {
	f, ok := LookupField(key)
	return ok && f.Secret
}

// matchTemplate checks whether concrete key parts match a template pattern.
// Template segments like "<name>" or "<role>" match any non-empty value.
// Bracket segments like "[]" in "repos[].url" also match any value.
func matchTemplate(key, tmpl []string) bool {
	if len(key) != len(tmpl) {
		return false
	}
	for i := range key {
		if tmpl[i] == key[i] {
			continue
		}
		if len(tmpl[i]) > 2 && tmpl[i][0] == '<' && tmpl[i][len(tmpl[i])-1] == '>' {
			continue // template wildcard
		}
		// Handle array bracket notation: "repos[]" matches "repos[]".
		return false
	}
	return true
}
