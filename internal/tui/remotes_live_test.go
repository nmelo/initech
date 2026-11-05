package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// ini-7op8: a remote added to or removed from initech.yaml while window 1
// runs connects or detaches without a restart.

// livePeer is a loopback initech serve that takes connection after
// connection, so a test can count them: one agent stream each, every control
// command answered OK and recorded.
type livePeer struct {
	name string
	ln   net.Listener

	mu       sync.Mutex
	events   []string // "open N" and "close N", in order.
	opened   int
	live     int
	maxLive  int
	tokens   []string // The hello token of each connection, never printed.
	commands []string // Every control command's action:name.
	sessions []*yamux.Session
}

func startLivePeer(t *testing.T, name string) *livePeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lp := &livePeer{name: name, ln: ln}
	go lp.serve()
	t.Cleanup(lp.close)
	return lp
}

func (lp *livePeer) addr() string { return lp.ln.Addr().String() }

func (lp *livePeer) serve() {
	for {
		conn, err := lp.ln.Accept()
		if err != nil {
			return
		}
		go lp.handle(conn)
	}
}

func (lp *livePeer) handle(conn net.Conn) {
	session, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		return
	}
	ctrl, err := session.Accept()
	if err != nil {
		session.Close()
		return
	}
	sc := bufio.NewScanner(ctrl)
	if !sc.Scan() {
		session.Close()
		return
	}
	var hello HelloMsg
	json.Unmarshal(sc.Bytes(), &hello)
	lp.mu.Lock()
	lp.opened++
	n := lp.opened
	lp.live++
	if lp.live > lp.maxLive {
		lp.maxLive = lp.live
	}
	lp.events = append(lp.events, "open "+itoa(n))
	lp.tokens = append(lp.tokens, hello.Token)
	lp.sessions = append(lp.sessions, session)
	lp.mu.Unlock()
	go func() {
		<-session.CloseChan()
		lp.mu.Lock()
		lp.live--
		lp.events = append(lp.events, "close "+itoa(n))
		lp.mu.Unlock()
	}()
	writeJSON(ctrl, HelloOKMsg{Action: "hello_ok", Version: ProtocolVersion, PeerName: lp.name,
		Agents: []AgentStatus{{Name: "eng9", Alive: true}}})
	stream, err := session.Open()
	if err != nil {
		return
	}
	id := stream.(*yamux.Stream).StreamID()
	writeJSON(ctrl, StreamMapMsg{Action: "stream_map", Streams: map[uint32]string{id: "eng9"}})
	for sc.Scan() {
		var cmd struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Name   string `json:"name"`
		}
		if json.Unmarshal(sc.Bytes(), &cmd) != nil {
			continue
		}
		lp.mu.Lock()
		lp.commands = append(lp.commands, cmd.Action+":"+cmd.Name)
		lp.mu.Unlock()
		writeJSON(ctrl, ControlResp{ID: cmd.ID, OK: true})
	}
}

func (lp *livePeer) close() {
	lp.ln.Close()
	lp.mu.Lock()
	ss := append([]*yamux.Session(nil), lp.sessions...)
	lp.mu.Unlock()
	for _, s := range ss {
		s.Close()
	}
}

func (lp *livePeer) snap() (events, tokens, commands []string, live, maxLive int) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	return append([]string(nil), lp.events...), append([]string(nil), lp.tokens...),
		append([]string(nil), lp.commands...), lp.live, lp.maxLive
}

// paneBook records what the manager reports, as window 1 would show it.
type paneBook struct {
	mu        sync.Mutex
	panes     map[string][]PaneView
	connected map[string]bool
	removed   []string
}

func newPaneBook() *paneBook {
	return &paneBook{panes: map[string][]PaneView{}, connected: map[string]bool{}}
}

func (b *paneBook) onChange(peer string, panes []PaneView, connected bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.panes[peer] = panes
	b.connected[peer] = connected
}

func (b *paneBook) onRemoved(name string, changed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.panes, name)
	delete(b.connected, name)
	b.removed = append(b.removed, name)
}

func (b *paneBook) showing(peer string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.connected[peer] {
		return 0
	}
	return len(b.panes[peer])
}

// pushBuilder makes role pushes reach the wire in a test.
func pushBuilder(t *testing.T) {
	t.Helper()
	old := defaultConfigureAgentBuilder
	t.Cleanup(func() { defaultConfigureAgentBuilder = old })
	defaultConfigureAgentBuilder = func(role string, _ *config.Project, _ config.Remote) (ConfigureAgentCmd, error) {
		return ConfigureAgentCmd{Action: "configure_agent", Name: role, Command: []string{"claude"}}, nil
	}
}

func liveManager(t *testing.T, project *config.Project, book *paneBook) *peerManager {
	t.Helper()
	quit := make(chan struct{})
	pm := newPeerManager(project, book.onChange, nil, quit)
	t.Cleanup(func() {
		close(quit)
		pm.wait()
	})
	return pm
}

func withRemotes(remotes map[string]config.Remote) *config.Project {
	return &config.Project{Name: "rig", PeerName: "nmbp", Token: "project-token", Remotes: remotes}
}

// TestDiffRemotes: only addr and token are a change; the empty cases do
// nothing.
func TestDiffRemotes(t *testing.T) {
	a := config.Remote{Addr: "h:1", Token: "x"}
	for name, tc := range map[string]struct {
		running, wanted map[string]config.Remote
		want            remotesChange
	}{
		"nothing to nothing": {nil, nil, remotesChange{}},
		"the same set":       {map[string]config.Remote{"wb": a}, map[string]config.Remote{"wb": a}, remotesChange{}},
		"added":              {nil, map[string]config.Remote{"wb": a}, remotesChange{Added: []string{"wb"}}},
		"removed":            {map[string]config.Remote{"wb": a}, nil, remotesChange{Removed: []string{"wb"}}},
		"new addr":           {map[string]config.Remote{"wb": a}, map[string]config.Remote{"wb": {Addr: "h:2", Token: "x"}}, remotesChange{Changed: []string{"wb"}}},
		"new token":          {map[string]config.Remote{"wb": a}, map[string]config.Remote{"wb": {Addr: "h:1", Token: "y"}}, remotesChange{Changed: []string{"wb"}}},
		"new roles only":     {map[string]config.Remote{"wb": a}, map[string]config.Remote{"wb": {Addr: "h:1", Token: "x", Roles: []string{"eng9"}}}, remotesChange{}},
	} {
		got := diffRemotes(tc.running, tc.wanted)
		if strings.Join(got.Removed, ",") != strings.Join(tc.want.Removed, ",") ||
			strings.Join(got.Changed, ",") != strings.Join(tc.want.Changed, ",") ||
			strings.Join(got.Added, ",") != strings.Join(tc.want.Added, ",") {
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}

// TestLiveRemotes_AddConnectsPushesRolesAndShowsPanes is AC1 at the manager:
// red at base, where a manager started with no remotes never dials one.
func TestLiveRemotes_AddConnectsPushesRolesAndShowsPanes(t *testing.T) {
	pushBuilder(t)
	peer := startLivePeer(t, "workbench")
	book := newPaneBook()
	pm := liveManager(t, withRemotes(nil), book)
	c := pm.reconcile(withRemotes(map[string]config.Remote{"workbench": {Addr: peer.addr(), Roles: []string{"eng9"}}}), book.onRemoved)
	if strings.Join(c.Added, ",") != "workbench" {
		t.Fatalf("reconcile said %+v", c)
	}
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "workbench's agent to show")
	_, tokens, cmds, _, _ := peer.snap()
	if strings.Join(cmds, ",") != "configure_agent:eng9" {
		t.Fatalf("the peer got %v, want the role pushed", cmds)
	}
	if len(tokens) != 1 || tokens[0] != "project-token" {
		t.Fatal("a remote with no token of its own did not connect with the project's")
	}
}

// TestLiveRemotes_RemoveDetachesAndStopsNothingOnThePeer is AC2.
func TestLiveRemotes_RemoveDetachesAndStopsNothingOnThePeer(t *testing.T) {
	peer := startLivePeer(t, "workbench")
	book := newPaneBook()
	pm := liveManager(t, withRemotes(map[string]config.Remote{"workbench": {Addr: peer.addr()}}), book)
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "workbench to connect")
	c := pm.reconcile(withRemotes(nil), book.onRemoved)
	if strings.Join(c.Removed, ",") != "workbench" {
		t.Fatalf("reconcile said %+v", c)
	}
	// reconcile returns once the manager has stopped, so the close is seen
	// on the peer at once and nothing reconnects after.
	waitFor(t, 5*time.Second, func() bool { _, _, _, live, _ := peer.snap(); return live == 0 }, "the peer to see the close")
	time.Sleep(1500 * time.Millisecond) // Longer than the reconnect pause.
	events, _, cmds, _, _ := peer.snap()
	if strings.Join(events, ",") != "open 1,close 1" {
		t.Fatalf("the peer saw %v, want one connection, closed, and no redial", events)
	}
	for _, c := range cmds {
		if strings.HasPrefix(c, "stop_agent") || strings.HasPrefix(c, "suspend") {
			t.Fatalf("remove sent %q to the peer; nothing on it may be stopped", c)
		}
	}
	if book.showing("workbench") != 0 || len(book.removed) != 1 {
		t.Fatalf("after remove the panel shows %d workbench panes, removed %v", book.showing("workbench"), book.removed)
	}
}

// TestLiveRemotes_ANewAddrClosesTheOldBeforeOpeningTheNew is AC3: remove then
// add, one connection at a time, no duplicate pane.
func TestLiveRemotes_ANewAddrClosesTheOldBeforeOpeningTheNew(t *testing.T) {
	oldPeer := startLivePeer(t, "workbench")
	newPeer := startLivePeer(t, "workbench")
	book := newPaneBook()
	var order []string
	var mu sync.Mutex
	pm := liveManager(t, withRemotes(map[string]config.Remote{"workbench": {Addr: oldPeer.addr()}}), book)
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "the first connection")
	c := pm.reconcile(withRemotes(map[string]config.Remote{"workbench": {Addr: newPeer.addr()}}), func(name string, changed bool) {
		// The client has closed; the old peer notices a moment later. A
		// remove that did not close would never get here with it at 0.
		deadline := time.Now().Add(2 * time.Second)
		live := 1
		for live != 0 && time.Now().Before(deadline) {
			_, _, _, live, _ = oldPeer.snap()
			time.Sleep(5 * time.Millisecond)
		}
		_, _, _, opened, _ := newPeer.snap()
		mu.Lock()
		order = append(order, "removed", "old live "+itoa(live), "new live "+itoa(opened))
		mu.Unlock()
		book.onRemoved(name, changed)
	})
	if strings.Join(c.Changed, ",") != "workbench" {
		t.Fatalf("reconcile said %+v", c)
	}
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "the new connection")
	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()
	if got != "removed,old live 0,new live 0" {
		t.Fatalf("at the remove: %s; want the old closed and the new not yet open", got)
	}
	if ev, _, _, _, _ := oldPeer.snap(); strings.Join(ev, ",") != "open 1,close 1" {
		t.Fatalf("the old addr saw %v", ev)
	}
	if ev, _, _, _, maxLive := newPeer.snap(); strings.Join(ev, ",") != "open 1" || maxLive != 1 {
		t.Fatalf("the new addr saw %v (max live %d)", ev, maxLive)
	}
}

// TestLiveRemotes_ANewTokenAloneReconnects is AC3's token half: the peer sees
// a new session carrying the new token.
func TestLiveRemotes_ANewTokenAloneReconnects(t *testing.T) {
	peer := startLivePeer(t, "workbench")
	book := newPaneBook()
	pm := liveManager(t, withRemotes(map[string]config.Remote{"workbench": {Addr: peer.addr(), Token: "first"}}), book)
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "the first connection")
	pm.reconcile(withRemotes(map[string]config.Remote{"workbench": {Addr: peer.addr(), Token: "second"}}), book.onRemoved)
	waitFor(t, 5*time.Second, func() bool { _, tok, _, _, _ := peer.snap(); return len(tok) == 2 }, "the second connection")
	events, tokens, _, _, maxLive := peer.snap()
	if tokens[0] != "first" || tokens[1] != "second" {
		t.Fatal("the reconnect did not carry the new token")
	}
	if strings.Join(events[:2], ",") != "open 1,close 1" || maxLive != 1 {
		t.Fatalf("events %v, max live %d: the old session must close before the new opens", events, maxLive)
	}
}

// TestLiveRemotes_RemovingARemoteThatNeverAnswersReturns: a remote whose
// handshake hangs is stopped at once, not after a timeout.
func TestLiveRemotes_RemovingARemoteThatNeverAnswersReturns(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c // Never answers the hello.
		}
	}()
	book := newPaneBook()
	pm := liveManager(t, withRemotes(map[string]config.Remote{"mute": {Addr: ln.Addr().String()}}), book)
	select {
	case c := <-accepted:
		defer c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("never dialled")
	}
	start := time.Now()
	pm.reconcile(withRemotes(nil), book.onRemoved)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("removing a remote stuck in its handshake took %s", took)
	}
}

// TestLiveRemotes_FeedItNothing: an empty block, removing a name that is not
// there, and the same set again do nothing.
func TestLiveRemotes_FeedItNothing(t *testing.T) {
	book := newPaneBook()
	pm := liveManager(t, withRemotes(nil), book)
	if c := pm.reconcile(withRemotes(map[string]config.Remote{}), book.onRemoved); !c.Empty() || len(book.removed) != 0 {
		t.Fatalf("an empty block did %+v", c)
	}
	peer := startLivePeer(t, "workbench")
	set := withRemotes(map[string]config.Remote{"workbench": {Addr: peer.addr()}})
	pm.reconcile(set, book.onRemoved)
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "workbench")
	if c := pm.reconcile(set, book.onRemoved); !c.Empty() {
		t.Fatalf("the same set again did %+v", c)
	}
	if ev, _, _, _, _ := peer.snap(); strings.Join(ev, ",") != "open 1" {
		t.Fatalf("the same set again touched the connection: %v", ev)
	}
}

// ── the yaml watch ──────────────────────────────────────────────────

func writeYAML(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, mtime, mtime)
}

const yamlNoRemotes = "project: rig\nroot: /tmp/rig\ntoken: project-token\nroles: [eng1]\n"

func yamlWithRemote(addr string) string {
	return yamlNoRemotes + "remotes:\n  workbench:\n    addr: " + addr + "\n"
}

// TestRemotesReload_AnEditToTheFileConnectsAfterTheDebounce is AC1's yaml
// half: an edit is read once it has held still, not mid-save.
func TestRemotesReload_AnEditToTheFileConnectsAfterTheDebounce(t *testing.T) {
	peer := startLivePeer(t, "workbench")
	path := filepath.Join(t.TempDir(), "initech.yaml")
	t0 := time.Now().Add(-time.Hour)
	writeYAML(t, path, yamlNoRemotes, t0)
	book := newPaneBook()
	pm := liveManager(t, withRemotes(nil), book)
	clk := &fakeClock{t: time.Now()}
	r := newRemotesReloader(path, pm, book.onRemoved, clk.now)
	r.check()
	if len(pm.runs) != 0 {
		t.Fatal("the file as started was read as a change")
	}
	writeYAML(t, path, yamlWithRemote(peer.addr()), t0.Add(time.Minute))
	r.check() // Seen, not yet still.
	pm.runsMu.Lock()
	n := len(pm.runs)
	pm.runsMu.Unlock()
	if n != 0 {
		t.Fatal("an edit was applied before it held still")
	}
	clk.set(clk.now().Add(remotesDebounce))
	r.check()
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "workbench from the yaml")
}

// TestRemotesReload_ABadYAMLChangesNothingAndIsLoggedOnce is AC5.
func TestRemotesReload_ABadYAMLChangesNothingAndIsLoggedOnce(t *testing.T) {
	logs := captureLogs(t)
	peer := startLivePeer(t, "workbench")
	path := filepath.Join(t.TempDir(), "initech.yaml")
	writeYAML(t, path, yamlWithRemote(peer.addr()), time.Now())
	book := newPaneBook()
	pm := liveManager(t, withRemotes(map[string]config.Remote{"workbench": {Addr: peer.addr()}}), book)
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "workbench")
	r := newRemotesReloader(path, pm, book.onRemoved, time.Now)
	// Half-written: the remotes block cut off mid-line, and a token value
	// in a place yaml quotes back.
	writeYAML(t, path, yamlNoRemotes+"remotes:\n  workbench: [PLANTED-7op8-TOKEN\n", time.Now())
	for i := 0; i < 2; i++ {
		if _, err := r.reload(); err == nil {
			t.Fatal("a yaml that does not parse was accepted")
		}
	}
	// A value of the wrong kind, which yaml quotes back in its error.
	writeYAML(t, path, yamlNoRemotes+"remotes:\n  workbench:\n    addr: "+peer.addr()+"\n    roles: PLANTED-7op8-TOKEN\n", time.Now())
	if _, err := r.reload(); err == nil {
		t.Fatal("a yaml of the wrong shape was accepted")
	}
	if book.showing("workbench") != 1 || len(book.removed) != 0 {
		t.Fatal("a bad yaml dropped the running remote")
	}
	out := logs.String()
	if n := strings.Count(out, "initech.yaml refused"); n != 2 {
		t.Fatalf("logged %d times, want once per distinct error (2):\n%s", n, out)
	}
	// yaml quotes only a value's first characters ("PLANTED..."), so the
	// check is for its start: a token's first characters are a leak too.
	if strings.Contains(out, "PLANTED") {
		t.Fatal("the log carries the file's value")
	}
	// An edit touching only another key changes nothing in the remotes.
	writeYAML(t, path, strings.Replace(yamlWithRemote(peer.addr()), "roles: [eng1]", "roles: [eng1, eng2]", 1), time.Now())
	if c, err := r.reload(); err != nil || !c.Empty() {
		t.Fatalf("an edit to roles only did %+v, %v", c, err)
	}
	if ev, _, _, _, _ := peer.snap(); strings.Join(ev, ",") != "open 1" {
		t.Fatalf("an edit elsewhere touched the connection: %v", ev)
	}
}

// TestRemotesReload_TheTUIDropsARemovedRemotesPanesAndLayout: the panes leave
// t.panes and the saved layout, with the line that says the mail stays on the
// peer.
func TestRemotesReload_TheTUIDropsARemovedRemotesPanesAndLayout(t *testing.T) {
	tui := newTestTUI()
	rp := NewRemotePane("eng9", "workbench", nil, nil, 80, 24)
	tui.handlePeerUpdate("workbench", []PaneView{rp}, true)
	tui.layoutState.Order = []string{"eng1", "workbench:eng9"}
	tui.layoutState.Hidden = map[string]bool{"workbench:eng9": true}
	tui.handlePeerRemoved("workbench", false)
	for _, p := range tui.panes {
		if p.Host() == "workbench" {
			t.Fatal("a removed remote's pane is still in the panel")
		}
	}
	if strings.Join(tui.layoutState.Order, ",") != "eng1" || len(tui.layoutState.Hidden) != 0 {
		t.Fatalf("the layout keeps %v %v", tui.layoutState.Order, tui.layoutState.Hidden)
	}
	if _, ok := tui.peerConnected["workbench"]; ok {
		t.Fatal("a removed remote is still tracked as a peer")
	}
	found := false
	for _, ev := range tui.eventLog {
		if strings.Contains(ev.Detail, "workbench left the remotes (1 agents detached; their mail stays on the peer)") {
			found = true
		}
		if strings.Contains(ev.Detail, "workbench disconnected") {
			t.Fatal("a remove was announced as a disconnect")
		}
	}
	if !found {
		t.Fatalf("no remove line in %+v", tui.eventLog)
	}
}

// TestRemotesReload_TheIPCActionReloadsAndSaysWhat: initech remote add's
// signal reaches the reload through the real IPC dispatch.
func TestRemotesReload_TheIPCActionReloadsAndSaysWhat(t *testing.T) {
	peer := startLivePeer(t, "workbench")
	path := filepath.Join(t.TempDir(), "initech.yaml")
	writeYAML(t, path, yamlNoRemotes, time.Now())
	book := newPaneBook()
	pm := liveManager(t, withRemotes(nil), book)
	tui := newTestTUI()
	ask := func() IPCResponse {
		conn := &fakeConn{}
		if !tui.HandleExtended(conn, IPCRequest{Action: "remotes_reload"}, nil) {
			t.Fatal("remotes_reload is not an action")
		}
		var resp IPCResponse
		json.Unmarshal(conn.written[:conn.findNewline()], &resp)
		return resp
	}
	if resp := ask(); resp.OK || !strings.Contains(resp.Error, "window 1 does") {
		t.Fatalf("a window with no reloader answered %+v", resp)
	}
	tui.remotes = newRemotesReloader(path, pm, book.onRemoved, time.Now)
	writeYAML(t, path, yamlWithRemote(peer.addr()), time.Now())
	if resp := ask(); !resp.OK || resp.Data != "connecting workbench" {
		t.Fatalf("the reload answered %+v", resp)
	}
	waitFor(t, 5*time.Second, func() bool { return book.showing("workbench") == 1 }, "workbench")
	writeYAML(t, path, yamlNoRemotes, time.Now())
	if resp := ask(); !resp.OK || resp.Data != "removed workbench" {
		t.Fatalf("the reload answered %+v", resp)
	}
}
