// Package cmd implements the initech CLI commands using Cobra.
// Each subcommand lives in its own file. Root handles global flags and version.
package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tui"
	"github.com/nmelo/initech/internal/update"
	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

var (
	resetLayout bool
	verbose     bool
	noColor     bool
	autoSuspend bool
	pprofAddr   string
	webPort     int

	// updateResult receives the background version check result.
	// Populated in PersistentPreRun, drained in PersistentPostRun.
	updateResult chan *update.ReleaseInfo
	updateCancel context.CancelFunc
)

var (
	executeRoot = func() error { return rootCmd.Execute() }
	exitRoot    = os.Exit
	tuiRun      = tui.Run
	listenTCP   = net.Listen
	serveHTTP   = http.Serve
)

var rootCmd = &cobra.Command{
	Use:   "initech",
	Short: "Bootstrap and manage multi-agent development projects. Have you seen my stapler?",
	Long: `Initech launches a TUI terminal multiplexer for managing multi-agent
development sessions. Each agent gets its own PTY-backed terminal pane
running Claude with the appropriate permission level.

Running initech with no subcommand launches the TUI.
Requires initech.yaml in the current directory or a parent directory.

Keybindings:
  ` + "`" + `                Open command modal
  Alt+Left/Right   Navigate between panes
  Alt+1            Focus mode (single pane)
  Alt+2            2x2 grid
  Alt+3            3x3 grid
  Alt+4            Main + stacked layout
  Alt+z            Zoom/unzoom focused pane
  Alt+s            Toggle agent status overlay
  Alt+q            Quit

Commands (via ` + "`" + ` modal):
  grid CxR         Set grid layout (e.g. grid 3x3)
  focus [name]     Focus mode, optionally on a named agent
  zoom             Toggle zoom
  panel            Toggle agent overlay
  main             Main + stacked layout
  layout reset     Reset layout to auto-calculated defaults
  quit             Exit`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if noColor {
			color.SetEnabled(false)
		}
		// Launch background version check (non-blocking, result drained in PostRun).
		if update.ShouldCheck() && Version != "dev" {
			ctx, cancel := context.WithCancel(context.Background())
			updateCancel = cancel
			ch := make(chan *update.ReleaseInfo, 1)
			updateResult = ch
			go func() {
				info, _ := update.CheckForUpdate(ctx, Version)
				ch <- info
			}()
		}
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		// Cancel the background check if it's still running.
		if updateCancel != nil {
			updateCancel()
		}
		// Drain the result (non-blocking).
		if updateResult != nil {
			select {
			case info := <-updateResult:
				if info != nil {
					LatestRelease = info
				}
			default:
			}
		}

		// Show update notification on stderr for CLI commands.
		// Skip for: TUI (has its own notification), serve, version.
		skip := map[string]bool{"initech": true, "serve": true, "version": true}
		if LatestRelease != nil && !skip[cmd.Name()] {
			if !update.ShouldSuppressNotification(LatestRelease.PublishedAt) {
				fmt.Fprintf(os.Stderr, "\nA new version of initech is available: v%s -> v%s\n  Update: %s\n\n",
					Version, LatestRelease.Version, update.UpdateInstruction())
			}
		}
		return nil
	},
	RunE: runTUI,
}

// LatestRelease holds the result of the background version check.
// Populated by PersistentPostRun, consumed by notification surfaces.
var LatestRelease *update.ReleaseInfo

// Execute runs the root command.
func Execute() {
	if err := executeRoot(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitRoot(1)
	}
}

func init() {
	rootCmd.Flags().BoolVar(&resetLayout, "reset-layout", false, "Ignore saved layout and start with auto-calculated defaults")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable DEBUG-level logging to .initech/initech.log")
	rootCmd.Flags().BoolVar(&autoSuspend, "auto-suspend", false, "Enable automatic agent suspension under memory pressure")
	rootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "Start pprof HTTP server on the given localhost address (e.g. localhost:6060)")
	rootCmd.Flags().IntVar(&webPort, "web-port", 0, "Start web companion server on the given port (0 = disabled)")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")

	// Register color functions as template functions so the usage template can
	// apply color to evaluated template expressions like .Name.
	cobra.AddTemplateFunc("cyanBold", color.CyanBold)
	cobra.AddTemplateFunc("blue", color.Blue)

	rootCmd.SetUsageTemplate(colorizedUsageTemplate)
	rootCmd.AddCommand(versionCmd)
}

// colorizedUsageTemplate is a Cobra usage template that applies color to
// section headers (cyan+bold) and subcommand names (blue). It is a modified
// version of Cobra's built-in default template.
const colorizedUsageTemplate = `{{cyanBold "Usage:"}}
{{- if .Runnable}}
  {{.UseLine}}
{{end}}
{{- if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]
{{end}}
{{- if gt (len .Aliases) 0}}

{{cyanBold "Aliases:"}}
  {{.NameAndAliases}}
{{end}}
{{- if .HasExample}}

{{cyanBold "Examples:"}}
{{.Example}}
{{end}}
{{- if .HasAvailableSubCommands}}

{{cyanBold "Available Commands:"}}
{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}  {{.Name | printf "%-15s" | blue}}{{.Short}}
{{end}}{{end}}
{{- end}}
{{- if .HasAvailableLocalFlags}}

{{cyanBold "Flags:"}}
{{.LocalFlags.FlagUsages | trimRightSpace}}
{{end}}
{{- if .HasAvailableInheritedFlags}}

{{cyanBold "Global Flags:"}}
{{.InheritedFlags.FlagUsages | trimRightSpace}}
{{end}}
{{- if .HasHelpSubCommands}}

{{cyanBold "Additional help topics:"}}
{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}  {{.CommandPath | rpad .CommandPath .CommandPathPadding}} {{.Short}}
{{end}}{{end}}
{{end}}
{{- if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.
{{end}}`

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the initech version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("initech %s\n", Version)
		return nil
	},
}

func runTUI(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}

	proj, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	agents := make([]tui.PaneConfig, 0, len(proj.Roles))
	for _, roleName := range proj.Roles {
		pcfg, err := buildAgentPaneConfig(roleName, proj)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Warning:", err)
			continue
		}
		agents = append(agents, pcfg)
	}

	if len(agents) == 0 {
		return fmt.Errorf("no valid role directories found. Run 'initech init' to create them")
	}

	// Bridge the background update check into the TUI's update notification.
	var tuiUpdateCh chan string
	if updateResult != nil {
		tuiUpdateCh = make(chan string, 1)
		go func() {
			if info := <-updateResult; info != nil {
				LatestRelease = info
				if !update.ShouldSuppressNotification(info.PublishedAt) {
					tuiUpdateCh <- info.Version
				}
			}
			close(tuiUpdateCh)
		}()
	}

	// Resolve auto-suspend: CLI flag overrides config. If the flag was
	// explicitly set on the command line, it wins. Otherwise, fall back to
	// the config file value.
	enableAutoSuspend := proj.Resource.AutoSuspend
	if cmd.Flags().Changed("auto-suspend") {
		enableAutoSuspend = autoSuspend
	}

	// Start pprof HTTP server when --pprof is set. Restricted to localhost
	// to prevent exposing goroutine dumps (which may contain in-memory tokens)
	// to the network.
	if pprofAddr != "" {
		host, _, err := net.SplitHostPort(pprofAddr)
		if err != nil {
			return fmt.Errorf("pprof: invalid address %q: %w", pprofAddr, err)
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "" {
			return fmt.Errorf("pprof: refusing to bind to non-localhost address %q (security risk)", pprofAddr)
		}
		ln, err := listenTCP("tcp", pprofAddr)
		if err != nil {
			return fmt.Errorf("pprof listen on %s: %w", pprofAddr, err)
		}
		fmt.Fprintf(os.Stderr, "pprof server listening on http://%s/debug/pprof\n", ln.Addr())
		go func() { _ = serveHTTP(ln, nil) }()
	}

	return tuiRun(tui.Config{
		Agents:            agents,
		ProjectName:       proj.Name,
		ProjectRoot:       proj.Root,
		ResetLayout:       resetLayout,
		Verbose:           verbose,
		Version:           Version,
		AutoSuspend:       enableAutoSuspend,
		PressureThreshold: proj.Resource.PressureThreshold,
		Project:           proj,
		UpdateResult:      tuiUpdateCh,
		PaneConfigBuilder: buildReloadingPaneConfigBuilder(cfgPath, buildAgentPaneConfig),
		WebPort:           webPort,
	})
}

// buildAgentPaneConfig constructs a PaneConfig for the given role from the
// project config. Returns an error if the workspace directory does not exist.
// INITECH_SOCKET and INITECH_AGENT are NOT set here; the TUI injects them
// at pane-creation time so they reflect the live socket path.
func buildAgentPaneConfig(roleName string, proj *config.Project) (tui.PaneConfig, error) {
	ov, hasOverride := proj.RoleOverrides[roleName]

	var argv []string
	if mock := os.Getenv("INITECH_MOCK_AGENT"); mock != "" {
		argv = []string{mock}
	} else {
		// Per-role command override takes priority (e.g. ["codex"] for non-Claude agents).
		// When Command is set, it is the complete command; claude_args are NOT appended.
		if hasOverride && len(ov.Command) > 0 {
			argv = append(argv, ov.Command...)
		} else {
			if len(proj.ClaudeCommand) > 0 {
				argv = append(argv, proj.ClaudeCommand...)
			} else {
				argv = []string{"claude"}
			}
			var roleArgs []string
			if hasOverride {
				roleArgs = ov.ClaudeArgs
			}
			if resolved := roles.ResolveClaudeArgs(roleName, proj.ClaudeArgs, roleArgs); len(resolved) > 0 {
				argv = append(argv, resolved...)
			}
		}
	}

	dir := filepath.Join(proj.Root, roleName)
	if hasOverride && ov.Dir != "" {
		dir = ov.Dir
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return tui.PaneConfig{}, fmt.Errorf("role %q directory does not exist: %s", roleName, dir)
	}

	var env []string

	agentType, autoApprove, noBracketedPaste, submitKey := resolvePaneBehavior(ov)

	return tui.PaneConfig{
		Name:             roleName,
		Command:          argv,
		Dir:              dir,
		Env:              env,
		AgentType:        agentType,
		AutoApprove:      autoApprove,
		NoBracketedPaste: noBracketedPaste,
		SubmitKey:        submitKey,
	}, nil
}
