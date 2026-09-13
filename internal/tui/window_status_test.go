package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

func TestWindowListener_HolderNoticeNamesProcessAndSafeRemedy(t *testing.T) {
	for _, name := range []string{"/opt/homebrew/bin/initech", "/usr/bin/python3", "not-initech"} {
		t.Run(name, func(t *testing.T) {
			u := newTestTUI()
			calls := 0
			u.inspectPortHolder = func(addr string) *PortHolder {
				calls++
				if addr != "127.0.0.1:9301" {
					t.Fatal(addr)
				}
				return &PortHolder{PID: 4321, Name: name, StartedAt: time.Now().Add(-2 * time.Hour)}
			}
			u.recordWindowBind("127.0.0.1:9301", errors.New("address already in use"))
			text := u.windowPort.Summary(time.Now())
			for _, want := range []string{name, "PID 4321", "age 2h", "address already in use", "restart this main"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			if strings.Contains(text, "kill 4321") != strings.HasSuffix(name, "/initech") {
				t.Fatalf("unsafe remedy: %s", text)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestWindowListener_StalePIDDoesNotPreventRealBind(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".initech", "initech.pid")
	if err := os.WriteFile(path, []byte("2147483647\n"), 0600); err != nil {
		t.Fatal(err)
	}
	u := newTestTUI()
	u.projectRoot = root
	u.inspectPortHolder = func(string) *PortHolder { t.Fatal("inspection on successful bind"); return nil }
	cleanup := u.startWindowListener(&config.Project{WindowListen: "127.0.0.1:0"}, "test", nil)
	defer cleanup()
	if u.windowSrv == nil || u.windowPort.State != "listening" || u.windowPort.Address != u.windowSrv.Addr() {
		t.Fatalf("bad bind result: %+v", u.windowPort)
	}
	if u.windowPort.Main != processAuthority {
		t.Fatal("main identity drift")
	}
	if !strings.Contains(u.windowPort.Summary(time.Now()), "listening on "+u.windowSrv.Addr()) {
		t.Fatal("missing actual bound address")
	}
	disabled := newTestTUI()
	disabled.startWindowListener(nil, "test", nil)()
	if disabled.WindowPortSnapshot() != nil {
		t.Fatal("single-window IPC changed")
	}
}

func TestWindowStatus_IPCListCarriesBindFailure(t *testing.T) {
	u := newTestTUI()
	u.windowPort = WindowPortStatus{State: "failed", Address: "127.0.0.1:9301", Reason: "occupied", Main: processAuthority}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan struct{})
	go func() { defer close(done); dispatchIPC(u, server, IPCRequest{Action: "list"}, nil) }()
	scanner := bufio.NewScanner(client)
	if !scanner.Scan() {
		t.Fatal("no response", scanner.Err())
	}
	var response IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.WindowPort == nil || !reflect.DeepEqual(*response.WindowPort, u.windowPort) {
		t.Fatalf("response: %+v", response)
	}
	<-done
}

func TestWindowAuthority_HandshakeFeedsViewerAndReplacesOldIdentity(t *testing.T) {
	u := newTestTUI()
	cleanup := u.startWindowListener(&config.Project{WindowListen: "127.0.0.1:0"}, "test", nil)
	defer cleanup()
	if u.windowSrv == nil {
		t.Fatal("server missing")
	}
	pc, err := connectPeer(WindowOnePeerName, config.Remote{Addr: u.windowSrv.Addr()}, &config.Project{PeerName: "window-2"})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if pc.authority == nil || *pc.authority != processAuthority {
		t.Fatalf("wrong handshake authority: %+v", pc.authority)
	}
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(140, 30)
	viewer := newTestTUI()
	viewer.screen = s
	viewer.windowID = "window-2"
	viewer.identityLineShown = true // opt-in since ini-evdn
	viewer.applyWindowAuthority(WindowOnePeerName, pc.authority)
	viewer.renderWindowConnectionStatus()
	text := readScreenRect(s, 0, 28, 140, 1)
	if !strings.Contains(text, pc.authority.StartedAt.Format(time.RFC3339)) || !strings.Contains(text, "main PID ") {
		t.Fatal(text)
	}
	next := &AuthorityIdentity{PID: 9876, StartedAt: time.Now().Add(-time.Minute).UTC()}
	viewer.applyWindowAuthority(WindowOnePeerName, next)
	viewer.renderWindowConnectionStatus()
	if !strings.Contains(readScreenRect(s, 0, 28, 140, 1), "main PID 9876") {
		t.Fatal("reconnect left old identity")
	}
	viewer.applyWindowAuthority(WindowOnePeerName, nil)
	if !strings.Contains(viewer.viewerAuthorityText(), "identity unavailable") {
		t.Fatal("old peer falsely identified")
	}
	viewer.viewerConnected = false
	if !strings.Contains(viewer.viewerAuthorityText(), "disconnected") {
		t.Fatal("offline peer displayed as connected")
	}
}

type holderRunner struct {
	calls  []string
	output map[string]string
	err    error
}

func (r *holderRunner) Run(name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return r.output[name], r.err
}
func (r *holderRunner) RunInDir(_ string, name string, args ...string) (string, error) {
	return r.Run(name, args...)
}

func TestWindowHolder_QueriesListenerInsteadOfPIDFile(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)
	r := &holderRunner{output: map[string]string{
		"lsof":           "p222\ncother\nn192.0.2.1:9301\np321\ncinitech\nn127.0.0.1:9301\n",
		"ps":             "01:02:03 /opt/homebrew/bin/initech",
		"powershell.exe": `[{"PID":222,"Name":"other","Address":"192.0.2.1","Started":"2026-09-12T12:00:00Z"},{"PID":321,"Name":"initech","Address":"127.0.0.1","Started":"2026-09-12T13:57:57Z"}]`,
	}}
	h := inspectListenerHolder(r, "127.0.0.1:9301", now)
	if h == nil || h.PID != 321 || !h.isInitech() || !h.StartedAt.Equal(now.Add(-3723*time.Second)) {
		t.Fatalf("holder=%+v", h)
	}
	if runtime.GOOS != "windows" && len(r.calls) != 2 {
		t.Fatal(r.calls)
	}
	r.err = errors.New("tool unavailable")
	if inspectListenerHolder(r, "127.0.0.1:9301", now) != nil {
		t.Fatal("inspection failure guessed holder")
	}
	r.calls = nil
	if inspectListenerHolder(r, "127.0.0.1:9301;bad", now) != nil || len(r.calls) != 0 {
		t.Fatal("invalid port executed command")
	}
	if parseLsofHolder("p123\ncinitech\nn*:9301\n", "127.0.0.1", "9301") == nil {
		t.Fatal("wildcard holder missed")
	}
	if parseWindowsHolder(`{"PID":123,"Name":"python","Address":"::","Started":"2026-09-12T12:00:00Z"}`, "::1") == nil {
		t.Fatal("Windows scalar result missed")
	}
	for _, bad := range []string{"", "oops", "-1:20"} {
		if _, ok := parseElapsed(bad); ok {
			t.Fatal(bad)
		}
	}
	if d, ok := parseElapsed("2-01:02:03"); !ok || d != 49*time.Hour+2*time.Minute+3*time.Second {
		t.Fatal(d, ok)
	}
}

func TestWindowAuthority_PeerManagerPublishesHandshakeIdentity(t *testing.T) {
	u := newTestTUI()
	defer u.startWindowListener(&config.Project{WindowListen: "127.0.0.1:0"}, "test", nil)()
	if u.windowSrv == nil {
		t.Fatal("no listener")
	}
	identities := make(chan AuthorityIdentity, 1)
	quit := make(chan struct{})
	pm := newPeerManager(&config.Project{PeerName: "window-2", Remotes: map[string]config.Remote{WindowOnePeerName: {Addr: u.windowSrv.Addr()}}}, func(string, []PaneView, bool) {}, nil, quit, func(peer string, a *AuthorityIdentity) {
		if peer == WindowOnePeerName && a != nil {
			identities <- *a
		}
	})
	defer func() { close(quit); pm.wait() }()
	select {
	case a := <-identities:
		if a != processAuthority {
			t.Fatalf("wrong authority %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manager never published real handshake identity")
	}
	// Drop this real transport without evicting the viewer identity. A fresh
	// handshake must publish authority again after the manager reconnects.
	d := u.windowSrv.daemon
	d.sessionsMu.Lock()
	for _, session := range d.sessions {
		session.Close()
	}
	d.sessionsMu.Unlock()
	select {
	case a := <-identities:
		if a != processAuthority {
			t.Fatalf("wrong reconnected authority %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect never republished authority identity")
	}
}

func TestWindowStatus_UnassignedMonitorFailureHasLowerEmphasis(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(120, 20)
	u := newTestTUI()
	u.screen = s
	u.windowPort = WindowPortStatus{State: "failed", Address: "127.0.0.1:9301", Reason: "occupied"}
	u.renderWindowConnectionStatus()
	_, _, style, _ := s.GetContent(0, 0)
	_, amber, _ := style.Decompose()
	u.assignment = &WindowAssignment{groupWindow: map[string]string{"Engineering": "window-2"}}
	u.renderWindowConnectionStatus()
	_, _, style, _ = s.GetContent(0, 0)
	_, red, _ := style.Decompose()
	if amber == red {
		t.Fatal("no emphasis change for assigned secondary window")
	}
	if !strings.Contains(readScreenRect(s, 0, 0, 120, 1), "WINDOW PORT UNAVAILABLE") {
		t.Fatal("missing persistent notice")
	}
}
