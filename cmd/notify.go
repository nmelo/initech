package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/webhook"
	"github.com/spf13/cobra"
)

var notifyCmd = &cobra.Command{
	Use:   "notify <message>",
	Short: "Post a notification to the configured webhook",
	Long: `Posts a JSON notification to the webhook_url configured in initech.yaml.
Works standalone without a running TUI session.

Examples:
  initech notify "Build passed, ready to ship"
  initech notify --kind deploy "v1.9.1 deployed to production"
  initech notify --kind release --agent shipper "v1.9.1 released to Homebrew"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runNotify,
}

var (
	notifyKind  string
	notifyAgent string
)

func init() {
	notifyCmd.Flags().StringVar(&notifyKind, "kind", "custom", "Event kind (e.g. deploy, release, milestone, custom)")
	notifyCmd.Flags().StringVar(&notifyAgent, "agent", "", "Agent name to attribute (default: INITECH_AGENT env var)")
	rootCmd.AddCommand(notifyCmd)
}

func runNotify(cmd *cobra.Command, args []string) error {
	message := strings.Join(args, " ")
	if message == "" {
		return fmt.Errorf("message required")
	}

	agent := notifyAgent
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

	if p.WebhookURL == "" {
		return fmt.Errorf("no webhook_url configured in initech.yaml")
	}

	if err := webhook.PostNotification(p.WebhookURL, notifyKind, agent, message, p.Name); err != nil {
		return err
	}

	fmt.Fprintln(cmd.ErrOrStderr(), "notification sent")
	return nil
}
