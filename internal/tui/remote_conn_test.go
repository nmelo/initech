package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

type remotePeerServerConfig struct {
	firstResponse any
	streamMap     any
	agentNames    []string
}

type remotePeerServer struct {
	addr    string
	helloCh chan HelloMsg
	errCh   chan error

	listener net.Listener
	closeCh  chan struct{}
	done     chan struct{}

	mu      sync.Mutex
	session *yamux.Session
	streams []net.Conn
}

func startRemotePeerServer(t *testing.T, cfg remotePeerServerConfig) *remotePeerServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &remotePeerServer{
		addr:     ln.Addr().String(),
		helloCh:  make(chan HelloMsg, 1),
		errCh:    make(chan error, 1),
		listener: ln,
		closeCh:  make(chan struct{}),
		done:     make(chan struct{}),
	}

	go srv.serve(cfg)
	t.Cleanup(func() { srv.Close() })

	return srv
}

func (srv *remotePeerServer) serve(cfg remotePeerServerConfig) {
	defer close(srv.done)

	conn, err := srv.listener.Accept()
	if err != nil {
		select {
		case <-srv.closeCh:
			return
		default:
			srv.reportErr(err)
			return
		}
	}
	defer conn.Close()

	session, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		srv.reportErr(err)
		return
	}
	srv.setSession(session)
	defer session.Close()

	ctrl, err := session.Accept()
	if err != nil {
		srv.reportErr(err)
		return
	}
	defer ctrl.Close()

	scanner := bufio.NewScanner(ctrl)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			srv.reportErr(err)
		}
		return
	}

	var hello HelloMsg
	if err := json.Unmarshal(scanner.Bytes(), &hello); err != nil {
		srv.reportErr(err)
		return
	}
	srv.helloCh <- hello

	if err := writeServerMessage(ctrl, cfg.firstResponse); err != nil {
		srv.reportErr(err)
		return
	}

	if cfg.streamMap == nil && len(cfg.agentNames) == 0 {
		<-srv.closeCh
		return
	}

	streams := make(map[uint32]string, len(cfg.agentNames))
	for _, agentName := range cfg.agentNames {
		stream, err := session.Open()
		if err != nil {
			srv.reportErr(err)
			return
		}
		srv.addStream(stream)
		yStream, ok := stream.(*yamux.Stream)
		if !ok {
			srv.reportErr(errors.New("opened stream was not a yamux stream"))
			return
		}
		streams[yStream.StreamID()] = agentName
	}

	streamMap := cfg.streamMap
	if streamMap == nil {
		streamMap = StreamMapMsg{Action: "stream_map", Streams: streams}
	}
	if err := writeServerMessage(ctrl, streamMap); err != nil {
		srv.reportErr(err)
		return
	}

	<-srv.closeCh
}

func writeServerMessage(conn net.Conn, msg any) error {
	switch v := msg.(type) {
	case string:
		_, err := conn.Write([]byte(v))
		return err
	default:
		return writeJSON(conn, msg)
	}
}

func (srv *remotePeerServer) setSession(session *yamux.Session) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	srv.session = session
}

func (srv *remotePeerServer) addStream(stream net.Conn) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	srv.streams = append(srv.streams, stream)
}

func (srv *remotePeerServer) reportErr(err error) {
	select {
	case srv.errCh <- err:
	default:
	}
}

func (srv *remotePeerServer) waitHello(t *testing.T) HelloMsg {
	t.Helper()
	select {
	case hello := <-srv.helloCh:
		return hello
	case err := <-srv.errCh:
		t.Fatalf("server error before hello: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for hello message")
	}
	return HelloMsg{}
}

func (srv *remotePeerServer) Close() {
	select {
	case <-srv.closeCh:
	default:
		close(srv.closeCh)
	}
	_ = srv.listener.Close()

	srv.mu.Lock()
	for _, stream := range srv.streams {
		_ = stream.Close()
	}
	if srv.session != nil {
		_ = srv.session.Close()
	}
	srv.mu.Unlock()

	select {
	case <-srv.done:
	case <-time.After(2 * time.Second):
	}
}

func TestConnectPeer_UsesProjectTokenAndBuildsRemotePanes(t *testing.T) {
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: 1, PeerName: "workbench"},
		agentNames:    []string{"eng1", "eng2"},
	})

	project := &config.Project{
		PeerName: "local-peer",
		Token:    "project-token",
	}
	remote := config.Remote{Addr: srv.addr}

	pc, err := connectPeer("workbench", remote, project)
	if err != nil {
		t.Fatalf("connectPeer: %v", err)
	}
	defer func() {
		for _, pane := range pc.panes {
			if rp, ok := pane.(*RemotePane); ok {
				rp.Close()
			}
		}
		pc.Close()
	}()

	hello := srv.waitHello(t)
	if hello.Action != "hello" {
		t.Fatalf("hello action = %q, want hello", hello.Action)
	}
	if hello.Token != "project-token" {
		t.Fatalf("hello token = %q, want project-token", hello.Token)
	}
	if hello.PeerName != "local-peer" {
		t.Fatalf("hello peer_name = %q, want local-peer", hello.PeerName)
	}

	if len(pc.panes) != 2 {
		t.Fatalf("len(pc.panes) = %d, want 2", len(pc.panes))
	}

	gotNames := make(map[string]bool, len(pc.panes))
	for _, pane := range pc.panes {
		gotNames[pane.Name()] = true
		if pane.Host() != "workbench" {
			t.Fatalf("pane host = %q, want workbench", pane.Host())
		}
		if !pane.IsAlive() {
			t.Fatalf("pane %q should be alive", pane.Name())
		}
	}
	for _, want := range []string{"eng1", "eng2"} {
		if !gotNames[want] {
			t.Fatalf("missing pane %q in %+v", want, gotNames)
		}
	}

	pc.Close()
	select {
	case <-pc.mux.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("control mux did not close after peerConn.Close")
	}
}

func TestConnectPeer_UsesRemoteTokenOverride(t *testing.T) {
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: 1, PeerName: "workbench"},
		streamMap:     StreamMapMsg{Action: "stream_map", Streams: map[uint32]string{}},
	})

	project := &config.Project{
		PeerName: "local-peer",
		Token:    "project-token",
	}
	remote := config.Remote{
		Addr:  srv.addr,
		Token: "remote-token",
	}

	pc, err := connectPeer("workbench", remote, project)
	if err != nil {
		t.Fatalf("connectPeer: %v", err)
	}
	defer pc.Close()

	hello := srv.waitHello(t)
	if hello.Token != "remote-token" {
		t.Fatalf("hello token = %q, want remote-token", hello.Token)
	}
	if len(pc.panes) != 0 {
		t.Fatalf("len(pc.panes) = %d, want 0", len(pc.panes))
	}
}

func TestConnectPeer_ServerRejected(t *testing.T) {
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: ErrorMsg{Action: "error", Error: "bad token"},
	})

	project := &config.Project{PeerName: "local-peer", Token: "project-token"}
	remote := config.Remote{Addr: srv.addr}

	_, err := connectPeer("workbench", remote, project)
	if err == nil {
		t.Fatal("connectPeer should fail on server rejection")
	}
	if !strings.Contains(err.Error(), "server rejected: bad token") {
		t.Fatalf("connectPeer error = %v, want server rejection", err)
	}

	hello := srv.waitHello(t)
	if hello.Token != "project-token" {
		t.Fatalf("hello token = %q, want project-token", hello.Token)
	}
}

func TestConnectPeer_InvalidHelloOK(t *testing.T) {
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: "{invalid-json}\n",
	})

	project := &config.Project{PeerName: "local-peer", Token: "project-token"}
	remote := config.Remote{Addr: srv.addr}

	_, err := connectPeer("workbench", remote, project)
	if err == nil {
		t.Fatal("connectPeer should fail on invalid hello_ok")
	}
	if !strings.Contains(err.Error(), "invalid hello_ok") {
		t.Fatalf("connectPeer error = %v, want invalid hello_ok", err)
	}
}

func TestConnectPeer_PeerNameMismatch(t *testing.T) {
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: 1, PeerName: "other-peer"},
	})

	project := &config.Project{PeerName: "local-peer", Token: "project-token"}
	remote := config.Remote{Addr: srv.addr}

	_, err := connectPeer("workbench", remote, project)
	if err == nil {
		t.Fatal("connectPeer should fail on peer_name mismatch")
	}
	if !strings.Contains(err.Error(), `peer_name mismatch: expected "workbench", got "other-peer"`) {
		t.Fatalf("connectPeer error = %v, want peer_name mismatch", err)
	}
}

func TestConnectPeer_InvalidStreamMap(t *testing.T) {
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: 1, PeerName: "workbench"},
		streamMap:     "{invalid-json}\n",
	})

	project := &config.Project{PeerName: "local-peer", Token: "project-token"}
	remote := config.Remote{Addr: srv.addr}

	_, err := connectPeer("workbench", remote, project)
	if err == nil {
		t.Fatal("connectPeer should fail on invalid stream_map")
	}
	if !strings.Contains(err.Error(), "invalid stream_map") {
		t.Fatalf("connectPeer error = %v, want invalid stream_map", err)
	}
}

func TestPeerConnClose_NilSafe(t *testing.T) {
	var pc peerConn
	pc.Close()
}
