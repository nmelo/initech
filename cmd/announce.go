package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/webhook"
	"github.com/spf13/cobra"
)

var announceCmd = &cobra.Command{
	Use:   "announce <message>",
	Short: "Send a voice announcement to Agent Radio",
	Long: `Posts a TTS announcement to the announce_url configured in initech.yaml.
Works standalone without a running TUI session.

Examples:
  initech announce "Phase 1 QA passed"
  initech announce --kind deploy.completed "v1.9.1 deployed to production"
  initech announce --agent eng1 --kind agent.completed --bead ini-abc "Auth refactor done"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAnnounce,
}

var (
	announceKind    string
	announceAgent   string
	announceProject string
	announceBead    string
)

func init() {
	announceCmd.Flags().StringVar(&announceKind, "kind", "custom", "Event kind (e.g. agent.completed, deploy.completed, custom)")
	announceCmd.Flags().StringVar(&announceAgent, "agent", "", "Agent name to attribute (default: INITECH_AGENT env var)")
	announceCmd.Flags().StringVar(&announceProject, "project", "", "Project name (default: from initech.yaml)")
	announceCmd.Flags().StringVar(&announceBead, "bead", "", "Bead ID for context")
	rootCmd.AddCommand(announceCmd)
}

func runAnnounce(cmd *cobra.Command, args []string) error {
	message := strings.Join(args, " ")
	if message == "" {
		return fmt.Errorf("message required")
	}

	agent := announceAgent
	if agent == "" {
		agent = os.Getenv("INITECH_AGENT")
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found")
	}
	p, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if p.AnnounceURL == "" {
		return fmt.Errorf("announce_url not configured in initech.yaml\nAdd: announce_url: http://<host>:8001/announce")
	}

	project := announceProject
	if project == "" {
		project = p.Name
	}

	payload := webhook.AnnouncePayload{
		Detail:  message,
		Kind:    announceKind,
		Agent:   agent,
		Project: project,
		BeadID:  announceBead,
	}

	result := webhook.PostAnnouncement(p.AnnounceURL, payload)
	fmt.Fprintln(cmd.ErrOrStderr(), result.Message)
	return result.Err
}
