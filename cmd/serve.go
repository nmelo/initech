package cmd

import (
	"fmt"
	"os"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var (
	servePort  int
	serveToken string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run headless daemon for remote TUI connections",
	Long: `Launches a headless daemon and listens on TCP for yamux connections
from remote TUI clients.

Zero-config mode (no initech.yaml):
  initech serve               # listen on 0.0.0.0:9090, auto-generate token
  initech serve --port 9091   # custom port
  initech serve --token abc   # custom token

Config mode (initech.yaml with mode: headless):
  initech serve               # uses initech.yaml settings`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 9090, "TCP port to listen on (zero-config mode)")
	serveCmd.Flags().StringVar(&serveToken, "token", "", "Auth token (overrides auto-generated token)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, cfgErr := config.Discover(wd)
	if cfgErr == nil {
		return runServeWithConfig(cmd, cfgPath)
	}

	return runServeZeroConfig(cmd, wd)
}

// runServeWithConfig is the existing path: initech.yaml found.
func runServeWithConfig(cmd *cobra.Command, cfgPath string) error {
	proj, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if proj.Mode != "headless" {
		return fmt.Errorf("initech serve requires mode: headless in initech.yaml")
	}

	agents := make([]tui.PaneConfig, 0, len(proj.Roles))
	for _, roleName := range proj.Roles {
		pcfg, err := buildAgentPaneConfig(roleName, proj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
		agents = append(agents, pcfg)
	}

	if len(agents) == 0 {
		return fmt.Errorf("no valid role directories found")
	}

	return tui.RunDaemon(tui.DaemonConfig{
		Project: proj,
		Agents:  agents,
		Version: Version,
		Verbose: verbose,
		WebPort: proj.EffectiveWebPort(),
	})
}

// runServeZeroConfig starts a bare daemon with no initech.yaml.
func runServeZeroConfig(cmd *cobra.Command, wd string) error {
	initechDir := ".initech"

	token := serveToken
	if token == "" {
		token = os.Getenv("INITECH_TOKEN")
	}
	if token == "" {
		var err error
		token, err = tui.ReadOrCreateToken(initechDir)
		if err != nil {
			return err
		}
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "remote"
	}

	listen := fmt.Sprintf("0.0.0.0:%d", servePort)

	proj := &config.Project{
		Name:     "initech",
		Root:     wd,
		PeerName: hostname,
		Listen:   listen,
		Token:    token,
		Mode:     "headless",
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Token: %s\n", token)
	fmt.Fprintf(out, "Listening on %s\n", listen)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Add to your local initech.yaml:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "remotes:")
	fmt.Fprintf(out, "  %s:\n", hostname)
	fmt.Fprintf(out, "    addr: <this-machine-ip>:%d\n", servePort)
	fmt.Fprintf(out, "    token: %s\n", token)
	fmt.Fprintln(out, "    roles: []")
	fmt.Fprintln(out)

	return tui.RunDaemon(tui.DaemonConfig{
		Project: proj,
		Agents:  nil,
		Version: Version,
		Verbose: verbose,
	})
}
