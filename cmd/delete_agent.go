package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var deleteAgentPurge bool

// confirmReader is the input source for --purge confirmation. Overridable for tests.
var confirmReader = func() *bufio.Reader { return bufio.NewReader(os.Stdin) }

var deleteAgentCmd = &cobra.Command{
	Use:     "delete-agent <name>",
	Aliases: []string{"fire"},
	Short:   "Remove an agent from the project",
	Long: `Removes a role from initech.yaml and optionally deletes its workspace.

If a TUI session is running, the agent is also hot-removed from the live session.
The workspace directory is preserved by default. Use --purge to delete it.`,
	Args: cobra.ExactArgs(1),
	RunE: runDeleteAgent,
}

func init() {
	deleteAgentCmd.Flags().BoolVar(&deleteAgentPurge, "purge", false, "Delete the workspace directory after removal")
	rootCmd.AddCommand(deleteAgentCmd)
}

func runDeleteAgent(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	roleName := args[0]

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

	found := false
	var newRoles []string
	for _, r := range p.Roles {
		if r == roleName {
			found = true
			continue
		}
		newRoles = append(newRoles, r)
	}
	if !found {
		return fmt.Errorf("agent %q not found in initech.yaml roles", roleName)
	}

	p.Roles = newRoles
	if p.RoleOverrides != nil {
		delete(p.RoleOverrides, roleName)
	}

	if err := config.Write(cfgPath, p); err != nil {
		return fmt.Errorf("update config: %w", err)
	}
	fmt.Fprintf(out, "  %s Removed %s from %s\n", color.Green("✓"), color.Bold(roleName), color.Bold("initech.yaml"))

	// Hot-remove from running TUI if a session is active.
	if sockPath, _, sockErr := discoverSocket(); sockErr == nil {
		resp, ipcErr := ipcCallSocket(sockPath, tui.IPCRequest{
			Action: "remove",
			Target: roleName,
		})
		if ipcErr != nil {
			fmt.Fprintf(out, "  %s Could not hot-remove from TUI: %v\n", color.Yellow("!"), ipcErr)
		} else if !resp.OK {
			fmt.Fprintf(out, "  %s TUI: %s\n", color.Yellow("!"), resp.Error)
		} else {
			fmt.Fprintf(out, "  %s Removed from running session\n", color.Green("✓"))
		}
	}

	if deleteAgentPurge {
		wsDir := filepath.Join(p.Root, roleName)
		if _, statErr := os.Stat(wsDir); statErr == nil {
			reader := confirmReader()
			fmt.Fprintf(out, "\n  Delete %s and all its contents? [y/N] ", color.Bold(wsDir))
			line, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(line)) != "y" {
				fmt.Fprintf(out, "  Skipped. Workspace preserved.\n")
			} else {
				if err := os.RemoveAll(wsDir); err != nil {
					return fmt.Errorf("delete workspace: %w", err)
				}
				fmt.Fprintf(out, "  %s Deleted %s\n", color.Green("✓"), color.Bold(roleName+"/"))
			}
		}
	}

	return nil
}
