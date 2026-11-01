package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var (
	appServeListen   string
	appServePort     int
	appServeProjects []string
)

var appServeCmd = &cobra.Command{
	Use:   "app-serve",
	Short: "Serve the initech app (iPhone and iPad) over the tailnet",
	Long: `initech app-serve is the Mac-side service the initech app talks to. It
listens on one address only: by default this Mac's Tailscale address, and
with --listen local-network the LAN address, for the build phase while
Tailscale is stopped. It never binds 0.0.0.0 or loopback.

It serves HTTPS only (TLS 1.3) with its own self-signed certificate, which
the app pins by the fingerprint in the pairing link. Every request carries
the device token (Authorization: Bearer <token>); a missing or wrong token
is refused with 401 and no body. Issue the token with initech app-serve
token, which prints it once.

It finds rigs by scanning each --projects folder for <folder>/*/initech.yaml
and serves, for every rig whose initech answers: GET /v1/rigs and
GET /v1/status (what initech status --json prints, with each pane's last
screen lines while it shows a dialog, a banner or a prompt; read at most once
every 5 seconds). No bd or git call runs on a timer.`,
	Args: cobra.NoArgs,
	RunE: runAppServe,
}

var appServeTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Issue the app's device token and print it once, as the pairing link",
	Long: `initech app-serve token makes a new device token, stores only its
SHA-256 (~/.config/initech/app-serve/token.sha256, mode 0600) and prints the
pairing link once:

  initech://pair?host=<address>&port=<port>&token=<token>&fp=<pin>

fp is the SHA-256 of the service's certificate (made on the first run,
~/.config/initech/app-serve/cert.pem, key 0600, kept across re-pairing),
which the app pins. The address is the one initech app-serve binds with the same --listen and
--port. A new token replaces the old one, so the device paired before is
refused from then on. The token is never printed again; run this again to
pair a device.`,
	Args: cobra.NoArgs,
	RunE: runAppServeToken,
}

func init() {
	for _, c := range []*cobra.Command{appServeCmd, appServeTokenCmd} {
		c.Flags().StringVar(&appServeListen, "listen", appserve.ListenTailnet, "where to listen: tailnet (the default and the release setting) or local-network (the LAN, while Tailscale is stopped)")
		c.Flags().IntVar(&appServePort, "port", appserve.DefaultPort, "TCP port")
	}
	appServeCmd.Flags().StringSliceVar(&appServeProjects, "projects", nil, "a folder whose subfolders are rigs (repeatable); default: the folder holding the rig found from the current directory")
	appServeCmd.AddCommand(appServeTokenCmd)
	rootCmd.AddCommand(appServeCmd)
}

// Seams for tests.
var (
	appServeStateDir = func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "initech", "app-serve")
	}
	appServeAddrs               = appserve.SystemAddrs
	appServeRunner iexec.Runner = &iexec.DefaultRunner{}
)

func runAppServeToken(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	ip, err := appServeBindIP()
	if err != nil {
		return err
	}
	_, fp, err := appserve.EnsureCert(appServeStateDir())
	if err != nil {
		return fmt.Errorf("make certificate: %w", err)
	}
	token, err := appserve.IssueToken(appServeStateDir())
	if err != nil {
		return fmt.Errorf("issue token: %w", err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Pair the app with this link (shown once; a new token revokes the previous device):")
	fmt.Fprintln(out, appserve.PairURL(ip.String(), appServePort, token, fp))
	return nil
}

func appServeBindIP() (net.IP, error) {
	addrs, err := appServeAddrs()
	if err != nil {
		return nil, fmt.Errorf("read interfaces: %w", err)
	}
	return appserve.BindAddr(appServeListen, addrs)
}

func runAppServe(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	ip, err := appServeBindIP()
	if err != nil {
		return err
	}
	digest, err := appserve.LoadDigest(appServeStateDir())
	if err != nil {
		return err
	}
	cert, _, err := appserve.LoadCert(appServeStateDir())
	if err != nil {
		return err
	}
	dirs := appServeProjects
	if len(dirs) == 0 {
		if p := discoverProjectBestEffort(); p != nil {
			dirs = []string{filepath.Dir(p.Root)}
		}
	}
	if len(dirs) == 0 {
		return fmt.Errorf("no rig folder: run from inside a rig or pass --projects <folder>")
	}
	srv := appserve.New(appserve.Config{
		Backend:  &appBackend{dirs: dirs, runner: appServeRunner},
		Digest:   digest,
		HasToken: true,
		StateDir: appServeStateDir(),
	})
	ln, err := appserve.Listen(ip, appServePort)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "initech app-serve listening on %s (%s), rigs under %v\n", ln.Addr(), appServeListen, dirs)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	go srv.Run(ctx)
	return appserve.ServeTLS(ln, srv.Handler(), cert)
}

// appBackend is the app service's Backend over the code initech status,
// peek and send run.
type appBackend struct {
	dirs   []string
	runner iexec.Runner
}

// FindRigs is every <dir>/*/initech.yaml that loads, one rig per root.
func (b *appBackend) FindRigs() []appserve.Rig {
	seen := map[string]bool{}
	var rigs []appserve.Rig
	for _, dir := range b.dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "initech.yaml"))
		sort.Strings(matches)
		for _, m := range matches {
			p, err := config.Load(m)
			if err != nil || p.Root == "" || seen[p.Root] {
				continue
			}
			seen[p.Root] = true
			rigs = append(rigs, appserve.Rig{Name: p.Name, Root: p.Root, Socket: tui.SocketPath(p.Root, p.Name)})
		}
	}
	return rigs
}

// Panes is the TUI's pane list with screen detail: state and banner, as
// status --json reads them, and no bd.
func (b *appBackend) Panes(r appserve.Rig) ([]appserve.PaneState, error) {
	if _, err := os.Stat(r.Socket); err != nil {
		return nil, err
	}
	resp, err := ipcCallSocket(r.Socket, tui.IPCRequest{Action: "list", Detail: true})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var panes []paneInfo
	if err := json.Unmarshal([]byte(resp.Data), &panes); err != nil {
		return nil, err
	}
	out := make([]appserve.PaneState, 0, len(panes))
	for _, pi := range panes {
		ps := appserve.PaneState{Name: pi.Name, Banner: pi.Banner, SessionFile: pi.SessionFile}
		if s := agentState(pi); s != nil {
			ps.State = *s
		}
		out = append(out, ps)
	}
	return out, nil
}

// Status is initech status --json for the rig, bd read in its root.
func (b *appBackend) Status(r appserve.Rig) ([]map[string]any, error) {
	p, err := config.Load(filepath.Join(r.Root, "initech.yaml"))
	if err != nil {
		return nil, err
	}
	st, err := collectStatus(b.runner, r.Socket, p, r.Root, true)
	if err != nil {
		return nil, err
	}
	return agentsAsMaps(statusAgents(st))
}

// agentsAsMaps is the agents as the JSON objects status --json prints.
func agentsAsMaps(agents []agentJSON) ([]map[string]any, error) {
	raw, err := json.Marshal(agents)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}

// Screen is the pane's last lines, as initech peek reads them.
func (b *appBackend) Screen(r appserve.Rig, pane string, lines int) ([]string, error) {
	resp, err := ipcCallSocket(r.Socket, tui.IPCRequest{Action: "peek", Target: pane, Lines: lines})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return appserve.SplitScreen(resp.Data), nil
}
