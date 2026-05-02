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
	"context"
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
	"github.com/nmelo/initech/internal/web"
)

// DaemonConfig holds the configuration for a headless daemon session.
type DaemonConfig struct {
	Project *config.Project
	Agents  []PaneConfig
	Version string
	Verbose bool
	WebPort int // Web companion port. 0 = disabled.
}

// Daemon manages headless agent panes and streams them to a yamux client.
type Daemon struct {
	panes      []*Pane
	panesMu    sync.Mutex            // Protects panes/ringBufs/multiSinks for hot-add/remove via control commands.
	ringBufs   map[string]*RingBuf   // Per-pane ring buffer keyed by agent name.
	multiSinks map[string]*MultiSink // Per-pane fan-out sink keyed by agent name.
	ownership  *agentOwnership       // Tracks which client pushed which agent (zero-config remote).
	project    *config.Project
	listener   net.Listener
	version    string
	timers     *TimerStore

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
}

// AgentStatus describes an agent's state for the hello handshake.
type AgentStatus struct {
	Name     string `json:"name"`
	Alive    bool   `json:"alive"`
	Activity string `json:"activity"`
	Bead     string `json:"bead,omitempty"`
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
	Action string `json:"action"`       // "send", "peek", "resize", "schedule", etc.
	Target string `json:"target"`       // Agent name.
	Host   string `json:"host,omitempty"`
	Text   string `json:"text,omitempty"`
	Enter  bool   `json:"enter,omitempty"`
	Lines  int    `json:"lines,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	FireAt string `json:"fire_at,omitempty"` // RFC3339 for schedule command.
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
	Name     string `json:"name,omitempty"`      // Agent name for stream_added.
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
		timers:     NewTimerStore(filepath.Join(cfg.Project.Root, ".initech", "timers.json")),
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
		for _, p := range d.panes {
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
	agentNames := make([]string, len(d.panes))
	for i, p := range d.panes {
		agentNames[i] = p.Name()
	}
	fmt.Fprintf(os.Stdout, "initech serve %s\n", cfg.Version)
	fmt.Fprintf(os.Stdout, "  peer:    %s\n", cfg.Project.PeerName)
	fmt.Fprintf(os.Stdout, "  listen:  %s (%s)\n", cfg.Project.Listen, ln.Addr().String())
	if len(agentNames) > 0 {
		fmt.Fprintf(os.Stdout, "  agents:  %s (%d running)\n", strings.Join(agentNames, " "), len(agentNames))
	} else {
		fmt.Fprintf(os.Stdout, "  agents:  (none)\n")
	}
	if sockPath != "" {
		fmt.Fprintf(os.Stdout, "  socket:  %s\n", sockPath)
	}
	fmt.Fprintf(os.Stdout, "  pid:     %d\n", os.Getpid())

	// Start web companion server when configured.
	if cfg.WebPort > 0 {
		webCtx, webCancel := context.WithCancel(context.Background())
		lister := &daemonPaneLister{d: d}
		subscriber := &daemonPaneSubscriber{d: d}
		stateProvider := &daemonStateProvider{d: d}
		paneWriter := &daemonPaneWriter{d: d}
		webSrv := web.NewServer(cfg.WebPort, lister, subscriber, stateProvider, nil, paneWriter, nil, nil)
		go func() {
			if err := webSrv.Start(webCtx); err != nil {
				LogError("daemon-web", "server exited with error", "err", err)
			}
		}()
		LogInfo("daemon-web", "companion server starting", "port", cfg.WebPort)
		fmt.Fprintf(os.Stdout, "  web:     http://0.0.0.0:%d\n", cfg.WebPort)
		defer func() {
			webCancel()
			shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
			webSrv.Shutdown(shutCtx)
			shutCancel()
		}()
	}

	fmt.Fprintln(os.Stdout, "\nWaiting for connections... (Ctrl+C to stop)")

	// Fire any overdue timers from a previous session.
	d.fireTimers()

	// 1-second ticker for timer execution.
	timerTicker := time.NewTicker(1 * time.Second)
	defer timerTicker.Stop()

	for {
		select {
		case sig := <-sigCh:
			LogInfo("daemon", "shutdown", "signal", sig.String())
			fmt.Fprintf(os.Stdout, "[%s] Shutting down (%s)...\n",
				time.Now().Format("15:04:05"), sig)
			d.gracefulShutdown()
			d.sessionsMu.Lock()
			nClients := len(d.sessions)
			d.sessionsMu.Unlock()
			fmt.Fprintf(os.Stdout, "[%s] Disconnected %d client(s), stopped %d agent(s)\n",
				time.Now().Format("15:04:05"), nClients, len(d.panes))
			return nil
		case conn := <-connCh:
			LogInfo("daemon", "client connected", "remote", conn.RemoteAddr().String())
			go d.handleConnection(conn)
		case <-timerTicker.C:
			d.fireTimers()
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
	panes := make([]PaneInfo, len(d.panes))
	for i, p := range d.panes {
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

func (d *Daemon) Timers() *TimerStore {
	return d.timers
}

func (d *Daemon) NotifyConfig() (webhookURL, project string) {
	if d.project != nil {
		return d.project.WebhookURL, d.project.Name
	}
	return "", ""
}

func (d *Daemon) HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool {
	return false
}

// handleConnection wraps a TCP connection in yamux, performs the hello
// handshake, and streams PTY bytes.
func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Wrap in yamux server.
	session, err := yamux.Server(conn, yamux.DefaultConfig())
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
		fmt.Fprintf(os.Stdout, "[%s] Client rejected: %s (auth failed)\n",
			time.Now().Format("15:04:05"), conn.RemoteAddr())
		// Delay before responding to slow down brute-force token guessing.
		time.Sleep(1 * time.Second)
		writeJSON(ctrl, ErrorMsg{Action: "error", Error: "auth failed"})
		return
	}

	// Handshake complete: clear the deadline for normal operation.
	ctrl.SetReadDeadline(time.Time{})

	LogInfo("daemon", "hello from", "peer", hello.PeerName, "version", hello.Version)
	fmt.Fprintf(os.Stdout, "[%s] Client connected: %s (peer: %s)\n",
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
		if oldSession, exists := d.clientSessions[hello.PeerName]; exists {
			LogWarn("daemon", "evicting stale client", "peer", hello.PeerName)
			// Close the yamux session: all its streams error immediately,
			// MultiSink auto-removes dead writers, and the old handleConnection
			// goroutine unwinds. This prevents blocked writes from starving
			// the new client of PTY bytes.
			go oldSession.Close()
		}
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

	// Build agent status list.
	agents := make([]AgentStatus, len(d.panes))
	for i, p := range d.panes {
		agents[i] = AgentStatus{
			Name:     p.Name(),
			Alive:    p.IsAlive(),
			Activity: p.Activity().String(),
			Bead:     p.BeadID(),
		}
	}

	// Send hello_ok.
	if err := writeJSON(ctrl, HelloOKMsg{
		Action:   "hello_ok",
		Version:  1,
		PeerName: d.project.PeerName,
		Agents:   agents,
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

	for _, p := range d.panes {
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
	fmt.Fprintf(os.Stdout, "[%s] Client disconnected: %s (peer: %s)\n",
		time.Now().Format("15:04:05"), conn.RemoteAddr(), hello.PeerName)
}

// replayToStream sends buffered PTY history from the ring buffer to the
// stream so the client can reconstruct the current screen state. Called
// synchronously before replay_done is sent on the control channel.
func (d *Daemon) replayToStream(p *Pane, stream net.Conn) {
	rb := d.ringBufs[p.Name()]
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
	ms := d.multiSinks[p.Name()]

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
				p.Resize(cmd.Rows, cmd.Cols)
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
			agents := make([]string, len(d.panes))
			for i, p := range d.panes {
				agents[i] = p.Name()
			}
			peerName := d.project.PeerName
			peers := []PeerInfo{{Name: peerName, Agents: agents}}
			data, _ := json.Marshal(peers)
			if !respond(cmd.ID, ControlResp{OK: true, Data: string(data)}) {
				return
			}

		case "schedule":
			if d.timers == nil {
				if !respond(cmd.ID, ControlResp{Error: "timer store not initialized"}) {
					return
				}
				continue
			}
			fireAt, err := time.Parse(time.RFC3339, cmd.FireAt)
			if err != nil {
				if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("invalid fire_at: %v", err)}) {
					return
				}
				continue
			}
			timer, addErr := d.timers.Add(cmd.Target, cmd.Host, cmd.Text, cmd.Enter, fireAt)
			if addErr != nil {
				if !respond(cmd.ID, ControlResp{Error: addErr.Error()}) {
					return
				}
				continue
			}
			if !respond(cmd.ID, ControlResp{OK: true, Data: timer.ID}) {
				return
			}

		case "list_timers":
			if d.timers == nil {
				if !respond(cmd.ID, ControlResp{OK: true, Data: "[]"}) {
					return
				}
				continue
			}
			timers := d.timers.List()
			tdata, _ := json.Marshal(timers)
			if !respond(cmd.ID, ControlResp{OK: true, Data: string(tdata)}) {
				return
			}

		case "cancel_timer":
			if d.timers == nil {
				if !respond(cmd.ID, ControlResp{Error: "timer store not initialized"}) {
					return
				}
				continue
			}
			timer, err := d.timers.Cancel(cmd.Text)
			if err != nil {
				if !respond(cmd.ID, ControlResp{Error: err.Error()}) {
					return
				}
				continue
			}
			if !respond(cmd.ID, ControlResp{OK: true, Data: timer.ID}) {
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

		default:
			if !respond(cmd.ID, ControlResp{Error: fmt.Sprintf("unknown action %q", cmd.Action)}) {
				return
			}
		}
	}
}

// findPane looks up a pane by name. Safe to call without locking because
// d.panes is populated during RunDaemon startup and never modified afterward.
// If hot-add/remove agents is implemented for the daemon, this must be
// synchronized (e.g., protected by d.mu or dispatched via a main goroutine).
func (d *Daemon) findPane(name string) *Pane {
	for _, p := range d.panes {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// fireTimers checks for due timers and delivers them to local agents.
func (d *Daemon) fireTimers() {
	if d.timers == nil {
		return
	}
	due, err := d.timers.FireDue(time.Now())
	if err != nil {
		LogWarn("timer", "persistence error after firing timers",
			"err", err, "count", len(due))
	}
	for _, timer := range due {
		d.fireScheduledSend(timer)
	}
}

func (d *Daemon) fireScheduledSend(timer Timer) {
	delay := time.Since(timer.FireAt)
	if delay > time.Second {
		LogInfo("timer", "firing overdue",
			"id", timer.ID, "target", timer.Target,
			"scheduled", timer.FireAt.Format(time.RFC3339),
			"delay", delay.Truncate(time.Second).String())
	} else {
		LogInfo("timer", "firing", "id", timer.ID, "target", timer.Target)
	}

	p := d.findPane(timer.Target)
	if p == nil {
		LogWarn("timer", "agent not found, message not delivered",
			"id", timer.ID, "target", timer.Target)
		return
	}
	if !p.IsAlive() {
		LogWarn("timer", "agent is dead, message not delivered",
			"id", timer.ID, "target", timer.Target)
		return
	}
	p.SendText(timer.Text, timer.Enter)
	LogInfo("timer", "delivered", "id", timer.ID, "target", timer.Target)
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
