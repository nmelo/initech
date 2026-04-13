package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configShowReveal bool

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Dump resolved config with source annotations",
	Long:  `Shows every config field with its current value and where the value came from (yaml, env, default, or not set).`,
	RunE:  runConfigShow,
}

func init() {
	configShowCmd.Flags().BoolVar(&configShowReveal, "reveal", false, "Show secret values in cleartext")
	configCmd.AddCommand(configShowCmd)
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run initech init")
	}

	proj, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	yamlKeys, err := parseYAMLKeys(cfgPath)
	if err != nil {
		return fmt.Errorf("parse yaml keys: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s %s\n\n", color.Dim("Config:"), cfgPath)

	lines := resolveAllFields(proj, yamlKeys)
	printConfigLines(out, lines, configShowReveal)

	return nil
}

// configLine holds one resolved field for output.
type configLine struct {
	Key    string
	Value  string
	Source string
	Secret bool
}

// resolveAllFields builds config lines for every registry field plus expanded
// map entries (remotes, role_overrides).
func resolveAllFields(proj *config.Project, yamlKeys map[string]bool) []configLine {
	var lines []configLine

	for _, f := range config.AllFields() {
		// Template fields (contain <name> or <role>) expand from actual map data.
		if strings.Contains(f.Key, "<") {
			continue
		}
		// Array template fields like repos[].url are handled by expandRepos.
		if strings.Contains(f.Key, "[]") {
			continue
		}

		val := resolveFieldValue(proj, f.Key)
		source := determineSource(f, val, yamlKeys)
		lines = append(lines, configLine{
			Key:    f.Key,
			Value:  val,
			Source: source,
			Secret: f.Secret,
		})
	}

	// Expand remotes map.
	if len(proj.Remotes) > 0 {
		names := sortedKeys(proj.Remotes)
		for _, name := range names {
			remote := proj.Remotes[name]
			lines = append(lines, configLine{
				Key:    fmt.Sprintf("remotes.%s.addr", name),
				Value:  remote.Addr,
				Source: "(yaml)",
			})
			if remote.Token != "" {
				lines = append(lines, configLine{
					Key:    fmt.Sprintf("remotes.%s.token", name),
					Value:  remote.Token,
					Source: "(yaml)",
					Secret: true,
				})
			}
		}
	}

	// Expand role_overrides map.
	if len(proj.RoleOverrides) > 0 {
		names := sortedKeys(proj.RoleOverrides)
		for _, name := range names {
			ov := proj.RoleOverrides[name]
			ovLines := expandRoleOverride(name, ov)
			lines = append(lines, ovLines...)
		}
	}

	// Expand repos array.
	for i, repo := range proj.Repos {
		lines = append(lines, configLine{
			Key:    fmt.Sprintf("repos[%d].url", i),
			Value:  repo.URL,
			Source: "(yaml)",
		})
		lines = append(lines, configLine{
			Key:    fmt.Sprintf("repos[%d].name", i),
			Value:  repo.Name,
			Source: "(yaml)",
		})
	}

	return lines
}

// resolveFieldValue extracts the current value of a config field from the
// Project struct. For fields with env var overrides, the effective value is
// returned (env takes priority).
func resolveFieldValue(proj *config.Project, key string) string {
	switch key {
	case "project":
		return proj.Name
	case "root":
		return proj.Root
	case "roles":
		return formatSlice(proj.Roles)
	case "claude_command":
		return formatSlice(proj.ClaudeCommand)
	case "claude_args":
		return formatSlice(proj.ClaudeArgs)
	case "peer_name":
		return proj.PeerName
	case "mode":
		return proj.Mode
	case "listen":
		return proj.Listen
	case "token":
		return proj.Token
	case "web_port":
		if proj.WebPort == nil {
			return "0"
		}
		return fmt.Sprintf("%d", *proj.WebPort)
	case "webhook_url":
		return proj.WebhookURL
	case "announce_url":
		return proj.AnnounceURL
	case "auto_notify":
		return fmt.Sprintf("%t", proj.IsAutoNotifyEnabled())
	case "mcp_port":
		if proj.McpPort == nil {
			return ""
		}
		return fmt.Sprintf("%d", *proj.McpPort)
	case "mcp_token":
		return proj.EffectiveMcpToken()
	case "mcp_bind":
		return proj.EffectiveMcpBind()
	case "beads.enabled":
		return fmt.Sprintf("%t", proj.Beads.IsEnabled())
	case "beads.prefix":
		return proj.Beads.Prefix
	case "resource.auto_suspend":
		return fmt.Sprintf("%t", proj.Resource.AutoSuspend)
	case "resource.pressure_threshold":
		threshold := proj.Resource.PressureThreshold
		if threshold == 0 {
			threshold = config.DefaultPressureThreshold
		}
		return fmt.Sprintf("%d", threshold)
	case "slack.app_token":
		return proj.EffectiveSlackAppToken()
	case "slack.bot_token":
		return proj.EffectiveSlackBotToken()
	case "slack.allowed_users":
		return formatSlice(proj.Slack.AllowedUsers)
	case "slack.response_mode":
		if proj.Slack.ResponseMode == "" {
			return "thread"
		}
		return proj.Slack.ResponseMode
	case "slack.thread_context":
		return fmt.Sprintf("%t", proj.Slack.IsThreadContextEnabled())
	default:
		return ""
	}
}

// determineSource figures out where a field's value came from.
func determineSource(f config.FieldMeta, val string, yamlKeys map[string]bool) string {
	// Check env var override first.
	if f.EnvVar != "" && os.Getenv(f.EnvVar) != "" {
		return fmt.Sprintf("(env: %s)", f.EnvVar)
	}

	// Check if explicitly present in yaml.
	if yamlKeys[f.Key] {
		return "(yaml)"
	}

	// Has a default value?
	if f.Default != "" && val != "" {
		return "(default)"
	}

	// Not set at all.
	if val == "" || val == "[]" {
		return "(not set)"
	}

	return "(default)"
}

// parseYAMLKeys reads the raw YAML file and returns a set of dot-notation keys
// that are explicitly present in the document.
func parseYAMLKeys(cfgPath string) (map[string]bool, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	keys := make(map[string]bool)
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		collectYAMLKeys(doc.Content[0], "", keys)
	}
	return keys, nil
}

// collectYAMLKeys walks a yaml.Node tree and collects dot-notation key paths.
func collectYAMLKeys(node *yaml.Node, prefix string, keys map[string]bool) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		fullKey := keyNode.Value
		if prefix != "" {
			fullKey = prefix + "." + keyNode.Value
		}
		keys[fullKey] = true

		if valNode.Kind == yaml.MappingNode {
			collectYAMLKeys(valNode, fullKey, keys)
		}
	}
}

// printConfigLines formats and writes the config lines with aligned columns.
func printConfigLines(out io.Writer, lines []configLine, reveal bool) {
	// Find max key width for alignment.
	maxKey := 0
	for _, l := range lines {
		if len(l.Key) > maxKey {
			maxKey = len(l.Key)
		}
	}

	// Find max value display width for source alignment.
	maxVal := 0
	for _, l := range lines {
		v := displayValue(l, reveal)
		if len(v) > maxVal {
			maxVal = len(v)
		}
	}
	// Cap value column at 40 to keep sources readable.
	if maxVal > 40 {
		maxVal = 40
	}

	for _, l := range lines {
		v := displayValue(l, reveal)
		fmt.Fprintf(out, "  %-*s = %-*s %s\n",
			maxKey, l.Key,
			maxVal, v,
			color.Dim(l.Source),
		)
	}
}

// displayValue returns the value string, applying secret masking if needed.
func displayValue(l configLine, reveal bool) string {
	if l.Secret && !reveal && l.Value != "" {
		return "****"
	}
	return l.Value
}

// formatSlice formats a string slice as [a, b, c].
func formatSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	return "[" + strings.Join(s, ", ") + "]"
}

// expandRoleOverride produces config lines for a single role override entry.
func expandRoleOverride(name string, ov config.RoleOverride) []configLine {
	var lines []configLine
	prefix := "role_overrides." + name

	if len(ov.Command) > 0 {
		lines = append(lines, configLine{Key: prefix + ".command", Value: formatSlice(ov.Command), Source: "(yaml)"})
	}
	if len(ov.ClaudeArgs) > 0 {
		lines = append(lines, configLine{Key: prefix + ".claude_args", Value: formatSlice(ov.ClaudeArgs), Source: "(yaml)"})
	}
	if ov.AgentType != "" {
		lines = append(lines, configLine{Key: prefix + ".agent_type", Value: ov.AgentType, Source: "(yaml)"})
	}
	if ov.Dir != "" {
		lines = append(lines, configLine{Key: prefix + ".dir", Value: ov.Dir, Source: "(yaml)"})
	}
	if ov.AutoApprove != nil {
		lines = append(lines, configLine{Key: prefix + ".auto_approve", Value: fmt.Sprintf("%t", *ov.AutoApprove), Source: "(yaml)"})
	}
	if ov.SubmitKey != "" {
		lines = append(lines, configLine{Key: prefix + ".submit_key", Value: ov.SubmitKey, Source: "(yaml)"})
	}
	if ov.NoBracketedPaste {
		lines = append(lines, configLine{Key: prefix + ".no_bracketed_paste", Value: "true", Source: "(yaml)"})
	}
	if ov.TechStack != "" {
		lines = append(lines, configLine{Key: prefix + ".tech_stack", Value: ov.TechStack, Source: "(yaml)"})
	}
	if ov.BuildCmd != "" {
		lines = append(lines, configLine{Key: prefix + ".build_cmd", Value: ov.BuildCmd, Source: "(yaml)"})
	}
	if ov.TestCmd != "" {
		lines = append(lines, configLine{Key: prefix + ".test_cmd", Value: ov.TestCmd, Source: "(yaml)"})
	}
	if ov.RepoName != "" {
		lines = append(lines, configLine{Key: prefix + ".repo_name", Value: ov.RepoName, Source: "(yaml)"})
	}
	return lines
}

// sortedKeys returns map keys sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
