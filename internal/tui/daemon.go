// daemon.go implements the headless initech daemon. It manages local agent
// panes without a TUI, listens on TCP, and streams PTY bytes to connected
// clients over yamux-multiplexed connections.
//
// Protocol:
//  1. Client connects via TCP, yamux server wraps the connection.
//  2. Client opens stream 0 (control channel), sends hello.
//  3. Server validates token, responds with hello_ok + agent list.
//  4. Server sends stream_map (yamux stream ID -> agent name).
//  5. Server opens one yamux stream per agent for bidirectional PTY bytes.
//  6. Control channel accepts JSON commands (send, peek, resize).
package tui

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// DaemonConfig holds the configuration for a headless daemon session.
type DaemonConfig struct {
	Project *config.Project
	Agents  []PaneConfig
	Version string
	Verbose bool
	// BuildAgent constructs a PaneConfig for a role named in the project
	// config — the same builder boot uses — so reload_agents can spawn roles
	// added to initech.yaml after startup WITHOUT a daemon bounce (ini-ap3i:
	// the headless daemon's only lifecycle event was boot, so every roster
	// change cost every running session on the machine). Nil in zero-config
	// mode, where there is no config to reload; the verb refuses honestly.
	BuildAgent func(role string, proj *config.Project) (PaneConfig, error)
}

// Daemon manages headless agent panes and streams them to a yamux client.
type Daemon struct {
	panes      []*Pane
	panesMu    sync.Mutex            // Protects panes/ringBufs/multiSinks for hot-add/remove via control commands.
	ringBufs   map[string]*RingBuf   // Per-pane ring buffer keyed by agent name.
	multiSinks map[string]*MultiSink // Per-pane fan-out sink keyed by agent name.
	lastSizes  map[string][2]int     // Last viewer-applied {rows, cols} per agent — survives restarts (ini-ap3i).
	buildAgent func(role string, proj *config.Project) (PaneConfig, error) // See DaemonConfig.BuildAgent.
	headless   bool // True for `initech serve` (no local renderer); resizes tell children exact truth.
	sockPath   string                // IPC socket path, for env injection into reload-spawned agents.
	ownership  *agentOwnership       // Tracks which client pushed which agent (zero-config remote).
	project    *config.Project
	listener   net.Listener
	version    string

	// yamuxCfg overrides the transport config for sessions this daemon
	// accepts. Nil means yamux.DefaultConfig() -- the WAN-sized defaults
	// every cross-machine peer keeps. Only startWindowServer sets it, so a
	// tightened keepalive is scoped to local window clients by construction
	// rather than by a conditional inside the shared path (ini-z8o).
	yamuxCfg *yamux.Config

	// console receives the human-facing connection notices ("Client
	// connected: ..."). NIL MEANS SILENT, and that is the whole point.
	//
	// `initech serve` is a headless process that owns its terminal, so it sets
	// this to os.Stdout and the notices are the UI. startWindowServer runs this
	// SAME code inside the TUI process, where tcell owns the terminal -- and
	// there, printing is display corruption, not output (ini-aqy): raw text
	// plus a newline goes in underneath tcell, scrolling the terminal that
	// tcell believes it still knows the contents of. Every later differential
	// update then paints into rows that have moved, which is the operator's
	// character-interleaved window 1.
	//
	// A writer rather than a bool so the destination is explicit at every
	// construction site: whoever builds a Daemon has to say where its console
	// output goes, and "nowhere" is a thing you can only say on purpose.
	console io.Writer

	// onFleetState applies a secondary window's set_fleet_state command on
	// window 1, the only writer (ini-9ka.10). Nil on a cross-machine daemon,
	// which has no fleet state to own -- so the command is refused there
	// rather than silently accepted.
	onFleetState func(FleetStateCmd) error

	// onGroupWindow applies a secondary window's set_group_window command on
	// window 1, the only writer of the assignment store (ini-la97). Nil on a
	// cross-machine daemon, which owns no assignment store -- so the command
	// is refused there rather than silently accepted, exactly as
	// onFleetState is.
	onGroupWindow func(GroupWindowCmd) error

	// onGroupOf applies a secondary window's set_group_of command on window 1,
	// the only writer of band membership (ini-la97). Membership is
	// fleet-scoped state that lives in layout.yaml; the routing boundary is
	// the state's scope, not its file.
	onGroupOf func(GroupOfCmd) error

	// paneOwnership returns window 1's current ownership decision, for the
	// hello handshake (ini-x5ob). Nil on a cross-machine daemon, which owns no
	// window partition -- an attaching peer there simply gets no map.
	paneOwnership func(peer string) map[string]string

	// onWindowAttached fires AFTER a secondary window's hello_ok is written,
	// on a goroutine of its own so the handshake never waits on the main loop.
	//
	// This exists because window 1 had no event for "a viewer attached" at all
	// (ini-x5ob). applyLayout -- which is what recomputes and re-serves the
	// ownership map -- is driven entirely by input: keys, the agents grid,
	// layout presets, a follower's reload. None of those fire when a window
	// dials in. So a fleet started in the ASSIGN-THEN-ATTACH order computed
	// ownership once, while no viewer was connected (everything correctly
	// folded back to window 1), served that map at the handshake, and never
	// recomputed: window 2 rendered nothing, including its own agents.
	//
	// The hello_ok map is a head start, not the guarantee. This is the
	// guarantee, and it is deliberately an EVENT rather than a poll -- a
	// per-frame republish was tried and rejected: it puts a broadcast on the
	// render path, where it can stall the loop it is trying to keep current.
	onWindowAttached func(peer string)

	// Active client sessions for graceful shutdown.
	sessionsMu sync.Mutex
	sessions   []*yamux.Session
	ctrlConns  []net.Conn // Control channels for shutdown notification.

	// Connected clients by peer name for host:agent routing and stale eviction.
	clients        map[string]net.Conn       // peer name -> control stream
	clientSessions map[string]*yamux.Session // peer name -> yamux session
	clientCtrlMu   map[string]*sync.Mutex    // per-client mutex for ctrl stream writes

	// Pending forward request responses. forwardToClient registers a channel
	// here before writing; handleControlStream delivers the response.
	fwdPendingMu sync.Mutex
	fwdPending   map[string]chan ControlResp // request ID -> response channel
	fwdSeq       atomic.Uint64
}

// ── Protocol messages ───────────────────────────────────────────────

// HelloMsg is sent by the client to initiate the handshake.
// ProtocolVersion is the window/peer control protocol version. Bump it when a
// wire form changes in a way an older peer would MISREAD rather than reject --
// including how fleet-scoped state is keyed, since that changes the protocol's
// meaning while leaving its shape intact.
//
// Not bumped by ini-yc03: that bead made in-memory and on-disk identity
// canonical, and measurement of every identity-bearing wire field (hello_ok
// agent names, set_fleet_state, set_group_of, set_group_window, the ipc pane
// list) showed all of them already canonical or carrying no agent identity. A
// bump would have fenced peers against a change that did not happen.
const ProtocolVersion = 1

type HelloMsg struct {
	Action   string `json:"action"`    // "hello"
	Version  int    `json:"version"`   // Protocol version (1).
	Token    string `json:"token"`     // Auth token.
	PeerName string `json:"peer_name"` // Client's peer name.
}

// HelloOKMsg is the server's response to a successful hello.
type HelloOKMsg struct {
	Action   string        `json:"action"`    // "hello_ok"
	Version  int           `json:"version"`   // Protocol version (1).
	PeerName string        `json:"peer_name"` // Server's peer name.
	Agents   []AgentStatus `json:"agents"`    // Current agent states.
	// Owner carries window 1's pane-ownership decision AT ATTACH (ini-x5ob).
	//
	// Serving it in the handshake, rather than leaving it to the first
	// broadcast, closes a window that would otherwise be real: a viewer
	// renders nothing until served (it derives no fallback, by design), so an
	// attach that had to wait for a round trip would plan ZERO panes for a
	// frame -- and zero planned panes means no emulator is resized, which is
	// the state that armed the ini-w6z replay crash. The guarantee ini-6m4
	// bought (a viewer plans its agents immediately on attach) is preserved by
	// making the answer arrive with the agent list, not after it.
	Owner map[string]string `json:"owner,omitempty"`
}

// AgentStatus describes an agent's state for the hello handshake.
// WaitingState is window 1's view of one agent's needs-input state as it
// crosses the wire (ini-35ak).
//
// It exists because waitingRows and shouldChime gate on the waitingPane
// interface, RemotePane could not implement it, and so every remote pane was
// skipped BEFORE scoping was consulted -- a viewer had structurally never
// chimed or listed a window-1 agent, in any release, while docs/spec.md
// canonizes that attention is never scoped.
//
// Embedded rather than nested so the JSON stays flat, and defined ONCE so the
// handshake seed and the change broadcast cannot drift into two encodings of
// one concept.
//
// SinceMillis is window 1's clock, and the viewer never recomputes it. Chime
// bookkeeping compares the instant a wait began (since.Equal(since)) to decide
// whether a wait is new, so a timestamp that moved on every broadcast would
// re-announce the same wait on every status frame.
type WaitingState struct {
	Waiting     bool   `json:"waiting,omitempty"`
	SinceMillis int64  `json:"waiting_since_ms,omitempty"`
	Preview     string `json:"waiting_preview,omitempty"`
}

// Since reconstructs the wait's start instant. Zero when not waiting, which is
// what *Pane reports too, so both kinds answer WaitingInput identically.
func (w WaitingState) Since() time.Time {
	if w.SinceMillis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(w.SinceMillis)
}

// waitingStateOf reads a pane's needs-input state for the wire. Panes that
// cannot detect a dialog report nothing, exactly as they do locally.
func waitingStateOf(p PaneView) WaitingState {
	wp, ok := p.(waitingPane)
	if !ok {
		return WaitingState{}
	}
	waiting, since, preview := wp.WaitingInput()
	if !waiting {
		return WaitingState{}
	}
	return WaitingState{Waiting: true, SinceMillis: since.UnixMilli(), Preview: preview}
}

type AgentStatus struct {
	WaitingState
	Name     string   `json:"name"`
	Alive    bool     `json:"alive"`
	Activity string   `json:"activity"`
	Bead     string   `json:"bead,omitempty"`  // PRIMARY bead only. Kept populated for wire compatibility with peers that predate Beads; new consumers must read Beads.
	Beads    []string `json:"beads,omitempty"` // ALL beads the agent holds (ini-9ka.11). Agents routinely hold several; reading Bead alone silently truncates to one, which displays as wrong-but-populated rather than empty.
	Desc     string   `json:"desc,omitempty"`  // Session description (ini-9ka.11).
}

// StreamMapMsg tells the client which yamux stream ID maps to which agent.
type StreamMapMsg struct {
	Action  string            `json:"action"`  // "stream_map"
	Streams map[uint32]string `json:"streams"` // Stream ID -> agent name.
}

// StreamAddedMsg announces a new agent stream created mid-session by a
// configure_agent push. The client opens a RemotePane bound to StreamID and
// adds it to the displayed pane list.
type StreamAddedMsg struct {
	Action   string `json:"action"`    // "stream_added"
	StreamID uint32 `json:"stream_id"` // yamux stream ID for this agent.
	Name     string `json:"name"`      // Agent name.
}

// ErrorMsg is sent on handshake failure.
type ErrorMsg struct {
	Action string `json:"action"` // "error"
	Error  string `json:"error"`
}

// ControlCmd is a command sent on the control channel after handshake.
type ControlCmd struct {
	ID     string `json:"id,omitempty"` // Request ID for response correlation.
	Action string `json:"action"`       // "send", "peek", "resize", etc.
	Target string `json:"target"`       // Agent name.
	Host   string `json:"host,omitempty"`
	Text   string `json:"text,omitempty"`
	Enter  bool   `json:"enter,omitempty"`
	Lines  int    `json:"lines,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Prune  bool   `json:"prune,omitempty"` // reload_agents: also remove self-started agents no longer in config.
}

// ControlResp is the response to a control command. It also carries unsolicited
// server-pushed commands (e.g. forward_send, stream_added) when Action is set.
type ControlResp struct {
	ID       string `json:"id,omitempty"` // Echoed from request for correlation.
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Data     string `json:"data,omitempty"`
	Action   string `json:"action,omitempty"`    // Set for unsolicited commands (e.g. "forward_send", "stream_added").
	Target   string `json:"target,omitempty"`    // Agent name for forward_send.
	Text     string `json:"text,omitempty"`      // Message text for forward_send.
	Enter    bool   `json:"enter,omitempty"`     // Append Enter for forward_send.
	StreamID uint32 `json:"stream_id,omitempty"` // yamux stream ID for stream_added.
	// Owner carries window 1's pane-ownership decision (ini-x5ob): canonical
	// agent key -> owning window id. Rides the same unsolicited-control-event
	// channel as session_notice, because it is the same kind of fact: the
	// session's shape as the AUTHORITY sees it.
	Owner map[string]string `json:"owner,omitempty"`
	// WaitingState rides agent_status (ini-35ak). Same wire and same envelope
	// as ownership, deliberately a DIFFERENT payload: Owner is the partition,
	// which is scoped state, while attention is never scoped. Carrying
	// attention on the ownership frame would fire partition updates on every
	// dialog and couple the two in the one direction the spec forbids.
	WaitingState
	Name  string   `json:"name,omitempty"`  // Agent name for stream_added / agent_status.
	Beads []string `json:"beads,omitempty"` // All beads an agent holds (ini-9ka.11 agent_status).
	Bead  string   `json:"bead,omitempty"`  // Primary bead only; wire compatibility for peers predating Beads.
}

// RunDaemon starts the headless daemon. Blocks until SIGINT/SIGTERM.
func RunDaemon(cfg DaemonConfig) error {
	logLevel := slog.LevelInfo
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}
	logCleanup := InitLogger(cfg.Project.Root, logLevel)
	defer logCleanup()

	LogInfo("daemon", "starting",
		"peer_name", cfg.Project.PeerName,
		"listen", cfg.Project.Listen,
		"agents", len(cfg.Agents),
		"version", cfg.Version)

	// Write PID file.
	pidCleanup := writePIDFile(cfg.Project.Root)
	defer pidCleanup()

	d := &Daemon{
		project:    cfg.Project,
		version:    cfg.Version,
		ringBufs:   make(map[string]*RingBuf),
		multiSinks: make(map[string]*MultiSink),
		ownership:  newAgentOwnership(),
		buildAgent: cfg.BuildAgent,
		headless:   true,
		// A headless serve owns its terminal, so the connection notices ARE
		// the UI here. The window server deliberately leaves this nil.
		console: os.Stdout,
	}

	// Start local IPC socket so agents can use 'initech send/peek'.
	sockPath := SocketPath(cfg.Project.Root, cfg.Project.Name)
	ipcCleanup, err := d.startDaemonIPC(sockPath)
	if err != nil {
		LogWarn("daemon", "IPC socket failed (agents won't have initech send)", "err", err)
		sockPath = "" // Don't inject if we couldn't bind.
	} else {
		defer ipcCleanup()
		LogInfo("daemon", "IPC socket", "path", sockPath)
	}
	d.sockPath = sockPath

	// Inject INITECH_SOCKET and INITECH_AGENT into every agent's environment.
	for i := range cfg.Agents {
		if sockPath != "" {
			cfg.Agents[i].Env = append(cfg.Agents[i].Env, "INITECH_SOCKET="+sockPath)
		}
		cfg.Agents[i].Env = append(cfg.Agents[i].Env, "INITECH_AGENT="+cfg.Agents[i].Name)
	}

	// Create and start agent panes with ring buffers and multi-sinks.
	for _, acfg := range cfg.Agents {
		p, err := NewPane(acfg, 24, 80)
		if err != nil {
			LogError("daemon", "pane creation failed", "name", acfg.Name, "err", err)
			return fmt.Errorf("create pane %q: %w", acfg.Name, err)
		}
		rb := NewRingBuf(DefaultRingBufSize)
		d.ringBufs[acfg.Name] = rb

		// MultiSink always includes the ring buffer. Client streams are
		// added/removed dynamically as clients connect/disconnect.
		ms := NewMultiSink()
		ms.Add(rb)
		d.multiSinks[acfg.Name] = ms
		p.SetNetworkSink(ms)

		p.Start()
		d.panes = append(d.panes, p)
		LogInfo("daemon", "agent started", "name", acfg.Name, "pid", p.pid)
	}
	defer func() {
		for _, p := range d.snapshotPanes() {
			p.Close()
		}
	}()

	// Bind TCP listener.
	ln, err := net.Listen("tcp", cfg.Project.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Project.Listen, err)
	}
	d.listener = ln
	defer ln.Close()
	LogInfo("daemon", "listening", "addr", ln.Addr().String())

	// Handle SIGINT/SIGTERM for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Accept connections in a goroutine.
	connCh := make(chan net.Conn, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // Listener closed.
			}
			connCh <- conn
		}
	}()

	LogInfo("daemon", "ready", "peer_name", cfg.Project.PeerName)

	// Startup banner to stdout.
	bannerPanes := d.snapshotPanes()
	agentNames := make([]string, len(bannerPanes))
	for i, p := range bannerPanes {
		agentNames[i] = p.Name()
	}
	d.consolef("initech serve %s\n", cfg.Version)
	d.consolef("  peer:    %s\n", cfg.Project.PeerName)
	d.consolef("  listen:  %s (%s)\n", cfg.Project.Listen, ln.Addr().String())
	if len(agentNames) > 0 {
		d.consolef("  agents:  %s (%d running)\n", strings.Join(agentNames, " "), len(agentNames))
	} else {
		d.consolef("  agents:  (none)\n")
	}
	if sockPath != "" {
		d.consolef("  socket:  %s\n", sockPath)
	}
	d.consolef("  pid:     %d\n", os.Getpid())

	d.consoleln("\nWaiting for connections... (Ctrl+C to stop)")

	for {
		select {
		case sig := <-sigCh:
			LogInfo("daemon", "shutdown", "signal", sig.String())
			d.consolef("[%s] Shutting down (%s)...\n",
				time.Now().Format("15:04:05"), sig)
			d.gracefulShutdown()
			d.sessionsMu.Lock()
			nClients := len(d.sessions)
			d.sessionsMu.Unlock()
			nAgents := len(d.snapshotPanes())
			d.consolef("[%s] Disconnected %d client(s), stopped %d agent(s)\n",
				time.Now().Format("15:04:05"), nClients, nAgents)
			return nil
		case conn := <-connCh:
			LogInfo("daemon", "client connected", "remote", conn.RemoteAddr().String())
			go d.handleConnection(conn)
		}
	}
}

// gracefulShutdown notifies all connected clients and closes sessions.
func (d *Daemon) gracefulShutdown() {
	d.sessionsMu.Lock()
	ctrls := d.ctrlConns
	sessions := d.sessions
	d.sessionsMu.Unlock()

	// Notify clients.
	for _, ctrl := range ctrls {
		writeJSON(ctrl, struct {
			Action string `json:"action"`
		}{"shutdown"})
	}

	// Close all yamux sessions (triggers client-side reconnect).
	for _, s := range sessions {
		s.Close()
	}
}

// startDaemonIPC starts a Unix domain socket server that accepts local IPC
// requests (send, peek, list) from agents running inside the daemon. This
// mirrors the TUI's IPC socket so that 'initech send' works for headless
// agents. Returns a cleanup function that removes the socket file.
func (d *Daemon) startDaemonIPC(socketPath string) (func(), error) {
	if _, err := os.Stat(socketPath); err == nil {
		// Check if a live daemon is already listening.
		conn, dialErr := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("daemon already running (socket %s is active). Use 'initech down' to stop it first", socketPath)
		}
		// Stale socket from a crashed instance; safe to remove.
		os.Remove(socketPath)
	}
	os.MkdirAll(filepath.Dir(socketPath), 0700)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	os.Chmod(socketPath, 0700)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.handleDaemonIPCConn(conn)
		}
	}()

	return func() { ln.Close(); os.Remove(socketPath) }, nil
}

// handleDaemonIPCConn handles a single IPC connection from a local agent.
func (d *Daemon) handleDaemonIPCConn(conn net.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := NewIPCScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeJSON(conn, IPCResponse{Error: "invalid JSON"})
		return
	}

	dispatchIPC(d, conn, req, scanner.Bytes())
}

// ── IPCHost implementation ─────────────────────────────────────────

func (d *Daemon) FindPaneView(name string) (PaneView, bool) {
	p := d.findPane(name)
	if p == nil {
		return nil, true
	}
	return p, true
}

func (d *Daemon) AllPanes() ([]PaneInfo, bool) {
	snap := d.snapshotPanes()
	panes := make([]PaneInfo, len(snap))
	for i, p := range snap {
		panes[i] = PaneInfo{
			Name:     p.Name(),
			Activity: p.Activity().String(),
			Alive:    p.IsAlive(),
			Visible:  true,
		}
	}
	return panes, true
}

func (d *Daemon) HandleSend(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	if len(req.Text) > maxSendTextLen {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("text too large (%d bytes, max %d)", len(req.Text), maxSendTextLen)})
		return
	}
	// Cross-machine routing: if Host is set and doesn't match our peer name,
	// forward via the connected client's control stream.
	if req.Host != "" && req.Host != d.project.PeerName {
		d.sessionsMu.Lock()
		clientCtrl := d.clients[req.Host]
		mu := d.clientCtrlMu[req.Host]
		d.sessionsMu.Unlock()
		if clientCtrl == nil {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("peer %q not connected. Run 'initech peers' to see available targets.", req.Host)})
			return
		}
		fwd := ControlCmd{Action: "forward_send", Target: req.Target, Text: req.Text, Enter: req.Enter}
		resp, err := d.forwardToClient(req.Host, clientCtrl, mu, fwd)
		if err != nil {
			writeIPCResponse(conn, IPCResponse{Error: err.Error()})
			return
		}
		if resp.Error != "" {
			writeIPCResponse(conn, IPCResponse{Error: resp.Error})
			return
		}
		writeIPCResponse(conn, IPCResponse{OK: true})
		return
	}
	p := d.findPane(req.Target)
	if p == nil {
		// No local pane matches. Require explicit host:agent for remote
		// delivery instead of auto-routing nondeterministically across
		// connected peers (ini-piyb.4).
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("agent %q not found. For remote agents use host:agent format (e.g. 'initech send workbench:%s ...').", req.Target, req.Target)})
		return
	}
	conn.SetReadDeadline(time.Time{})
	p.SendText(req.Text, req.Enter)
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (d *Daemon) HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool {
	return false
}

// handleConnection wraps a TCP connection in yamux, performs the hello
// handshake, and streams PTY bytes.
func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Wrap in yamux server. Config comes from this daemon INSTANCE: the
	// cross-machine daemon leaves it nil and gets the untouched defaults;
	// the in-process window server sets a tightened one (ini-z8o).
	session, err := yamux.Server(conn, d.yamuxConfig())
	if err != nil {
		LogError("daemon", "yamux server init failed", "err", err)
		return
	}
	defer session.Close()

	// Track this session for graceful shutdown.
	d.sessionsMu.Lock()
	d.sessions = append(d.sessions, session)
	d.sessionsMu.Unlock()
	defer func() {
		d.sessionsMu.Lock()
		for i, s := range d.sessions {
			if s == session {
				d.sessions = append(d.sessions[:i], d.sessions[i+1:]...)
				break
			}
		}
		d.sessionsMu.Unlock()
	}()

	// Accept the control stream (client opens stream 0 first).
	ctrl, err := session.Accept()
	if err != nil {
		LogError("daemon", "control stream accept failed", "err", err)
		return
	}
	defer ctrl.Close()

	// Track control channel for shutdown notifications.
	d.sessionsMu.Lock()
	d.ctrlConns = append(d.ctrlConns, ctrl)
	d.sessionsMu.Unlock()
	defer func() {
		d.sessionsMu.Lock()
		for i, c := range d.ctrlConns {
			if c == ctrl {
				d.ctrlConns = append(d.ctrlConns[:i], d.ctrlConns[i+1:]...)
				break
			}
		}
		d.sessionsMu.Unlock()
	}()

	// Read hello from client. Deadline prevents slow-loris attacks from
	// holding a goroutine indefinitely without completing the handshake.
	ctrl.SetReadDeadline(time.Now().Add(10 * time.Second))
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		LogWarn("daemon", "no hello received")
		return
	}
	var hello HelloMsg
	if err := json.Unmarshal(scanner.Bytes(), &hello); err != nil {
		LogWarn("daemon", "invalid hello JSON", "err", err)
		writeJSON(ctrl, ErrorMsg{Action: "error", Error: "invalid hello"})
		return
	}
	if hello.Action != "hello" {
		writeJSON(ctrl, ErrorMsg{Action: "error", Error: "expected hello"})
		return
	}

	// Validate token (constant-time comparison to prevent timing side-channel).
	if d.project.Token != "" && subtle.ConstantTimeCompare([]byte(hello.Token), []byte(d.project.Token)) != 1 {
		LogWarn("daemon", "auth failed", "peer", hello.PeerName)
		d.consolef("[%s] Client rejected: %s (auth failed)\n",
			time.Now().Format("15:04:05"), conn.RemoteAddr())
		// Delay before responding to slow down brute-force token guessing.
		time.Sleep(1 * time.Second)
		writeJSON(ctrl, ErrorMsg{Action: "error", Error: "auth failed"})
		return
	}

	// Handshake complete: clear the deadline for normal operation.
	ctrl.SetReadDeadline(time.Time{})

	// PROTOCOL VERSION GATE (ini-yc03). The version was carried and LOGGED but
	// never checked, so a mixed-version pair proceeded and diverged silently
	// -- the failure mode this fleet has now paid for four times. Fleet-scoped
	// identity is part of the protocol's meaning, not just its shape: a peer
	// that keys fleet state differently produces two truths for one fleet, and
	// the operator sees it as a display bug rather than a version mismatch.
	//
	// Refusing is the conservative direction. A refused attach is loud,
	// immediate, and names its own fix; a silent divergence is the thing this
	// whole bead sequence exists to end.
	if hello.Version != ProtocolVersion {
		LogWarn("daemon", "refusing peer with mismatched protocol version",
			"peer", hello.PeerName, "theirs", hello.Version, "ours", ProtocolVersion)
		writeJSON(ctrl, ErrorMsg{Action: "error", Error: fmt.Sprintf(
			"protocol version mismatch: this session speaks v%d, the attaching window speaks v%d. "+
				"Both windows must run the same initech version; upgrade the older one and retry.",
			ProtocolVersion, hello.Version)})
		return
	}

	LogInfo("daemon", "hello from", "peer", hello.PeerName, "version", hello.Version)
	d.consolef("[%s] Client connected: %s (peer: %s)\n",
		time.Now().Format("15:04:05"), conn.RemoteAddr(), hello.PeerName)

	// Register client for host:agent routing. If a client with the same
	// peer_name is already connected (stale session from a crashed client),
	// evict it first: close its yamux session so all its streams error out
	// and the MultiSink auto-removes them. Without this, the stale client's
	// blocked stream writes starve the new client of PTY bytes.
	if hello.PeerName != "" {
		d.sessionsMu.Lock()
		if d.clients == nil {
			d.clients = make(map[string]net.Conn)
			d.clientSessions = make(map[string]*yamux.Session)
			d.clientCtrlMu = make(map[string]*sync.Mutex)
		}
		d.evictExistingLocked(hello.PeerName, conn.RemoteAddr().String())
		d.clients[hello.PeerName] = ctrl
		d.clientSessions[hello.PeerName] = session
		d.clientCtrlMu[hello.PeerName] = &sync.Mutex{}
		d.sessionsMu.Unlock()
		defer func() {
			d.sessionsMu.Lock()
			if d.clients[hello.PeerName] == ctrl {
				delete(d.clients, hello.PeerName)
				delete(d.clientSessions, hello.PeerName)
				delete(d.clientCtrlMu, hello.PeerName)
			}
			d.sessionsMu.Unlock()
		}()
	}

	// Snapshot panes once under panesMu, then use it for the status list and
	// the per-agent stream setup below (ini-sz46: never range d.panes unlocked).
	panesSnap := d.snapshotPanes()

	// Build agent status list.
	agents := make([]AgentStatus, len(panesSnap))
	for i, p := range panesSnap {
		agents[i] = AgentStatus{
			// Seeded at the handshake so a window attaching while an agent is
			// ALREADY waiting shows it on its first frame, instead of staying
			// blind until the next transition (ini-9ka.11's precedent, and the
			// same late-attach correctness).
			WaitingState: waitingStateOf(p),
			Name:         p.Name(),
			Alive:        p.IsAlive(),
			Activity:     p.Activity().String(),
			Bead:         p.BeadID(),
			Beads:        p.BeadIDs(),
			Desc:         p.SessionDesc(),
		}
	}

	// Send hello_ok, carrying window 1's ownership decision so the attaching
	// window can plan its panes on the very first frame (ini-x5ob).
	var owner map[string]string
	if d.paneOwnership != nil {
		owner = d.paneOwnership(hello.PeerName)
	}
	if err := writeJSON(ctrl, HelloOKMsg{
		Action:   "hello_ok",
		Version:  ProtocolVersion,
		PeerName: d.project.PeerName,
		Agents:   agents,
		Owner:    owner,
	}); err != nil {
		LogWarn("daemon", "failed to send hello_ok", "err", err)
		return
	}

	// Open one yamux stream per agent and build stream map.
	// Stream IDs are assigned sequentially starting from 1.
	streamMap := make(map[uint32]string)
	type agentStream struct {
		pane   *Pane
		stream net.Conn
	}
	var streams []agentStream

	for _, p := range panesSnap {
		s, err := session.Open()
		if err != nil {
			LogError("daemon", "stream open failed", "agent", p.Name(), "err", err)
			return
		}
		ys, _ := s.(*yamux.Stream)
		streamMap[ys.StreamID()] = p.Name()
		streams = append(streams, agentStream{pane: p, stream: s})
	}

	// Send stream map.
	if err := writeJSON(ctrl, StreamMapMsg{Action: "stream_map", Streams: streamMap}); err != nil {
		LogWarn("daemon", "failed to send stream_map", "err", err)
		return
	}

	// Notify client that replay data is about to be sent on agent streams.
	if err := writeJSON(ctrl, struct {
		Action string `json:"action"`
	}{"replay_start"}); err != nil {
		LogWarn("daemon", "failed to send replay_start", "err", err)
		return
	}

	// Phase 1: replay ring buffer to each stream synchronously so the
	// client has complete screen state before replay_done fires.
	for _, as := range streams {
		d.replayToStream(as.pane, as.stream)
	}

	// Signal replay complete. All buffered history has been sent.
	if err := writeJSON(ctrl, struct {
		Action string `json:"action"`
	}{"replay_done"}); err != nil {
		LogWarn("daemon", "failed to send replay_done", "err", err)
		return
	}

	// NOW tell the TUI a window attached -- after replay_done, i.e. after the
	// whole positional preamble (hello_ok, stream_map, replay_start,
	// replay_done) is on the wire.
	//
	// This used to fire immediately after hello_ok, which put a broadcast into
	// the exact gap between hello_ok and stream_map. The client read the next
	// frame and unmarshalled it as the stream map, got an empty one, and built
	// no panes -- a blank monitor holding a correct ownership map. The client
	// side is now robust to out-of-order frames, but the ordering is fixed
	// here too: a handshake in progress is not a good moment to broadcast at
	// the window it is still setting up, whatever the receiver tolerates.
	//
	// Still off this goroutine: the handshake must never wait on the main loop.
	if d.onWindowAttached != nil && isSecondaryWindowIdentity(hello.PeerName) {
		go d.onWindowAttached(hello.PeerName)
	}

	// Phase 2: start live streaming goroutines (upstream keystrokes +
	// downstream fan-out via MultiSink).
	var wg sync.WaitGroup
	for _, as := range streams {
		wg.Add(1)
		go func(p *Pane, s net.Conn) {
			defer wg.Done()
			defer s.Close()
			d.streamAgentLive(p, s)
		}(as.pane, as.stream)
	}

	// Handle control commands until client disconnects.
	d.handleControlStream(ctrl, scanner, hello.PeerName)

	// Wait for streaming goroutines to finish.
	wg.Wait()
	LogInfo("daemon", "client disconnected")
	d.consolef("[%s] Client disconnected: %s (peer: %s)\n",
		time.Now().Format("15:04:05"), conn.RemoteAddr(), hello.PeerName)
}

// replayToStream sends buffered PTY history from the ring buffer to the
// stream so the client can reconstruct the current screen state. Called
// synchronously before replay_done is sent on the control channel.
func (d *Daemon) replayToStream(p *Pane, stream net.Conn) {
	rb := d.ringBufFor(p.Name()) // snapshot the ref under a short lock, then stream unlocked
	if rb != nil {
		if snap := rb.Snapshot(); len(snap) > 0 {
			n, err := stream.Write(snap)
			LogDebug("daemon", "replay sent", "agent", p.Name(), "bytes", n, "err", err)
		} else {
			LogDebug("daemon", "replay empty", "agent", p.Name())
		}
	} else {
		LogDebug("daemon", "replay no ringbuf", "agent", p.Name())
	}
}

// streamAgentLive wires bidirectional live PTY streaming between a pane and
// a yamux stream. Adds the stream to the pane's MultiSink for downstream
// fan-out. Reads upstream keystrokes until the client disconnects.
func (d *Daemon) streamAgentLive(p *Pane, stream net.Conn) {
	ms := d.multiSinkFor(p.Name()) // snapshot the ref under a short lock, then stream unlocked

	if ms != nil {
		ms.Add(stream)
		LogDebug("daemon", "stream added to MultiSink", "agent", p.Name(), "sink_writers", ms.Len())
		defer func() {
			ms.Remove(stream)
			LogDebug("daemon", "stream removed from MultiSink", "agent", p.Name())
		}()
	} else {
		LogWarn("daemon", "no MultiSink for agent", "agent", p.Name())
	}

	// Upstream: client keystrokes -> PTY.
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if err != nil {
			return // Client disconnected.
		}
		if p.ptmx != nil {
			p.ptmx.Write(buf[:n])
		}
	}
}

// handleControlStream reads JSON commands from the control stream and
// dispatches them to agents. peerName identifies the connected client and
// is used for ownership checks on configure_agent / stop_agent / restart_agent.
func (d *Daemon) handleControlStream(ctrl net.Conn, scanner *bufio.Scanner, peerName string) {
	// respond writes a ControlResp with the request's ID echoed back for
	// correlation. Returns false if the write fails (dead control stream).
	respond := func(id string, resp ControlResp) bool {
		resp.ID = id
		if err := writeJSON(ctrl, resp); err != nil {
			LogWarn("daemon", "control stream write failed", "err", err)
			return false
		}
		return true
	}

	for scanner.Scan() {
		line := scanner.Bytes()

		// Check if this is a response to a pending forward_send request.
		if d.deliverForwardResp(line) {
			continue
		}

		var cmd ControlCmd
		if err := json.Unmarshal(line, &cmd); err != nil {
			if !respond("", ControlResp{Error: "invalid JSON"}) {
				return
			}
			continue
		}
		// One line per command, always: tonight's restart_agent timeout was
		// diagnosed blind because neither end logged the wire (the 9gvn
		// zero-deferral-records lesson, one channel over).
		LogInfo("daemon", "control cmd", "action", cmd.Action, "peer", peerName, "id", cmd.ID)

		switch cmd.Action {
		case "send":
			if len(cmd.Text) > maxSendTextLen {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("text too large (%d bytes, max %d)", len(cmd.Text), maxSendTextLen)}) {
					return
				}
				continue
			}
			p := d.findPane(cmd.Target)
			if p == nil {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)}) {
					return
				}
				continue
			}
			p.SendText(cmd.Text, cmd.Enter)
			if !respond(cmd.ID, ControlResp{OK: true}) {
				return
			}

		case "peek":
			p := d.findPane(cmd.Target)
			if p == nil {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)}) {
					return
				}
				continue
			}
			if !respond(cmd.ID, ControlResp{OK: true, Data: peekContent(p, cmd.Lines)}) {
				return
			}

		case "resize":
			p := d.findPane(cmd.Target)
			if p == nil {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)}) {
					return
				}
				continue
			}
			if cmd.Rows > 0 && cmd.Cols > 0 {
				if d.headless {
					p.ResizeExact(cmd.Rows, cmd.Cols)
				} else {
					p.Resize(cmd.Rows, cmd.Cols)
				}
				d.panesMu.Lock()
				if d.lastSizes == nil {
					d.lastSizes = make(map[string][2]int)
				}
				d.lastSizes[cmd.Target] = [2]int{cmd.Rows, cmd.Cols}
				d.panesMu.Unlock()
			}
			if !respond(cmd.ID, ControlResp{OK: true}) {
				return
			}

		case "forward_send":
			// Received from a remote TUI: deliver to a local agent.
			if len(cmd.Text) > maxSendTextLen {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("text too large (%d bytes, max %d)", len(cmd.Text), maxSendTextLen)}) {
					return
				}
				continue
			}
			p := d.findPane(cmd.Target)
			if p == nil {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)}) {
					return
				}
				continue
			}
			p.SendText(cmd.Text, cmd.Enter)
			if !respond(cmd.ID, ControlResp{OK: true}) {
				return
			}

		case "peers_query":
			snap := d.snapshotPanes()
			agents := make([]string, len(snap))
			for i, p := range snap {
				agents[i] = p.Name()
			}
			peerName := d.project.PeerName
			peers := []PeerInfo{{Name: peerName, Agents: agents}}
			data, _ := json.Marshal(peers)
			if !respond(cmd.ID, ControlResp{OK: true, Data: string(data)}) {
				return
			}

		case "ping":
			respond(cmd.ID, ControlResp{OK: true, Data: "pong"})

		case "configure_agent":
			if !respond(cmd.ID, d.handleConfigureAgent(line, peerName)) {
				return
			}

		case "stop_agent":
			if !respond(cmd.ID, d.handleStopAgent(line, peerName)) {
				return
			}

		case "restart_agent":
			if !respond(cmd.ID, d.handleRestartAgent(line, peerName)) {
				return
			}

		case "reload_agents":
			if !respond(cmd.ID, d.handleReloadAgents(cmd.ID, peerName, cmd.Prune)) {
				return
			}

		case "set_fleet_state":
			if !respond(cmd.ID, d.handleSetFleetState(line)) {
				return
			}

		case "set_group_window":
			if !respond(cmd.ID, d.handleSetGroupWindow(line)) {
				return
			}

		case "set_group_of":
			if !respond(cmd.ID, d.handleSetGroupOf(line)) {
				return
			}

		default:
			if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("unknown action %q", cmd.Action)}) {
				return
			}
		}
	}
}

// findPane looks up a pane by name under panesMu. Zero-config remote
// (configure_agent/stop_agent) mutates d.panes at runtime via
// startPushedPane/removePane, so this MUST hold the lock — an earlier
// "never modified after startup" assumption caused a fatal concurrent
// map/slice access on reconnect (ini-sz46). No caller holds panesMu when
// calling findPane, so the lock does not nest.
func (d *Daemon) findPane(name string) *Pane {
	d.panesMu.Lock()
	defer d.panesMu.Unlock()
	for _, p := range d.panes {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// snapshotPanes returns a copy of the pane slice taken under panesMu. Callers
// range the copy without holding the lock; the Pane pointers are stable.
func (d *Daemon) snapshotPanes() []*Pane {
	d.panesMu.Lock()
	defer d.panesMu.Unlock()
	out := make([]*Pane, len(d.panes))
	copy(out, d.panes)
	return out
}

// ringBufFor returns the ring buffer for a pane (or nil) under a SHORT panesMu
// hold. The caller streams from the returned *RingBuf WITHOUT the lock, so a
// slow network write never blocks the hot-add/remove writers (ini-sz46).
func (d *Daemon) ringBufFor(name string) *RingBuf {
	d.panesMu.Lock()
	defer d.panesMu.Unlock()
	return d.ringBufs[name]
}

// multiSinkFor returns the fan-out sink for a pane (or nil) under a SHORT
// panesMu hold; the caller uses the returned *MultiSink without the lock.
func (d *Daemon) multiSinkFor(name string) *MultiSink {
	d.panesMu.Lock()
	defer d.panesMu.Unlock()
	return d.multiSinks[name]
}

// deliverForwardResp checks if a JSON line from the control stream is a
// response to a pending forward_send request. Returns true if the line was
// consumed (callers should skip further processing).
func (d *Daemon) deliverForwardResp(line []byte) bool {
	var probe struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(line, &probe) != nil || probe.ID == "" {
		return false
	}
	d.fwdPendingMu.Lock()
	ch, ok := d.fwdPending[probe.ID]
	d.fwdPendingMu.Unlock()
	if !ok {
		return false
	}
	var resp ControlResp
	json.Unmarshal(line, &resp) //nolint:errcheck
	ch <- resp
	return true
}

// forwardToClient sends a forward_send command to a client's control stream
// and waits for the client to confirm delivery. The command carries an ID;
// the client's ControlMux dispatches it to its onRequest handler, which
// delivers to the target pane and writes back a response with the same ID.
// handleControlStream reads that response and delivers it to the waiting
// channel registered here (ini-piyb.1).
func (d *Daemon) forwardToClient(peerName string, ctrl net.Conn, mu *sync.Mutex, fwd ControlCmd) (ControlResp, error) {
	id := fmt.Sprintf("fwd%d", d.fwdSeq.Add(1))
	fwd.ID = id

	ch := make(chan ControlResp, 1)
	d.fwdPendingMu.Lock()
	if d.fwdPending == nil {
		d.fwdPending = make(map[string]chan ControlResp)
	}
	d.fwdPending[id] = ch
	d.fwdPendingMu.Unlock()

	defer func() {
		d.fwdPendingMu.Lock()
		delete(d.fwdPending, id)
		d.fwdPendingMu.Unlock()
	}()

	mu.Lock()
	err := writeJSON(ctrl, fwd)
	mu.Unlock()
	if err != nil {
		return ControlResp{}, fmt.Errorf("write to peer %s: %w", peerName, err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(5 * time.Second):
		return ControlResp{}, fmt.Errorf("peer %s: forward_send timeout", peerName)
	}
}

// writeJSON encodes v as JSON and writes it as a newline-terminated line.
// Returns an error if marshaling or writing fails.
func writeJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// yamuxConfig returns the transport config for sessions this daemon accepts.
//
// A nil yamuxCfg -- every cross-machine daemon, since only startWindowServer
// sets it -- yields yamux.DefaultConfig() with no mutation, so the remote
// path's timeouts are not merely equal to the defaults but are literally the
// same call the code made before ini-z8o.
func (d *Daemon) yamuxConfig() *yamux.Config {
	if d.yamuxCfg != nil {
		return d.yamuxCfg
	}
	return yamux.DefaultConfig()
}

// handleSetFleetState applies a secondary window's global hide/protect/pin
// change on window 1 (ini-9ka.10). Ownership is not per-agent here -- fleet
// state is session-global, so any attached window may request a change; the
// authority rule is about who WRITES the file, not who may ask.
func (d *Daemon) handleSetFleetState(line []byte) ControlResp {
	var cmd FleetStateCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid set_fleet_state payload: %v", err)}
	}
	if d.onFleetState == nil {
		return ControlResp{ID: cmd.ID, Error: "this session does not own fleet state"}
	}
	if err := d.onFleetState(cmd); err != nil {
		return ControlResp{ID: cmd.ID, Error: err.Error()}
	}
	return ControlResp{ID: cmd.ID, OK: true, Action: "set_fleet_state_ok", Target: cmd.Name}
}

// handleSetGroupWindow applies a secondary window's group move on window 1
// (ini-la97). Like fleet state, the authority rule is about who WRITES the
// store, not who may ask: any attached window may request a move.
func (d *Daemon) handleSetGroupWindow(line []byte) ControlResp {
	var cmd GroupWindowCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid set_group_window payload: %v", err)}
	}
	if d.onGroupWindow == nil {
		return ControlResp{ID: cmd.ID, Error: "this session does not own window assignments"}
	}
	if err := d.onGroupWindow(cmd); err != nil {
		return ControlResp{ID: cmd.ID, Error: err.Error()}
	}
	return ControlResp{ID: cmd.ID, OK: true, Action: "set_group_window_ok", Target: cmd.Group}
}

// handleSetGroupOf applies a secondary window's band-membership change on
// window 1 (ini-la97).
func (d *Daemon) handleSetGroupOf(line []byte) ControlResp {
	var cmd GroupOfCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid set_group_of payload: %v", err)}
	}
	if d.onGroupOf == nil {
		return ControlResp{ID: cmd.ID, Error: "this session does not own band membership"}
	}
	if err := d.onGroupOf(cmd); err != nil {
		return ControlResp{ID: cmd.ID, Error: err.Error()}
	}
	return ControlResp{ID: cmd.ID, OK: true, Action: "set_group_of_ok", Target: cmd.Agent}
}

// evictExistingLocked hands the window identity to a newcomer: the OLD
// client under peerName gets the identity_taken_over verdict on its control
// stream, then its session is closed. Caller holds sessionsMu.
//
// TELLING the evicted client WHY is the whole fix (ini-jhm6): without a
// verdict, an eviction is indistinguishable from a transient drop, and the
// 1ch reconnect loop rightly treats those as retryable -- so the evicted
// client redialed, won the identity back, evicted the newcomer, forever: a
// 1-second connect/disconnect war between two undead windows. The verdict is
// what lets the loser CONCLUDE it lost.
//
// The 200ms delay before close is the verdict's flush window (measured: the
// war test flaked 3/8 without it -- yamux teardown discards stream data still
// buffered at close). Invisible to the newcomer, whose handshake proceeds
// concurrently. Delivery stays best-effort BY THE TRANSPORT'S NATURE; the
// client's quick-death inference is the backstop for the remainder.
func (d *Daemon) evictExistingLocked(peerName, newcomerAddr string) {
	oldSession, exists := d.clientSessions[peerName]
	if !exists {
		return
	}
	LogWarn("daemon", "evicting stale client", "peer", peerName)
	if oldCtrl := d.clients[peerName]; oldCtrl != nil {
		if mu := d.clientCtrlMu[peerName]; mu != nil {
			mu.Lock()
			writeJSON(oldCtrl, ControlResp{ //nolint:errcheck // best-effort goodbye on a dying conn
				Action: identityTakenOverAction,
				Text:   fmt.Sprintf("identity %q taken over by a new connection from %s", peerName, newcomerAddr),
			})
			mu.Unlock()
		}
	}
	time.AfterFunc(200*time.Millisecond, func() { oldSession.Close() })
}

// consolef writes a human-facing notice to the daemon's console, if it has
// one. A nil console is SILENCE BY DESIGN, not a missing dependency: the window
// server runs this code inside the TUI, where anything written to the terminal
// lands underneath tcell and corrupts the display (ini-aqy).
func (d *Daemon) consolef(format string, args ...any) {
	if d == nil || d.console == nil {
		return
	}
	fmt.Fprintf(d.console, format, args...)
}

// consoleln is consolef's Fprintln counterpart, with the same nil-is-silent
// contract.
func (d *Daemon) consoleln(args ...any) {
	if d == nil || d.console == nil {
		return
	}
	fmt.Fprintln(d.console, args...)
}
