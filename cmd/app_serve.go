package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
every 5 seconds), and the rest of the API in the operator guide. Its only bd
and git calls on a timer are the progress read every 5 minutes.`,
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
	appServeAddrs                  = appserve.SystemAddrs
	appServeRunner    iexec.Runner = &iexec.DefaultRunner{}
	appServeAddrEvery              = 30 * time.Second
	appServeListenOn               = appserve.Listen
	appServeExecutable             = os.Executable
	appServeLstat                  = os.Lstat
	appServeResolve                = filepath.EvalSymlinks
	appServeBinaryEvery            = appserve.BinaryEvery
	appServeDrainGrace             = appserve.DrainGrace
)

// errBinaryReplaced ends a serve whose initech an upgrade replaced: a
// planned restart, which RunMarked counts clean and the command exits 0 on,
// so its one log line is the only one.
var errBinaryReplaced = fmt.Errorf("the initech it runs was replaced: %w", appserve.ErrPlannedRestart)

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
	link := appserve.PairURL(ip.String(), appServePort, token, fp)
	fmt.Fprintln(out, link)
	code, err := appserve.RenderQR(link)
	if err != nil {
		return fmt.Errorf("draw the QR code: %w; the link above still pairs", err)
	}
	fmt.Fprint(out, "\n"+code)
	return nil
}

func appServeBindIP() (net.IP, error) {
	return appServeBindIPFrom(appServeListen, appServeAddrs)
}

// appServeBindIPFrom is the address to bind for listen over the interfaces
// addrs reads.
func appServeBindIPFrom(listen string, addrs func() ([]appserve.IfaceAddr, error)) (net.IP, error) {
	list, err := addrs()
	if err != nil {
		return nil, fmt.Errorf("read interfaces: %w", err)
	}
	return appserve.BindAddr(listen, list)
}

// appServeAPNsClient is the client pushes go out on (HTTP/2 to Apple), and
// appServeAPNsHost, when set, the host instead of Apple's: seams for tests.
var (
	appServeAPNsClient = &http.Client{}
	appServeAPNsHost   = ""
)

// appServePushes sends pushes from the feed once the first poll has read the
// rigs' settings: the apns block, then the key. With either missing or
// unusable it says so once and the service runs on without pushes.
func appServePushes(ctx context.Context, srv *appserve.Server, b *appBackend, errw io.Writer) {
	select {
	case <-ctx.Done():
		return
	case <-srv.FirstPoll():
	}
	cfg, err := b.APNs()
	if err != nil {
		fmt.Fprintf(errw, "initech app-serve: no pushes: %v\n", err)
		return
	}
	key, err := appserve.LoadAPNsKey(cfg)
	if err != nil {
		fmt.Fprintf(errw, "initech app-serve: no pushes: %v\n", err)
		return
	}
	if srv.Devices() == nil {
		fmt.Fprintln(errw, "initech app-serve: no pushes: the devices file could not be read")
		return
	}
	host := cfg.Host()
	if appServeAPNsHost != "" {
		host = appServeAPNsHost
	}
	sender := appserve.NewSender(cfg, key, host, appServeAPNsClient, srv.Devices(), time.Now, func(line string) { fmt.Fprintln(errw, line) })
	fmt.Fprintf(errw, "initech app-serve: pushes go to %s for %s\n", host, cfg.Topic)
	appserve.NewPusher(srv.Feed(), func(p appserve.Payload) { sender.Push(p, nil) }).Run(ctx)
}

// Waiting is the rig's beads in waiting_on_nelson, from one bd list in its
// root; a rig with beads off has none.
func (b *appBackend) Waiting(r appserve.Rig) ([]appserve.WaitingBead, error) {
	p, err := config.Load(filepath.Join(r.Root, "initech.yaml"))
	if err != nil {
		return nil, err
	}
	if !p.Beads.IsEnabled() {
		return nil, nil
	}
	raw, err := b.runner.RunInDir(r.Root, "bd", "list", "--status", "waiting_on_nelson", "--json")
	if err != nil {
		return nil, err
	}
	var beads []appserve.WaitingBead
	if err := json.Unmarshal([]byte(raw), &beads); err != nil {
		return nil, err
	}
	return beads, nil
}

// appServeLeavePane drops what a shell inside a pane inherits. The service
// serves every rig and speaks as the operator: INITECH_SOCKET would aim its
// reads at that pane's rig and INITECH_AGENT would sign its sends as that
// agent.
func appServeLeavePane() {
	os.Unsetenv("INITECH_SOCKET")
	os.Unsetenv("INITECH_AGENT")
}

func runAppServe(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	appServeLeavePane()
	appserve.NarrowLog(os.Stderr, cmd.ErrOrStderr())
	errw := cmd.ErrOrStderr()
	base := cmd.Context()
	if base == nil {
		base = context.Background()
	}
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ip, err := appServeBindIP()
	if err != nil {
		return err
	}
	dirs, err := appServeRigFolders()
	if err != nil {
		return err
	}
	state := appServeStateDir()
	cert, _, err := appserve.EnsureCert(state)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	if _, err := appserve.LoadDigest(state); err != nil {
		fmt.Fprintf(errw, "initech app-serve: %v; every request is refused until then\n", err)
	}
	backend := &appBackend{dirs: dirs, runner: appServeRunner, report: func(dir, why string) {
		appserve.ReportFolder(state, appServeNotify, errw, dir, why)
	}}
	srv := appserve.New(appserve.Config{
		Backend:  backend,
		TokenDir: state,
		StateDir: state,
		OnStuck:  func(notice string) { fmt.Fprintln(errw, notice) },
	})
	ln, err := appServeListenOn(ip, appServePort)
	if err != nil {
		return err
	}
	fmt.Fprintf(errw, "initech app-serve listening on %s (%s), rigs under %v\n", ln.Addr(), appServeListen, dirs)
	go srv.Run(ctx)
	go srv.RunProgress(ctx)
	go srv.RunTail(ctx)
	go srv.RunWaiting(ctx)
	go appServePushes(ctx, srv, backend, errw)

	// The bound address going away (DHCP, Tailscale restarting) ends the
	// run, so launchd starts it again on the new one.
	// The goroutines below take the seams they use as values here, so a test
	// that restores them after the service returns does not race a watcher
	// that has not yet seen ctx end.
	addrEvery, addrs, listen := appServeAddrEvery, appServeAddrs, appServeListen
	binEvery, resolve, grace := appServeBinaryEvery, appServeResolve, appServeDrainGrace
	moved := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				ln.Close()
				return
			case <-time.After(addrEvery):
			}
			if cur, err := appServeBindIPFrom(listen, addrs); err != nil || !cur.Equal(ip) {
				close(moved)
				ln.Close()
				return
			}
		}
	}()
	// An upgrade moves the link launchd ran to a new build: the service
	// notices within appServeBinaryEvery, says so once, lets requests in
	// flight finish and exits, and KeepAlive starts the new build.
	replaced := make(chan struct{})
	if exe, err := appServeExecutable(); err != nil {
		fmt.Fprintf(errw, "initech app-serve: its own path is unknown (%v), so an upgrade does not restart the service\n", err)
	} else if start, ok := appserve.ArmBinaryWatch(exe, appServeLstat, resolve, errw); ok {
		go func() {
			tick := time.NewTicker(binEvery)
			defer tick.Stop()
			if to := appserve.BinaryReplaced(ctx, exe, start, resolve, tick.C); to != "" {
				fmt.Fprintln(errw, appserve.ReplacedLine(start, to))
				close(replaced)
			}
		}()
	}
	err = appserve.RunMarked(state, appServeNotify, errw, func() error {
		err := appserve.ServeTLSUntil(ln, srv.Handler(), cert, replaced, grace)
		select {
		case <-replaced:
			return errBinaryReplaced
		default:
		}
		select {
		case <-moved:
			return fmt.Errorf("the address %s went away; restarting on the new one: %w", ip, appserve.ErrPlannedRestart)
		default:
		}
		if ctx.Err() != nil {
			return nil
		}
		return err
	})
	if errors.Is(err, errBinaryReplaced) {
		return nil
	}
	return err
}

// appServeRigFolders is the folders to scan for rigs: --projects, else the
// folder holding the rig found from the current directory.
func appServeRigFolders() ([]string, error) {
	if len(appServeProjects) > 0 {
		return appServeProjects, nil
	}
	if p := discoverProjectBestEffort(); p != nil {
		return []string{filepath.Dir(p.Root)}, nil
	}
	return nil, fmt.Errorf("no rig folder: run from inside a rig or pass --projects <folder>")
}

// appBackend is the app service's Backend over the code initech status,
// peek and send run.
type appBackend struct {
	dirs   []string
	runner iexec.Runner
	report func(dir, why string) // told once when a rig folder does not answer or is refused

	scanOnce sync.Once
	folders  *appserve.FolderScan

	apnsMu sync.Mutex
	apns   map[string]appserve.APNsConfig // by initech.yaml path

	refusedMu sync.Mutex
	refused   map[string]bool // folders reported refused, until they read again
}

// FindRigs is the rigs in the scan folders, a folder that does not answer in
// time left out (appserve.FolderScan).
func (b *appBackend) FindRigs() []appserve.Rig {
	return b.scan().Scan(b.dirs)
}

// scan is the backend's one FolderScan, kept across scans so a held folder
// stays one read and one report.
func (b *appBackend) scan() *appserve.FolderScan {
	b.scanOnce.Do(func() {
		b.folders = &appserve.FolderScan{Read: b.readRigFolder, OnHeld: func(dir string) {
			if b.report != nil {
				b.report(dir, "it has not answered")
			}
		}}
	})
	return b.folders
}

// apnsBlock is the part of an initech.yaml the pushes read.
type apnsBlock struct {
	APNs *appserve.APNsConfig `yaml:"apns"`
}

// readAPNs records the apns block of the initech.yaml at path, if it has one.
func (b *appBackend) readAPNs(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var blk apnsBlock
	if yaml.Unmarshal(raw, &blk) != nil || blk.APNs == nil {
		return
	}
	b.apnsMu.Lock()
	defer b.apnsMu.Unlock()
	if b.apns == nil {
		b.apns = map[string]appserve.APNsConfig{}
	}
	b.apns[path] = *blk.APNs
}

// APNs is the push settings: the apns block of whichever rig's initech.yaml
// the scans found holding one. None is an error, and so are two different
// ones, which would make the push depend on scan order.
func (b *appBackend) APNs() (appserve.APNsConfig, error) {
	b.apnsMu.Lock()
	defer b.apnsMu.Unlock()
	var paths []string
	for p := range b.apns {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return appserve.APNsConfig{}, errors.New("no apns block in any rig's initech.yaml")
	}
	first := b.apns[paths[0]]
	for _, p := range paths[1:] {
		if b.apns[p] != first {
			return appserve.APNsConfig{}, fmt.Errorf("%s and %s hold different apns blocks", paths[0], p)
		}
	}
	return first, nil
}

// readRigFolder is every <dir>/*/initech.yaml that loads. Glob swallows I/O
// errors, so the folder is read first: one macOS refuses, or that is not
// there, would otherwise read as a folder with no rigs and nothing said.
func (b *appBackend) readRigFolder(dir string) []appserve.Rig {
	_, err := os.ReadDir(dir)
	b.refusedMu.Lock()
	first := err != nil && !b.refused[dir]
	if err != nil {
		if b.refused == nil {
			b.refused = map[string]bool{}
		}
		b.refused[dir] = true
	} else {
		delete(b.refused, dir)
	}
	b.refusedMu.Unlock()
	if first && b.report != nil {
		b.report(dir, err.Error())
	}
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "initech.yaml"))
	sort.Strings(matches)
	var rigs []appserve.Rig
	for _, m := range matches {
		b.readAPNs(m)
		p, err := config.Load(m)
		if err != nil || p.Root == "" {
			continue
		}
		rigs = append(rigs, appserve.Rig{Name: p.Name, Root: p.Root, Socket: tui.SocketPath(p.Root, p.Name)})
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
		// A TUI older than session_file names none: without this, every
		// pane of a rig not yet restarted reads as having no transcript and
		// its super as having nothing waiting.
		if ps.SessionFile == "" && pi.Host == "" {
			ps.SessionFile = tui.NewestTranscript(filepath.Join(r.Root, pi.Name))
		}
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

// appServeProgressHours is the window of the service's progress reports,
// initech progress's default.
const appServeProgressHours = 6

// ActiveEfforts is the effort prefixes of the rig's beads past open and not
// closed, from one bd list; a rig with beads off has none.
func (b *appBackend) ActiveEfforts(r appserve.Rig) ([]string, error) {
	p, err := config.Load(filepath.Join(r.Root, "initech.yaml"))
	if err != nil {
		return nil, err
	}
	if !p.Beads.IsEnabled() {
		return nil, nil
	}
	raw, err := b.runner.RunInDir(r.Root, "bd", "list", "--status", strings.Join(shownStatuses, ","), "--json")
	if err != nil {
		return nil, err
	}
	var beads []progress.Bead
	if err := json.Unmarshal([]byte(raw), &beads); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, bd := range beads {
		prefix, _ := progress.Effort(bd.ID)
		if !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	return out, nil
}

// Progress is initech progress --json for the rig and prefix.
func (b *appBackend) Progress(r appserve.Rig, prefix string) (progress.Report, error) {
	p, err := config.Load(filepath.Join(r.Root, "initech.yaml"))
	if err != nil {
		return progress.Report{}, err
	}
	return progressReport(b.runner, p, prefix, appServeProgressHours, progressNow().UTC())
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
