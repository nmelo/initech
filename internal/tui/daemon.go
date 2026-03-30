// daemon.go implements the headless initech daemon. It manages local agent
// panes without a TUI, listens on TCP, and streams PTY bytes to connected
// clients over yamux-multiplexed connections.
//
// Protocol:
//   1. Client connects via TCP, yamux server wraps the connection.
//   2. Client opens stream 0 (control channel), sends hello.
//   3. Server validates token, responds with hello_ok + agent list.
//   4. Server sends stream_map (yamux stream ID -> agent name).
//   5. Server opens one yamux stream per agent for bidirectional PTY bytes.
//   6. Control channel accepts JSON commands (send, peek, resize).
package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// DaemonConfig holds the configuration for a headless daemon session.
type DaemonConfig struct {
	Project  *config.Project
	Agents   []PaneConfig
	Version  string
	Verbose  bool
}

// Daemon manages headless agent panes and streams them to a yamux client.
type Daemon struct {
	panes      []*Pane
	ringBufs   map[string]*RingBuf   // Per-pane ring buffer keyed by agent name.
	multiSinks map[string]*MultiSink // Per-pane fan-out sink keyed by agent name.
	project    *config.Project
	listener   net.Listener
	mu         sync.Mutex
	version    string
	timers     *TimerStore

	// Active client sessions for graceful shutdown.
	sessionsMu sync.Mutex
	sessions   []*yamux.Session
	ctrlConns  []net.Conn // Control channels for shutdown notification.
}

// ── Protocol messages ───────────────────────────────────────────────

// HelloMsg is sent by the client to initiate the handshake.
type HelloMsg struct {
	Action   string `json:"action"`   // "hello"
	Version  int    `json:"version"`  // Protocol version (1).
	Token    string `json:"token"`    // Auth token.
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
	Action  string         `json:"action"`  // "stream_map"
	Streams map[uint32]string `json:"streams"` // Stream ID -> agent name.
}

// ErrorMsg is sent on handshake failure.
type ErrorMsg struct {
	Action string `json:"action"` // "error"
	Error  string `json:"error"`
}

// ControlCmd is a command sent on the control channel after handshake.
type ControlCmd struct {
	Action string `json:"action"` // "send", "peek", "resize", "schedule", etc.
	Target string `json:"target"` // Agent name.
	Host   string `json:"host,omitempty"`
	Text   string `json:"text,omitempty"`
	Enter  bool   `json:"enter,omitempty"`
	Lines  int    `json:"lines,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	FireAt string `json:"fire_at,omitempty"` // RFC3339 for schedule command.
}

// ControlResp is the response to a control command.
type ControlResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  string `json:"data,omitempty"`
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
	connCh := make(chan net.Conn, 1)
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

	for {
		select {
		case sig := <-sigCh:
			LogInfo("daemon", "shutdown", "signal", sig.String())
			d.gracefulShutdown()
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
		writeJSON(ctrl, struct{ Action string `json:"action"` }{"shutdown"})
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
		os.Remove(socketPath)
	}
	os.MkdirAll(filepath.Dir(socketPath), 0700)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}

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

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeJSON(conn, IPCResponse{Error: "invalid JSON"})
		return
	}

	switch req.Action {
	case "send":
		if req.Target == "" {
			writeJSON(conn, IPCResponse{Error: "target is required"})
			return
		}
		p := d.findPane(req.Target)
		if p == nil {
			writeJSON(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
			return
		}
		conn.SetReadDeadline(time.Time{})
		p.SendText(req.Text, req.Enter)
		writeJSON(conn, IPCResponse{OK: true})

	case "peek":
		if req.Target == "" {
			writeJSON(conn, IPCResponse{Error: "target is required"})
			return
		}
		p := d.findPane(req.Target)
		if p == nil {
			writeJSON(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
			return
		}
		writeJSON(conn, IPCResponse{OK: true, Data: peekContent(p, req.Lines)})

	case "list":
		type paneInfo struct {
			Name     string `json:"name"`
			Activity string `json:"activity"`
			Alive    bool   `json:"alive"`
			Visible  bool   `json:"visible"`
		}
		panes := make([]paneInfo, len(d.panes))
		for i, p := range d.panes {
			panes[i] = paneInfo{
				Name:     p.Name(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  true,
			}
		}
		data, _ := json.Marshal(panes)
		writeJSON(conn, IPCResponse{OK: true, Data: string(data)})

	default:
		writeJSON(conn, IPCResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
	}
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

	// Read hello from client.
	scanner := bufio.NewScanner(ctrl)
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

	// Validate token.
	if d.project.Token != "" && hello.Token != d.project.Token {
		LogWarn("daemon", "auth failed", "peer", hello.PeerName)
		writeJSON(ctrl, ErrorMsg{Action: "error", Error: "auth failed"})
		return
	}

	LogInfo("daemon", "hello from", "peer", hello.PeerName, "version", hello.Version)

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
	writeJSON(ctrl, HelloOKMsg{
		Action:   "hello_ok",
		Version:  1,
		PeerName: d.project.PeerName,
		Agents:   agents,
	})

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
	writeJSON(ctrl, StreamMapMsg{Action: "stream_map", Streams: streamMap})

	// Notify client that replay data is about to be sent on agent streams.
	writeJSON(ctrl, struct{ Action string `json:"action"` }{"replay_start"})

	// Start bidirectional streaming goroutines. streamAgent replays the
	// ring buffer snapshot before switching to live bytes.
	var wg sync.WaitGroup
	for _, as := range streams {
		wg.Add(1)
		go func(p *Pane, s net.Conn) {
			defer wg.Done()
			defer s.Close()
			d.streamAgent(p, s)
		}(as.pane, as.stream)
	}

	// Signal that replay is complete and live streaming has begun.
	writeJSON(ctrl, struct{ Action string `json:"action"` }{"replay_done"})

	// Handle control commands until client disconnects.
	d.handleControlStream(ctrl, scanner)

	// Wait for streaming goroutines to finish.
	wg.Wait()
	LogInfo("daemon", "client disconnected")
}

// streamAgent wires bidirectional PTY streaming between a pane and a yamux
// stream. On connect: replays the ring buffer snapshot, then adds the stream
// to the pane's MultiSink for live fan-out. On disconnect: removes the stream.
// Upstream (client -> PTY) is a read loop forwarding keystrokes.
func (d *Daemon) streamAgent(p *Pane, stream net.Conn) {
	rb := d.ringBufs[p.Name()]
	ms := d.multiSinks[p.Name()]

	// Replay: send buffered PTY history so the client reconstructs the
	// current screen state before live bytes start flowing.
	if rb != nil {
		if snap := rb.Snapshot(); len(snap) > 0 {
			stream.Write(snap)
		}
	}

	// Add this client's stream to the fan-out sink. readLoop writes to
	// the MultiSink which delivers to all clients + ring buffer.
	if ms != nil {
		ms.Add(stream)
		defer ms.Remove(stream)
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
// dispatches them to agents.
func (d *Daemon) handleControlStream(ctrl net.Conn, scanner *bufio.Scanner) {
	for scanner.Scan() {
		var cmd ControlCmd
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			writeJSON(ctrl, ControlResp{Error: "invalid JSON"})
			continue
		}

		switch cmd.Action {
		case "send":
			p := d.findPane(cmd.Target)
			if p == nil {
				writeJSON(ctrl, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)})
				continue
			}
			p.SendText(cmd.Text, cmd.Enter)
			writeJSON(ctrl, ControlResp{OK: true})

		case "peek":
			p := d.findPane(cmd.Target)
			if p == nil {
				writeJSON(ctrl, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)})
				continue
			}
			writeJSON(ctrl, ControlResp{OK: true, Data: peekContent(p, cmd.Lines)})

		case "resize":
			p := d.findPane(cmd.Target)
			if p == nil {
				writeJSON(ctrl, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)})
				continue
			}
			if cmd.Rows > 0 && cmd.Cols > 0 {
				p.Resize(cmd.Rows, cmd.Cols)
			}
			writeJSON(ctrl, ControlResp{OK: true})

		case "forward_send":
			// Received from a remote TUI: deliver to a local agent.
			p := d.findPane(cmd.Target)
			if p == nil {
				writeJSON(ctrl, ControlResp{Error: fmt.Sprintf("agent %q not found", cmd.Target)})
				continue
			}
			p.SendText(cmd.Text, cmd.Enter)
			writeJSON(ctrl, ControlResp{OK: true})

		case "peers_query":
			agents := make([]string, len(d.panes))
			for i, p := range d.panes {
				agents[i] = p.Name()
			}
			peerName := d.project.PeerName
			peers := []PeerInfo{{Name: peerName, Agents: agents}}
			data, _ := json.Marshal(peers)
			writeJSON(ctrl, ControlResp{OK: true, Data: string(data)})

		case "schedule":
			if d.timers == nil {
				writeJSON(ctrl, ControlResp{Error: "timer store not initialized"})
				continue
			}
			fireAt, err := time.Parse(time.RFC3339, cmd.FireAt)
			if err != nil {
				writeJSON(ctrl, ControlResp{Error: fmt.Sprintf("invalid fire_at: %v", err)})
				continue
			}
			timer := d.timers.Add(cmd.Target, cmd.Host, cmd.Text, cmd.Enter, fireAt)
			writeJSON(ctrl, ControlResp{OK: true, Data: timer.ID})

		case "list_timers":
			if d.timers == nil {
				writeJSON(ctrl, ControlResp{OK: true, Data: "[]"})
				continue
			}
			timers := d.timers.List()
			tdata, _ := json.Marshal(timers)
			writeJSON(ctrl, ControlResp{OK: true, Data: string(tdata)})

		case "cancel_timer":
			if d.timers == nil {
				writeJSON(ctrl, ControlResp{Error: "timer store not initialized"})
				continue
			}
			timer, err := d.timers.Cancel(cmd.Text)
			if err != nil {
				writeJSON(ctrl, ControlResp{Error: err.Error()})
				continue
			}
			writeJSON(ctrl, ControlResp{OK: true, Data: timer.ID})

		default:
			writeJSON(ctrl, ControlResp{Error: fmt.Sprintf("unknown action %q", cmd.Action)})
		}
	}
}

// findPane looks up a pane by name.
func (d *Daemon) findPane(name string) *Pane {
	for _, p := range d.panes {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// writeJSON encodes v as JSON and writes it as a newline-terminated line.
func writeJSON(w io.Writer, v any) {
	data, _ := json.Marshal(v)
	w.Write(data)
	w.Write([]byte("\n"))
}

