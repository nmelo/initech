package tui

import (
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// ini-a2nb: a connect with a roles list stops on the peer only what this
// initech pushed there, and shows only what the list names.

const sweepSelf = "nmbp"

func sweepCatCommand() []string { return []string{"/bin/sh", "-c", "cat"} }

func sweepPosix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the agents are a POSIX shell")
	}
}

// sweepPush starts an agent on the real serve as pushed by owner.
func sweepPush(t *testing.T, d *Daemon, name, owner string) {
	t.Helper()
	line, _ := json.Marshal(ConfigureAgentCmd{Action: "configure_agent", Name: name, Command: sweepCatCommand(),
		Dir: filepath.Join(d.project.Root, name)})
	if resp := d.handleConfigureAgent(line, owner); !resp.OK {
		t.Fatalf("push %s: %+v", name, resp)
	}
	t.Cleanup(func() { d.removePane(name) })
}

func sweepPID(p *Pane) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func sweepBuilder(t *testing.T) {
	t.Helper()
	old := defaultConfigureAgentBuilder
	t.Cleanup(func() { defaultConfigureAgentBuilder = old })
	defaultConfigureAgentBuilder = func(string, *config.Project, config.Remote) (ConfigureAgentCmd, error) {
		return ConfigureAgentCmd{Command: sweepCatCommand(), Dir: t.TempDir()}, nil
	}
}

// sweepView is what this initech shows from the peer.
type sweepView struct {
	mu    sync.Mutex
	panes map[string]bool
	conn  bool
	sig   chan struct{} // Signalled on every change; await waits on it, never on a clock.
}

func (v *sweepView) signal() {
	select {
	case v.sig <- struct{}{}:
	default:
	}
}

// await returns once cond holds, re-reading it at each change this initech
// is told of. No deadline: the test binary's -timeout ends a hang.
func (v *sweepView) await(cond func() bool) {
	for !cond() {
		<-v.sig
	}
}

func (v *sweepView) shows(name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.panes[name]
}

func (v *sweepView) connected() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conn
}

func (v *sweepView) changed(_ string, panes []PaneView, connected bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.panes = map[string]bool{}
	for _, p := range panes {
		v.panes[p.Name()] = true
	}
	v.conn = connected
	v.signal()
}

func (v *sweepView) added(_ string, p PaneView) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.panes == nil {
		v.panes = map[string]bool{}
	}
	v.panes[p.Name()] = true
	v.signal()
}

func (v *sweepView) names() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []string
	for n := range v.panes {
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// sweepLogs is the log, signalled on every line, so a test waits for a line
// as an event.
type sweepLogs struct {
	mu  sync.Mutex
	b   strings.Builder
	sig chan struct{}
}

func (l *sweepLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.b.Write(p)
	l.mu.Unlock()
	select {
	case l.sig <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (l *sweepLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// await returns once the log holds want. No deadline, as sweepView.await.
func (l *sweepLogs) await(want string) {
	for !strings.Contains(l.String(), want) {
		<-l.sig
	}
}

func sweepCaptureLogs(t *testing.T) *sweepLogs {
	t.Helper()
	l := &sweepLogs{sig: make(chan struct{}, 1)}
	appLogger.mu.Lock()
	prev := appLogger.logger
	appLogger.logger = slog.New(slog.NewTextHandler(l, &slog.HandlerOptions{Level: slog.LevelDebug}))
	appLogger.mu.Unlock()
	t.Cleanup(func() {
		appLogger.mu.Lock()
		appLogger.logger = prev
		appLogger.mu.Unlock()
	})
	return l
}

func sweepManager(t *testing.T, project *config.Project, v *sweepView) *peerManager {
	t.Helper()
	quit := make(chan struct{})
	pm := &peerManager{project: project, onPanesChanged: v.changed, onPaneAdded: v.added, quit: quit}
	t.Cleanup(func() {
		close(quit)
		pm.wait()
	})
	return pm
}

// sweepYAML is an initech.yaml naming the serve as a remote with roles.
func sweepYAML(addr, roles string) string {
	return "project: test\nroot: /tmp/rig\npeer_name: " + sweepSelf + "\ntoken: fake-a2nb-token\nroles:\n    - super\n" +
		"remotes:\n    testhost:\n        addr: " + addr + "\n        roles:\n            - " + roles + "\n"
}

// TestSweep_AConnectStopsOnlyWhatThisInitechPushed is AC1, AC2 and AC3 with a
// real serve and real processes: red at base, which stops eng9 and eng8.
func TestSweep_AConnectStopsOnlyWhatThisInitechPushed(t *testing.T) {
	sweepPosix(t)
	for _, path := range []string{"yaml at start", "live yaml edit", "initech remote add's edit"} {
		t.Run(path, func(t *testing.T) {
			sweepBuilder(t)
			logs := sweepCaptureLogs(t)
			td := startTestDaemon(t, "fake-a2nb-token", "eng9") // The serve's own.
			d := td.daemon
			sweepPush(t, d, "eng8", "another-rig")
			sweepPush(t, d, "old", sweepSelf) // Ours, and no longer listed.
			eng9, eng8 := d.findPane("eng9"), d.findPane("eng8")
			pid9, pid8 := sweepPID(eng9), sweepPID(eng8)

			v := &sweepView{sig: make(chan struct{}, 1)}
			remote := config.Remote{Addr: td.addr, Roles: []string{"eng2"}}
			project := &config.Project{Name: "test", PeerName: sweepSelf, Token: "fake-a2nb-token", Root: t.TempDir()}
			pm := sweepManager(t, project, v)
			switch path {
			case "yaml at start":
				project.Remotes = map[string]config.Remote{"testhost": remote}
				pm.runsMu.Lock()
				pm.startPeerLocked("testhost", effectiveRemote(remote, project), project)
				pm.runsMu.Unlock()
			case "live yaml edit", "initech remote add's edit":
				file := filepath.Join(t.TempDir(), "initech.yaml")
				if path == "live yaml edit" {
					os.WriteFile(file, []byte(sweepYAML(td.addr, "eng2")), 0o600)
				} else {
					os.WriteFile(file, []byte("project: test\nroot: /tmp/rig\npeer_name: "+sweepSelf+"\ntoken: fake-a2nb-token\nroles:\n    - super\n"), 0o600)
					if err := config.Update(file, func(p *config.Project) error {
						p.Remotes = map[string]config.Remote{"testhost": remote}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				r := newRemotesReloader(file, pm, nil, time.Now)
				if c, err := r.reload(); err != nil || strings.Join(c.Added, ",") != "testhost" {
					t.Fatalf("reload: %+v %v", c, err)
				}
			}

			// Connected is told after the push and the sweep returned, and
			// both answer only once the serve has acted: then eng2's stream.
			v.await(func() bool { return v.connected() && v.shows("eng2") })
			if got := v.names(); got != "eng2" {
				t.Fatalf("this initech shows %q, want only the listed eng2", got)
			}
			if d.findPane("old") != nil || d.findPane("eng2") == nil {
				t.Fatal("connected before old was stopped and eng2 pushed")
			}
			if eng9.IsAlive() != true || sweepPID(d.findPane("eng9")) != pid9 {
				t.Fatal("the serve's own eng9 was stopped or replaced by a connect")
			}
			if eng8.IsAlive() != true || sweepPID(d.findPane("eng8")) != pid8 {
				t.Fatal("another initech's eng8 was stopped or replaced by a connect")
			}
			if got := d.pushedBy("eng2"); got != sweepSelf {
				t.Fatalf("eng2 recorded as pushed by %q", got)
			}
			out := logs.String()
			for _, name := range []string{"eng9", "eng8"} {
				if strings.Contains(out, "stopped orphan\" peer=testhost agent="+name) {
					t.Fatalf("the log says %s, not ours, was stopped", name)
				}
			}
			for _, name := range []string{"eng9", "eng8"} {
				if !strings.Contains(out, "left running: not pushed by this initech\" peer=testhost agent="+name) {
					t.Fatalf("the log does not name %s as left running:\n%s", name, out)
				}
			}
			if !strings.Contains(out, "stopped orphan\" peer=testhost agent=old") {
				t.Fatalf("the log does not say old was stopped:\n%s", out)
			}
			// A hidden pane's stream ends: only the ring buffer still feeds.
			logs.await("stream removed from MultiSink\" agent=eng9")
			if n := d.multiSinkFor("eng9").Len(); n != 1 {
				t.Fatalf("eng9 still feeds %d writers", n)
			}
		})
	}
}

// TestSweep_NoRolesListShowsAllAndStopsNone: the case with nothing to narrow.
func TestSweep_NoRolesListShowsAllAndStopsNone(t *testing.T) {
	sweepPosix(t)
	sweepBuilder(t)
	td := startTestDaemon(t, "fake-a2nb-token", "eng9")
	d := td.daemon
	sweepPush(t, d, "old", sweepSelf)
	v := &sweepView{sig: make(chan struct{}, 1)}
	project := &config.Project{Name: "test", PeerName: sweepSelf, Token: "fake-a2nb-token", Root: t.TempDir()}
	pm := sweepManager(t, project, v)
	pm.runsMu.Lock()
	pm.startPeerLocked("testhost", config.Remote{Addr: td.addr, Token: "fake-a2nb-token"}, project)
	pm.runsMu.Unlock()
	v.await(func() bool { return v.connected() && v.names() == "eng9,old" })
	if d.findPane("old") == nil || !d.findPane("eng9").IsAlive() {
		t.Fatal("no roles list stopped something")
	}
}

// TestSweep_APeerWithNoPanesGetsTheListPushedAndNothingElse is feed-it-nothing:
// a peer running nothing, and a list naming an agent it does not have.
func TestSweep_APeerWithNoPanesGetsTheListPushedAndNothingElse(t *testing.T) {
	sweepPosix(t)
	sweepBuilder(t)
	logs := sweepCaptureLogs(t)
	td := startTestDaemon(t, "fake-a2nb-token")
	v := &sweepView{sig: make(chan struct{}, 1)}
	project := &config.Project{Name: "test", PeerName: sweepSelf, Token: "fake-a2nb-token", Root: t.TempDir()}
	pm := sweepManager(t, project, v)
	pm.runsMu.Lock()
	pm.startPeerLocked("testhost", config.Remote{Addr: td.addr, Token: "fake-a2nb-token", Roles: []string{"eng2"}}, project)
	pm.runsMu.Unlock()
	v.await(func() bool { return v.connected() && v.shows("eng2") })
	if got := v.names(); got != "eng2" {
		t.Fatalf("this initech shows %q, want only eng2", got)
	}
	t.Cleanup(func() { td.daemon.removePane("eng2") })
	if out := logs.String(); strings.Contains(out, "left running") || strings.Contains(out, "stopped orphan") {
		t.Fatalf("nothing was there to leave or stop:\n%s", out)
	}
}

// TestSweep_AnExplicitStopOfAnAgentWithNoOwnerStillWorks: the sweep changed,
// not the operator's stop.
func TestSweep_AnExplicitStopOfAnAgentWithNoOwnerStillWorks(t *testing.T) {
	sweepPosix(t)
	td := startTestDaemon(t, "fake-a2nb-token", "eng9")
	line, _ := json.Marshal(StopAgentCmd{Action: "stop_agent", Name: "eng9"})
	if resp := td.daemon.handleStopAgent(line, sweepSelf); !resp.OK {
		t.Fatalf("an explicit stop was refused: %+v", resp)
	}
	// The stop answers after the pane closed, so no wait.
	if td.daemon.findPane("eng9").IsAlive() {
		t.Fatal("eng9 still alive after an explicit stop answered")
	}
}

// TestPushedByThisInitech: who the sweep may stop, including the empty cases.
func TestPushedByThisInitech(t *testing.T) {
	running := []AgentStatus{{Name: "mine", PushedBy: sweepSelf}, {Name: "theirs", PushedBy: "another-rig"}, {Name: "nobodys"}}
	if got := pushedByThisInitech(running, sweepSelf); len(got) != 1 || !got["mine"] {
		t.Fatalf("owned %v", got)
	}
	// An initech with no peer name owns nothing, not the ownerless panes.
	if got := pushedByThisInitech(running, ""); len(got) != 0 {
		t.Fatalf("no peer name owned %v", got)
	}
	// An older serve names no pusher: nothing is ours, so nothing is stopped.
	dec := computePushDiff([]string{"eng2"}, []AgentStatus{{Name: "a"}, {Name: "b"}}, pushedByThisInitech([]AgentStatus{{Name: "a"}, {Name: "b"}}, sweepSelf))
	if len(dec.Stop) != 0 || strings.Join(dec.LeftRunning, ",") != "a,b" {
		t.Fatalf("an older serve: %+v", dec)
	}
}

// TestHiddenByRoles: a list hides what it does not name; no list hides nothing.
func TestHiddenByRoles(t *testing.T) {
	h := hiddenByRoles([]string{"eng2"})
	if h("eng2") || !h("eng9") {
		t.Fatal("a list must show what it names and hide the rest")
	}
	if none := hiddenByRoles(nil); none("eng9") {
		t.Fatal("no list hid an agent")
	}
}

// TestSweep_ALaterStreamForAnUnlistedAgentGetsNoPane: a stream_added after the
// handshake is held to the same list.
func TestSweep_ALaterStreamForAnUnlistedAgentGetsNoPane(t *testing.T) {
	v := &sweepView{sig: make(chan struct{}, 1)}
	pm := &peerManager{onPaneAdded: v.added}
	pc := &peerConn{hidden: hiddenByRoles([]string{"eng2"})}
	a, b := net.Pipe()
	defer b.Close()
	pm.attachPushedStream("testhost", pc, "eng9", a)
	if v.names() != "" || len(pc.panes) != 0 {
		t.Fatalf("an unlisted agent got a pane: %q", v.names())
	}
	if _, err := a.Write([]byte("x")); err == nil {
		t.Fatal("the unlisted agent's stream was left open")
	}
}

// TestDiscardStream_APeerWritingMoreThanTheWindowIsNotStalled: a closed stream
// nobody reads would hold the peer's write once the window fills, and with it
// the agent's output path.
func TestDiscardStream_APeerWritingMoreThanTheWindowIsNotStalled(t *testing.T) {
	cli, srv := net.Pipe()
	client, err := yamux.Client(cli, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := yamux.Server(srv, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	out, err := server.Open()
	if err != nil {
		t.Fatal(err)
	}
	in, err := client.Accept()
	if err != nil {
		t.Fatal(err)
	}
	discardStream(in)
	wrote := make(chan error, 1)
	go func() {
		_, err := out.Write(make([]byte, 1024*1024))
		wrote <- err
	}()
	// A stall hangs here until the test binary's -timeout: no clock of ours.
	if err := <-wrote; err != nil {
		t.Fatalf("the peer's write failed: %v", err)
	}
	buf := make([]byte, 16)
	if _, err := out.Read(buf); err == nil {
		t.Fatal("the peer never saw the stream end")
	}
}
