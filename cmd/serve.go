package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var (
	servePort      int
	serveToken     string
	serveShowToken bool
)

// runDaemon is tui.RunDaemon, a seam so tests run the real serve path
// without starting a listener.
var runDaemon = tui.RunDaemon

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run headless daemon for remote TUI connections",
	Long: `Launches a headless daemon and listens on TCP for yamux connections
from remote TUI clients.

Zero-config mode (no initech.yaml):
  initech serve               # listen on 0.0.0.0:9090, auto-generate token
  initech serve --port 9091   # custom port
  initech serve --show-token  # print the stored token and exit

The token is created on the first run, stored in .initech/token (mode 0600)
and printed that one time, in the connection snippet. Later starts say where
it is stored, never its value: a daemon under a supervisor would otherwise
write the key that opens every agent's terminal into its log on each start.

Config mode (initech.yaml with mode: headless):
  initech serve               # uses initech.yaml settings`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 9090, "TCP port to listen on (zero-config mode)")
	serveCmd.Flags().StringVar(&serveToken, "token", "", "Deprecated: a token on the command line is visible to other users (ps, shell history). Use the token file (.initech/token) instead. Overrides it for this run; never printed")
	serveCmd.Flags().BoolVar(&serveShowToken, "show-token", false, "Print the stored token and exit (initech.yaml token in config mode, .initech/token otherwise); never creates one")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, cfgErr := config.Discover(wd)
	if serveShowToken {
		return showServeToken(cmd, cfgPath, cfgErr, wd)
	}
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

	return runDaemon(tui.DaemonConfig{
		Project:    proj,
		Agents:     agents,
		Version:    Version,
		Verbose:    verbose,
		BuildAgent: buildAgentPaneConfig,
	})
}

// runServeZeroConfig starts a bare daemon with no initech.yaml.
func runServeZeroConfig(cmd *cobra.Command, wd string) error {
	initechDir := ".initech"

	// Where the token came from decides the Token line, which carries the
	// value only on the run that created it (ini-hl6r).
	token, source := serveToken, "from --token (not shown)"
	if token == "" {
		token, source = os.Getenv("INITECH_TOKEN"), "from INITECH_TOKEN (not shown)"
	}
	// tokenCreated reports whether the token was freshly generated on this
	// run (true) or read from .initech/token (false). The connection snippet
	// is only printed on first run to avoid noise on subsequent restarts.
	tokenCreated := false
	if token == "" {
		var err error
		token, tokenCreated, err = tui.ReadOrCreateTokenStatus(initechDir)
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
	tokenPath := filepath.Join(wd, initechDir, "token")
	switch {
	case tokenCreated:
		fmt.Fprintf(out, "Token: created and stored in %s; shown once, in the snippet below\n", tokenPath)
	case serveToken == "" && os.Getenv("INITECH_TOKEN") == "":
		fmt.Fprintf(out, "Token: stored in %s (initech serve --show-token prints it)\n", tokenPath)
	default:
		fmt.Fprintf(out, "Token: %s\n", source)
	}
	fmt.Fprintf(out, "Listening on %s\n", listen)

	if tokenCreated {
		printConnectionSnippet(out, hostname, tui.LANIPv4(), servePort, token)
	}

	return runDaemon(tui.DaemonConfig{
		Project: proj,
		Agents:  nil,
		Version: Version,
		Verbose: verbose,
	})
}

// printConnectionSnippet writes a ready-to-paste YAML block to out so the
// operator can copy it directly into their local initech.yaml. Only printed
// on first run (when the token is freshly generated).
func printConnectionSnippet(out io.Writer, hostname, ip string, port int, token string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Add this to your local initech.yaml:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "remotes:")
	fmt.Fprintf(out, "  %s:\n", hostname)
	fmt.Fprintf(out, "    addr: %s:%d\n", ip, port)
	fmt.Fprintf(out, "    token: %s\n", token)
	fmt.Fprintln(out, "    roles: []   # Add role names here")
	fmt.Fprintln(out)
}

// showServeToken prints the daemon's token and nothing else (ini-hl6r): the
// one way to see it again after the run that created it. Config mode reads
// initech.yaml's token; zero-config reads .initech/token. It never creates a
// token, and never starts the daemon.
func showServeToken(cmd *cobra.Command, cfgPath string, cfgErr error, wd string) error {
	if cfgErr == nil {
		proj, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if proj.Token == "" {
			return fmt.Errorf("no token yet: %s sets none", cfgPath)
		}
		fmt.Fprintln(cmd.OutOrStdout(), proj.Token)
		return nil
	}
	tokenPath := filepath.Join(wd, ".initech", "token")
	b, err := os.ReadFile(tokenPath)
	tok := strings.TrimSpace(string(b))
	if err != nil || tok == "" {
		return fmt.Errorf("no token yet: initech serve creates one in %s on its first run", tokenPath)
	}
	fmt.Fprintln(cmd.OutOrStdout(), tok)
	return nil
}
