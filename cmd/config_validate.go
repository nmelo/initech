package cmd

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Check config for errors before starting",
	Long:  `Runs the same validation checks that run at startup but without starting the TUI. Reports PASS, WARN, ERROR, or INFO for each check.`,
	RunE:  runConfigValidate,
}

func init() {
	configCmd.AddCommand(configValidateCmd)
}

// validationResult holds one check outcome.
type validationResult struct {
	Level  string // "PASS", "WARN", "ERROR", "INFO"
	Label  string // e.g. "project", "roles", "mcp_port"
	Detail string // human-readable description
}

func runConfigValidate(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		cmd.PrintErrln(color.RedBold("ERROR"), " no initech.yaml found. Run initech init.")
		return fmt.Errorf("no initech.yaml found")
	}

	// Read raw yaml to check for parse errors and unknown fields.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var rawNode yaml.Node
	if err := yaml.Unmarshal(data, &rawNode); err != nil {
		cmd.PrintErrln(color.RedBold("ERROR"), " YAML parse error: ", err)
		return fmt.Errorf("yaml parse error")
	}

	// Parse into Project struct (without Validate, we run our own checks).
	var proj config.Project
	if err := yaml.Unmarshal(data, &proj); err != nil {
		cmd.PrintErrln(color.RedBold("ERROR"), " config parse error: ", err)
		return fmt.Errorf("config parse error")
	}
	proj.Root = expandRootForValidation(proj.Root)

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s %s\n\n", color.Dim("Config:"), cfgPath)

	results := runValidationChecks(&proj, cfgPath, &rawNode)
	printValidationResults(out, results)

	errors := 0
	warnings := 0
	infos := 0
	for _, r := range results {
		switch r.Level {
		case "ERROR":
			errors++
		case "WARN":
			warnings++
		case "INFO":
			infos++
		}
	}

	fmt.Fprintln(out)
	summary := fmt.Sprintf("%d error(s), %d warning(s), %d info", errors, warnings, infos)
	if errors > 0 {
		fmt.Fprintln(out, color.RedBold(summary))
		return fmt.Errorf("%d validation error(s)", errors)
	}
	if warnings > 0 {
		fmt.Fprintln(out, color.YellowBold(summary))
	} else {
		fmt.Fprintln(out, color.GreenBold(summary))
	}
	return nil
}

// runValidationChecks performs all validation and returns structured results.
func runValidationChecks(proj *config.Project, cfgPath string, rawNode *yaml.Node) []validationResult {
	var results []validationResult

	// a. project field non-empty.
	if proj.Name == "" {
		results = append(results, validationResult{"ERROR", "project", "project name is required"})
	} else {
		results = append(results, validationResult{"PASS", "project", fmt.Sprintf("%q", proj.Name)})
	}

	// b. root directory exists and is a directory.
	if proj.Root == "" {
		results = append(results, validationResult{"ERROR", "root", "root path is required"})
	} else if info, err := os.Stat(proj.Root); err != nil {
		results = append(results, validationResult{"ERROR", "root", fmt.Sprintf("directory does not exist: %s", proj.Root)})
	} else if !info.IsDir() {
		results = append(results, validationResult{"ERROR", "root", fmt.Sprintf("not a directory: %s", proj.Root)})
	} else {
		results = append(results, validationResult{"PASS", "root", "exists, is a directory"})
	}

	// c. roles list non-empty.
	if len(proj.Roles) == 0 {
		results = append(results, validationResult{"ERROR", "roles", "no roles defined"})
	} else {
		results = append(results, validationResult{"PASS", "roles", fmt.Sprintf("%d roles defined", len(proj.Roles))})
	}

	// d. Each role has a workspace directory.
	roleSet := make(map[string]bool, len(proj.Roles))
	for _, role := range proj.Roles {
		roleSet[role] = true
		wsDir := role
		if proj.Root != "" {
			wsDir = proj.Root + "/" + role
		}
		if _, err := os.Stat(wsDir); err != nil {
			results = append(results, validationResult{"WARN", role, "workspace directory not found (run initech init)"})
		}
	}

	// e. Port numbers in valid range.
	if proj.WebPort != nil && *proj.WebPort != 0 {
		if *proj.WebPort < 1 || *proj.WebPort > 65535 {
			results = append(results, validationResult{"ERROR", "web_port", fmt.Sprintf("%d out of range (1-65535)", *proj.WebPort)})
		} else {
			results = append(results, validationResult{"PASS", "web_port", fmt.Sprintf("%d", *proj.WebPort)})
		}
	}
	if proj.McpPort != nil && *proj.McpPort != 0 {
		if *proj.McpPort < 1 || *proj.McpPort > 65535 {
			results = append(results, validationResult{"ERROR", "mcp_port", fmt.Sprintf("%d out of range (1-65535)", *proj.McpPort)})
		} else {
			results = append(results, validationResult{"PASS", "mcp_port", fmt.Sprintf("%d", *proj.McpPort)})
		}
	}
	if proj.Listen != "" {
		host, _, err := net.SplitHostPort(proj.Listen)
		if err != nil {
			results = append(results, validationResult{"ERROR", "listen", fmt.Sprintf("malformed address: %s", proj.Listen)})
		} else {
			results = append(results, validationResult{"PASS", "listen", proj.Listen})
			_ = host
		}
	}

	// f. mcp_bind validation.
	if proj.McpBind != "" && proj.McpBind != "0.0.0.0" && proj.McpBind != "127.0.0.1" && proj.McpBind != "localhost" {
		ip := net.ParseIP(proj.McpBind)
		if ip == nil {
			results = append(results, validationResult{"WARN", "mcp_bind", fmt.Sprintf("unusual bind address: %s", proj.McpBind)})
		}
	}

	// g. Role overrides reference valid roles.
	for name := range proj.RoleOverrides {
		if !roleSet[name] {
			results = append(results, validationResult{"ERROR", "role_overrides." + name, fmt.Sprintf("role %q not in roles list", name)})
		}
	}

	// h. Remote addresses parseable as host:port.
	for name, remote := range proj.Remotes {
		if remote.Addr == "" {
			results = append(results, validationResult{"ERROR", "remotes." + name, "empty addr"})
		} else if _, _, err := net.SplitHostPort(remote.Addr); err != nil {
			results = append(results, validationResult{"ERROR", "remotes." + name, fmt.Sprintf("malformed addr %q: %s", remote.Addr, err)})
		}
	}

	// i. beads.prefix when enabled.
	if proj.Beads.IsEnabled() && proj.Beads.Prefix == "" {
		results = append(results, validationResult{"WARN", "beads.prefix", "beads enabled but no prefix set"})
	}

	// j. webhook_url.
	if proj.WebhookURL == "" {
		results = append(results, validationResult{"INFO", "webhook_url", "not configured (initech notify disabled)"})
	}

	// k. announce_url.
	if proj.AnnounceURL == "" {
		results = append(results, validationResult{"INFO", "announce_url", "not configured (initech announce disabled)"})
	}

	// l. Unknown yaml fields.
	if rawNode.Kind == yaml.DocumentNode && len(rawNode.Content) > 0 {
		unknowns := findUnknownFields(rawNode.Content[0], "")
		for _, u := range unknowns {
			results = append(results, validationResult{"WARN", u, "unknown field (possible typo)"})
		}
	}

	return results
}

// findUnknownFields walks the yaml.Node tree and returns dot-notation keys
// that don't match any registry entry.
func findUnknownFields(node *yaml.Node, prefix string) []string {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	var unknowns []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		fullKey := keyNode.Value
		if prefix != "" {
			fullKey = prefix + "." + keyNode.Value
		}

		if _, ok := config.LookupField(fullKey); !ok {
			// Not a direct match. If the value is a map, check if it could be
			// a template key (e.g., remotes.workbench maps to remotes.<name>).
			if valNode.Kind == yaml.MappingNode {
				// Recurse into the sub-map to check its children.
				subUnknowns := findUnknownFields(valNode, fullKey)
				unknowns = append(unknowns, subUnknowns...)
			} else {
				unknowns = append(unknowns, fullKey)
			}
		} else if valNode.Kind == yaml.MappingNode {
			// Known field with sub-keys: recurse.
			subUnknowns := findUnknownFields(valNode, fullKey)
			unknowns = append(unknowns, subUnknowns...)
		}
	}
	return unknowns
}

// printValidationResults formats check results with colored level tags.
func printValidationResults(out io.Writer, results []validationResult) {
	for _, r := range results {
		var tag string
		switch r.Level {
		case "PASS":
			tag = color.Green("PASS ")
		case "WARN":
			tag = color.YellowBold("WARN ")
		case "ERROR":
			tag = color.RedBold("ERROR")
		case "INFO":
			tag = color.Cyan("INFO ")
		}
		fmt.Fprintf(out, "  %s  %s: %s\n", tag, r.Label, r.Detail)
	}
}

// expandRootForValidation expands ~ in root path (mirrors config.Load behavior
// without running the full Load+Validate pipeline).
func expandRootForValidation(root string) string {
	if !strings.HasPrefix(root, "~") {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return root
	}
	return home + root[1:]
}
