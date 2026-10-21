//go:build !windows

package tui

// ini-cxia: the second-window listener refuses connections without the
// session's token. Before the fix the hello check ran only when the project
// had a token, and nothing gave a TUI project one, so any local process got
// hello_ok: every pane's output, raw keys into every PTY, agent control.

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/nmelo/initech/internal/config"
)

// tokenlessWindowProject is a window-1 project with no configured token,
// rooted in its own temp dir so a token file never lands outside the test.
// The root is short: the window server binds no unix socket, but .initech
// paths stay well under any length limit.
func tokenlessWindowProject(t *testing.T) *config.Project {
	t.Helper()
	root, err := os.MkdirTemp("", "cxia")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &config.Project{Name: "winproj", Root: root, PeerName: "window1", WindowListen: "127.0.0.1:0"}
}

// startTokenlessWindowServer serves a tokenless project and returns the
// server, its address, the project and the cleanup (also registered).
func startTokenlessWindowServer(t *testing.T) (*windowServer, string, *config.Project, func()) {
	t.Helper()
	proj := tokenlessWindowProject(t)
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, cleanup, err := startWindowServer(proj, "test", panes, func(f func()) { go f() }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	return ws, ws.Addr(), proj, cleanup
}

// helloWithToken sends one hello carrying token and returns the first reply's
// action and error text, and how long the reply took.
func helloWithToken(t *testing.T, addr, token string) (action, errText string, took time.Duration) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	ctrl, err := session.Open()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}
	start := time.Now()
	if err := writeJSON(ctrl, HelloMsg{Action: "hello", Version: ProtocolVersion, Token: token, PeerName: "window2"}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	sc := NewIPCScanner(ctrl)
	if !sc.Scan() {
		t.Fatal("no reply to hello")
	}
	took = time.Since(start)
	var reply struct {
		Action string `json:"action"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(sc.Bytes(), &reply); err != nil {
		t.Fatalf("reply %q: %v", sc.Bytes(), err)
	}
	return reply.Action, reply.Error, took
}

// AC 1: with no configured token, a wrong token and an empty token are both
// refused with the auth-failed reply after the brute-force delay.
func TestWindowServer_RefusesAHelloWithAWrongOrEmptyToken(t *testing.T) {
	_, addr, _, _ := startTokenlessWindowServer(t)
	for _, tok := range []string{"wrong-token", ""} {
		action, errText, took := helloWithToken(t, addr, tok)
		if action != "error" || errText != "auth failed" {
			t.Errorf("hello with token %q: got %q/%q, want error/auth failed; any process on the host could attach", tok, action, errText)
		}
		if action == "error" && took < 900*time.Millisecond {
			t.Errorf("hello with token %q refused after %v, want the 1 s brute-force delay", tok, took)
		}
	}
}

// viewerOf is window 2's view of proj: same root, window 1 as its peer at
// addr, with the configured token (if any) the shared config carries.
func viewerOf(proj *config.Project, addr string) (*config.Project, config.Remote) {
	v := *proj
	v.PeerName = "window-2"
	v.WindowListen = ""
	return &v, config.Remote{Addr: addr, Token: proj.Token}
}

func connectWindowTwo(t *testing.T, proj *config.Project, addr string) (*peerConn, error) {
	t.Helper()
	v, remote := viewerOf(proj, addr)
	pc, err := connectPeer(WindowOnePeerName, remote, v)
	if pc != nil {
		t.Cleanup(func() { pc.session.Close() })
	}
	return pc, err
}

func readTokenFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(windowTokenPath(root))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	return string(b)
}

// AC 2: window 2 on the same machine connects with no config change: window
// 1 wrote the file and window 2 read it.
func TestWindowToken_SecondWindowConnectsFromTheTokenFile(t *testing.T) {
	_, addr, proj, _ := startTokenlessWindowServer(t)
	if _, err := connectWindowTwo(t, proj, addr); err != nil {
		t.Fatalf("window 2 could not connect from the token file: %v", err)
	}
}

// AC 3: a configured token is served as before and no file is written.
func TestWindowToken_AConfiguredTokenWritesNoFile(t *testing.T) {
	proj := tokenlessWindowProject(t)
	proj.Token = "configured-token"
	ws, cleanup, err := startWindowServer(proj, "test", []*Pane{windowServerTestPane("eng1")}, func(f func()) { go f() }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	if _, err := os.Stat(windowTokenPath(proj.Root)); !os.IsNotExist(err) {
		t.Fatalf("a configured token wrote %s (err=%v)", windowTokenPath(proj.Root), err)
	}
	if _, err := connectWindowTwo(t, proj, ws.Addr()); err != nil {
		t.Fatalf("window 2 with the configured token: %v", err)
	}
	// A token file left lying around must not override the configured one.
	if err := writeWindowToken(proj.Root, "stray-file-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectWindowTwo(t, proj, ws.Addr()); err != nil {
		t.Fatalf("a stray token file overrode the configured token: %v", err)
	}
}

// AC 4: the file is 0600 inside .initech, removed by window 1's cleanup.
func TestWindowToken_FileIs0600AndRemovedOnCleanExit(t *testing.T) {
	_, _, proj, cleanup := startTokenlessWindowServer(t)
	fi, err := os.Stat(windowTokenPath(proj.Root))
	if err != nil {
		t.Fatalf("no token file after window 1 started: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, want 0600: any user on the host could read it", fi.Mode().Perm())
	}
	if len(readTokenFile(t, proj.Root)) != 64 {
		t.Fatalf("token is not 32 random bytes, hex")
	}
	entries, _ := os.ReadDir(filepath.Join(proj.Root, ".initech"))
	for _, e := range entries {
		if e.Name() != windowTokenFile {
			t.Errorf("leftover %s in .initech: the temp file of the atomic write was not renamed away", e.Name())
		}
	}
	cleanup()
	if _, err := os.Stat(windowTokenPath(proj.Root)); !os.IsNotExist(err) {
		t.Fatalf("token file survived window 1's clean exit (err=%v)", err)
	}
}

// AC 4 as ruled: a stale file from a crashed session is overwritten at the
// next start, a window 2 holding the old token is refused, and its next
// attempt (re-reading the file) connects with no restart.
func TestWindowToken_WindowTwoRecoversAfterWindowOneRestarts(t *testing.T) {
	proj := tokenlessWindowProject(t)
	if err := writeWindowToken(proj.Root, "stale-token-from-a-crash"); err != nil {
		t.Fatal(err)
	}
	start := func() (*windowServer, func()) {
		ws, cleanup, err := startWindowServer(proj, "test", []*Pane{windowServerTestPane("eng1")}, func(f func()) { go f() }, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("startWindowServer: %v", err)
		}
		return ws, cleanup
	}
	ws1, cleanup1 := start()
	tok1 := readTokenFile(t, proj.Root)
	if tok1 == "stale-token-from-a-crash" {
		t.Fatal("the stale token file was not overwritten at start")
	}
	if _, err := connectWindowTwo(t, proj, ws1.Addr()); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	cleanup1()

	ws2, cleanup2 := start() // window 1 restarted: a new token
	t.Cleanup(cleanup2)
	if readTokenFile(t, proj.Root) == tok1 {
		t.Fatal("a restarted window 1 reused its previous token")
	}
	// The next attempt re-reads the file and connects.
	if _, err := connectWindowTwo(t, proj, ws2.Addr()); err != nil {
		t.Fatalf("window 2 did not recover after window 1 restarted: %v", err)
	}
	// A hello still holding the old token is refused.
	if action, errText, _ := helloWithToken(t, ws2.Addr(), tok1); action != "error" || errText != "auth failed" {
		t.Fatalf("the previous session's token was accepted: %q/%q", action, errText)
	}
}

// AC 4 and feed-it-nothing: with no usable token file, window 2's attempt
// fails without dialling and says to start window 1 first.
func TestWindowToken_NoUsableFileFailsWithoutDialling(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dialled := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			dialled <- struct{}{}
			c.Close()
		}
	}()
	cases := map[string]func(root string){
		"no .initech directory": func(root string) { os.RemoveAll(filepath.Join(root, ".initech")) },
		"empty file":            func(root string) { os.WriteFile(windowTokenPath(root), nil, 0o600) },
		"only a newline":        func(root string) { os.WriteFile(windowTokenPath(root), []byte("\n"), 0o600) },
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			proj := tokenlessWindowProject(t)
			prepare(proj.Root)
			_, err := connectWindowTwo(t, proj, ln.Addr().String())
			if !errors.Is(err, errNoWindowToken) {
				t.Fatalf("err = %v, want errNoWindowToken", err)
			}
			if !strings.Contains(err.Error(), "start it first") {
				t.Errorf("error %q does not tell the operator to start window 1", err)
			}
		})
	}
	select {
	case <-dialled:
		t.Fatal("window 2 dialled window 1 without a token")
	case <-time.After(100 * time.Millisecond):
	}
}

// AC 5: the token reaches no log line, no error text and no hint.
func TestWindowToken_NeverReachesTheLogOrAnyMessage(t *testing.T) {
	logs := captureLogs(t)
	_, addr, proj, cleanup := startTokenlessWindowServer(t)
	tok := readTokenFile(t, proj.Root)
	helloWithToken(t, addr, "wrong-token")
	if _, err := connectWindowTwo(t, proj, addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	cleanup()
	if _, err := connectWindowTwo(t, proj, addr); err == nil || strings.Contains(err.Error(), tok) {
		t.Fatalf("after window 1 exited: err=%v (must fail, without the token in its text)", err)
	}
	if strings.Contains(logs.String(), tok) {
		t.Fatal("the window token appears in the log")
	}
	for _, s := range []string{errNoWindowToken.Error(), noWindowOneHint, unservedViewerHint} {
		if strings.Contains(s, tok) {
			t.Fatalf("the window token appears in %q", s)
		}
	}
}

// AC 5, by inventory: nothing in the token path starts a process, so the
// token cannot reach a command line.
func TestWindowToken_TheTokenPathStartsNoProcess(t *testing.T) {
	for _, f := range []string{"window_token.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"os/exec", "exec.Command", "Runner", "Run("} {
			if strings.Contains(string(b), banned) {
				t.Errorf("%s contains %q: the token path must not start a process", f, banned)
			}
		}
	}
}

// The empty-viewer hint: the peer manager reports a missing token file for
// the window-1 peer only, and the viewer then says to start window 1.
func TestWindowToken_MissingFileTurnsTheViewerHintToStartWindowOne(t *testing.T) {
	var got []bool
	pm := &peerManager{onWindowToken: func(m bool) { got = append(got, m) }}
	pm.noteWindowToken(WindowOnePeerName, errNoWindowToken)
	pm.noteWindowToken(WindowOnePeerName, nil)
	pm.noteWindowToken("workbench", errNoWindowToken) // a remote host: not reported
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("reports = %v, want [true false]", got)
	}

	u := newTestTUI()
	u.windowID = "window-2"
	if hint, _ := u.classifyViewerEmpty(); hint != unservedViewerHint {
		t.Fatalf("hint with the token present = %q", hint)
	}
	u.windowTokenMissing = true
	if hint, _ := u.classifyViewerEmpty(); hint != noWindowOneHint {
		t.Fatalf("hint with no token file = %q, want %q", hint, noWindowOneHint)
	}
}

// An empty project root never resolves the token path against the working
// directory.
func TestWindowToken_AProjectWithNoRootTouchesNoFile(t *testing.T) {
	if err := writeWindowToken("", "x"); !errors.Is(err, errNoProjectRoot) {
		t.Fatalf("write with no root: %v", err)
	}
	if _, err := readWindowToken(""); !errors.Is(err, errNoProjectRoot) {
		t.Fatalf("read with no root: %v", err)
	}
	if _, err := os.Stat(".initech"); !os.IsNotExist(err) {
		t.Fatalf("a .initech appeared in the working directory (err=%v)", err)
	}
}
