package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
)

var configGetReveal bool

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a single config value by key",
	Long: `Prints the resolved value for a config key. Output is raw (no key
prefix, no decoration) for easy use in scripts: PORT=$(initech config get mcp_port).

Secret fields are masked unless --reveal is passed.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigGet,
}

func init() {
	configGetCmd.Flags().BoolVar(&configGetReveal, "reveal", false, "Show secret values in cleartext")
	configCmd.AddCommand(configGetCmd)
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	key := args[0]

	// Validate key against registry.
	meta, ok := config.LookupField(key)
	if !ok {
		return fmt.Errorf("unknown config key %q. Run 'initech config list' to see available keys", key)
	}

	// Load config.
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}
	p, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	val, found := resolveConfigValue(p, key)

	// Secret masking.
	if meta.Secret && !configGetReveal && found && val != "" {
		fmt.Fprintln(cmd.OutOrStdout(), "(masked - use --reveal to show)")
		return nil
	}

	if !found || val == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "(not set)")
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), val)
	return nil
}

// resolveConfigValue maps a dot-notation key to the actual value in the
// loaded config. Returns the string value and whether the field was found.
func resolveConfigValue(p *config.Project, key string) (string, bool) {
	// Direct top-level fields.
	switch key {
	case "project":
		return p.Name, true
	case "root":
		return p.Root, true
	case "roles":
		return strings.Join(p.Roles, ","), true
	case "claude_command":
		return strings.Join(p.ClaudeCommand, ","), true
	case "claude_args":
		return strings.Join(p.ClaudeArgs, ","), true
	case "peer_name":
		return p.PeerName, true
	case "mode":
		return p.Mode, true
	case "listen":
		return p.Listen, true
	case "token":
		return p.Token, true
	case "web_port":
		return formatIntPtr(p.WebPort), true
	case "webhook_url":
		return p.WebhookURL, true
	case "announce_url":
		return p.AnnounceURL, true
	case "mcp_port":
		return formatIntPtr(p.McpPort), true
	case "mcp_token":
		return p.EffectiveMcpToken(), true
	case "mcp_bind":
		return p.EffectiveMcpBind(), true

	// Beads.
	case "beads.enabled":
		return strconv.FormatBool(p.Beads.IsEnabled()), true
	case "beads.prefix":
		return p.Beads.Prefix, true

	// Resource.
	case "resource.auto_suspend":
		return strconv.FormatBool(p.Resource.AutoSuspend), true
	case "resource.pressure_threshold":
		v := p.Resource.PressureThreshold
		if v == 0 {
			v = config.DefaultPressureThreshold
		}
		return strconv.Itoa(v), true

	// Slack.
	case "slack.app_token":
		return p.EffectiveSlackAppToken(), true
	case "slack.bot_token":
		return p.EffectiveSlackBotToken(), true
	case "slack.allowed_users":
		return strings.Join(p.Slack.AllowedUsers, ","), true
	case "slack.response_mode":
		return p.Slack.ResponseMode, true
	case "slack.thread_context":
		return strconv.FormatBool(p.Slack.IsThreadContextEnabled()), true
	}

	// Template key resolution: remotes.<name>.field, role_overrides.<role>.field
	parts := strings.Split(key, ".")
	if len(parts) == 3 {
		switch parts[0] {
		case "remotes":
			return resolveRemote(p, parts[1], parts[2])
		case "role_overrides":
			return resolveRoleOverride(p, parts[1], parts[2])
		}
	}

	// repos[].* keys are template-only; not resolvable for a specific index.
	return "", false
}

func resolveRemote(p *config.Project, name, field string) (string, bool) {
	r, ok := p.Remotes[name]
	if !ok {
		return "", true // valid key, remote not configured
	}
	switch field {
	case "addr":
		return r.Addr, true
	case "token":
		return r.Token, true
	}
	return "", false
}

func resolveRoleOverride(p *config.Project, role, field string) (string, bool) {
	ov, ok := p.RoleOverrides[role]
	if !ok {
		return "", true // valid key, role override not configured
	}
	switch field {
	case "command":
		return strings.Join(ov.Command, ","), true
	case "claude_args":
		return strings.Join(ov.ClaudeArgs, ","), true
	case "agent_type":
		return ov.AgentType, true
	case "dir":
		return ov.Dir, true
	case "auto_approve":
		if ov.AutoApprove == nil {
			return "", true
		}
		return strconv.FormatBool(*ov.AutoApprove), true
	case "submit_key":
		return ov.SubmitKey, true
	case "no_bracketed_paste":
		return strconv.FormatBool(ov.NoBracketedPaste), true
	case "tech_stack":
		return ov.TechStack, true
	case "build_cmd":
		return ov.BuildCmd, true
	case "test_cmd":
		return ov.TestCmd, true
	case "repo_name":
		return ov.RepoName, true
	}
	return "", false
}

func formatIntPtr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
