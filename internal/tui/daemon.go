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
	"sync"
	"syscall"

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
	panes    []*Pane
	project  *config.Project
	listener net.Listener
	mu       sync.Mutex
	version  string
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
	Action string `json:"action"` // "send", "peek", "resize"
	Target string `json:"target"` // Agent name.
	Text   string `json:"text,omitempty"`
	Enter  bool   `json:"enter,omitempty"`
	Lines  int    `json:"lines,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Cols   int    `json:"cols,omitempty"`
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
		project: cfg.Project,
		version: cfg.Version,
	}

	// Create and start agent panes.
	for _, acfg := range cfg.Agents {
		p, err := NewPane(acfg, 24, 80)
		if err != nil {
			LogError("daemon", "pane creation failed", "name", acfg.Name, "err", err)
			return fmt.Errorf("create pane %q: %w", acfg.Name, err)
		}
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
			return nil
		case conn := <-connCh:
			LogInfo("daemon", "client connected", "remote", conn.RemoteAddr().String())
			d.handleConnection(conn)
		}
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

	// Accept the control stream (client opens stream 0 first).
	ctrl, err := session.Accept()
	if err != nil {
		LogError("daemon", "control stream accept failed", "err", err)
		return
	}
	defer ctrl.Close()

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

	// Start bidirectional streaming goroutines.
	var wg sync.WaitGroup
	for _, as := range streams {
		wg.Add(1)
		go func(p *Pane, s net.Conn) {
			defer wg.Done()
			defer s.Close()
			d.streamAgent(p, s)
		}(as.pane, as.stream)
	}

	// Handle control commands until client disconnects.
	d.handleControlStream(ctrl, scanner)

	// Wait for streaming goroutines to finish.
	wg.Wait()
	LogInfo("daemon", "client disconnected")
}

// streamAgent copies PTY output to the yamux stream and stream input to
// the PTY. The PTY output goes to both the local emulator (for activity
// tracking) and the network stream.
func (d *Daemon) streamAgent(p *Pane, stream net.Conn) {
	// Downstream: PTY -> network stream.
	// The pane's readLoop already feeds the emulator via the internal pipe.
	// We need a separate reader from the PTY fd for the network copy.
	// Since the PTY fd is already consumed by readLoop, we read from the
	// emulator's output and forward to the stream.
	//
	// For now, use a simpler approach: the client will poll the emulator
	// state via peek commands on the control channel, and we send periodic
	// snapshots. Full PTY byte streaming requires refactoring readLoop to
	// tee output, which is Stage 3 work.
	//
	// This goroutine stays alive to keep the stream open and handle
	// upstream (client -> PTY) input.
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if err != nil {
			return // Client closed stream.
		}
		// Forward client keystrokes to the pane's PTY.
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

