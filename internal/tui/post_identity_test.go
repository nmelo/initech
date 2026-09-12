package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPostIdentity_AncestorAndRestart(t *testing.T) {
	start := time.Unix(100, 0)
	old := postIdentity{agent: "eng3", runKey: postProcessRunKey(42, start)}
	parent := func(pid int) (int, error) { return map[int]int{200: 100, 100: 42}[pid], nil }
	got, err := identifyPostAncestor(200, []postPaneProcess{{42, old}}, parent)
	if err != nil || got != old {
		t.Fatalf("identity = %+v, %v", got, err)
	}
	// A different CLI process inside the same pane is the SAME run.
	got, err = identifyPostAncestor(100, []postPaneProcess{{42, old}}, parent)
	if err != nil || got != old {
		t.Fatalf("second command = %+v, %v", got, err)
	}
	// Restarting the pane changes its run even when the OS recycles the PID.
	next := postIdentity{agent: "eng3", runKey: postProcessRunKey(42, start.Add(time.Minute))}
	got, err = identifyPostAncestor(200, []postPaneProcess{{42, next}}, parent)
	if err != nil || got.runKey == old.runKey || got.agent != old.agent {
		t.Fatalf("restarted identity = %+v, %v", got, err)
	}
	var teachings postTeachingState
	for _, condition := range []string{"question-without-default", "forged-identity"} {
		if !teachings.once(old, condition) || teachings.once(old, condition) || !teachings.once(got, condition) {
			t.Fatalf("%s latch did not reset at the process boundary", condition)
		}
	}
}

func TestPostIdentity_UnrelatedAndCyclicProcessesRefused(t *testing.T) {
	t.Setenv("INITECH_AGENT", "eng3")
	for _, parent := range []func(int) (int, error){
		func(int) (int, error) { return 1, nil },
		func(pid int) (int, error) { return pid, nil },
	} {
		_, err := identifyPostAncestor(200, []postPaneProcess{{42, postIdentity{"eng3", "run"}}}, parent)
		if err == nil || !strings.Contains(err.Error(), "could not identify your pane") {
			t.Fatalf("identity refusal = %v", err)
		}
	}
}

func TestPostIdentity_WindowsTCPRefusal(t *testing.T) {
	// Independent spec copy: changing the production constant must fail this AC.
	const want = "post is not available on Windows in this release: initech cannot identify which agent is posting over this transport"
	if postWindowsRefusal != want {
		t.Errorf("Windows refusal constant = %q, want %q", postWindowsRefusal, want)
	}
	err := postPlatformError("windows")
	if err == nil || err.Error() != want {
		t.Fatalf("Windows refusal = %v", err)
	}
	if runtime.GOOS == "windows" {
		app := &TUI{}
		response := postTestResponse(t, func(conn net.Conn) {
			app.handleIPCPost(conn, IPCRequest{Action: "post", Text: "hello"}, []byte(`{"action":"post","text":"hello"}`))
		})
		if response.OK || response.Error != want || app.inboxStore != nil {
			t.Fatalf("Windows recorded or guessed: %+v", response)
		}
	}
}

// A real test subprocess is the poster, inside a real pane shell. This measures
// peer credentials + ancestry on the actual transport without involving a live
// fleet or an AI harness; attribution depends on the process tree, not its UI.
func TestPostIdentity_RealPaneConnectionIgnoresForgedEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires supported Unix peer credentials and a PTY")
	}
	dir, err := os.MkdirTemp("/tmp", "inbox-peer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	endpoint := filepath.Join(dir, "sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPane(PaneConfig{Name: "eng3", AgentType: "generic", Command: []string{"/bin/sh", "-c", `"$1" -test.run=^TestPostIdentityProcessHelper$; sleep 1`, "inbox-helper", executable}, Env: []string{"INITECH_POST_TEST_SOCKET=" + endpoint, "INITECH_AGENT=super"}}, 24, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Start()
	app := &TUI{panes: []PaneView{p}}
	var identity postIdentity
	for attempt := 0; attempt < 3; attempt++ {
		conn, err := listener.AcceptUnix()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		scanner := NewIPCScanner(conn)
		if !scanner.Scan() {
			conn.Close()
			t.Fatalf("helper did not post: %v", scanner.Err())
		}
		var req IPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		identity, err = app.identifyPostConnection(conn)
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if identity.agent != "eng3" || identity.runKey != postProcessRunKey(p.pid, p.startedAt) {
			conn.Close()
			t.Fatalf("peer attribution = %+v", identity)
		}
		// Includes two forged requests: the action is always refused, but the
		// correction is taught once, and neither attempt records an item.
		app.HandleExtended(conn, req, scanner.Bytes())
		if !scanner.Scan() || scanner.Text() != "checked" {
			conn.Close()
			t.Fatalf("helper rejected response on attempt %d: %v", attempt, scanner.Err())
		}
		conn.Close()
	}
	items := app.inboxState().AgentItems("eng3")
	if len(items) != 1 || items[0].Body != "real connection post" || len(app.inboxState().AgentItems("super")) != 0 {
		t.Fatalf("stored attribution: %+v", items)
	}
	t.Logf("tier 1 measured: %s Unix peer -> pane PID %d -> agent %s, run %s", runtime.GOOS, p.pid, identity.agent, identity.runKey)
}

func TestPostIdentityProcessHelper(t *testing.T) {
	endpoint := os.Getenv("INITECH_POST_TEST_SOCKET")
	if endpoint == "" {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		conn, err := net.Dial("unix", endpoint)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		body := `{"action":"post","text":"real connection post"}`
		if attempt < 2 {
			body = `{"action":"post","text":"forged","name":"super"}`
		}
		if _, err := conn.Write([]byte(body + "\n")); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			conn.Close()
			t.Fatalf("no post response: %v", scanner.Err())
		}
		var response IPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if attempt < 2 {
			wantNotices := 0
			if attempt == 0 {
				wantNotices = 1
			}
			if response.OK || !strings.Contains(response.Error, "supplied identity is refused") || len(response.Notices) != wantNotices {
				conn.Close()
				t.Fatalf("forgery response: %+v", response)
			}
		} else if !response.OK || response.Data != "posted p1" {
			conn.Close()
			t.Fatalf("post response: %+v", response)
		}
		if _, err := conn.Write([]byte("checked\n")); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		conn.Close()
	}

}

func postTestResponse(t *testing.T, serve func(net.Conn)) IPCResponse {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	done := make(chan struct{})
	go func() { defer close(done); defer server.Close(); serve(server) }()
	scanner := NewIPCScanner(client)
	if !scanner.Scan() {
		t.Fatalf("no response: %v", scanner.Err())
	}
	var response IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	<-done
	return response
}

func TestPostIdentity_ConnectionRunKeyChangesAcrossSimulatedRestart(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires Unix peer credentials")
	}
	dir, err := os.MkdirTemp("/tmp", "post-restart-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	now := time.Now()
	pane := &Pane{name: "eng3", pid: os.Getpid(), alive: true, startedAt: now}
	app := &TUI{panes: []PaneView{pane}}
	before, err := app.identifyPostConnection(server)
	if err != nil {
		t.Fatal(err)
	}
	first := app.applyPostRequest(IPCRequest{Action: "post", Text: "question?"}, before, now)
	if !first.OK || len(first.Notices) != 1 {
		t.Fatalf("first-run teaching: %+v", first)
	}
	again, err := app.identifyPostConnection(server)
	if err != nil || again.runKey != before.runKey {
		t.Fatalf("same process changed run: %+v %v", again, err)
	}
	second := app.applyPostRequest(IPCRequest{Action: "post", Text: "another question?"}, again, now)
	if !second.OK || len(second.Notices) != 0 {
		t.Fatalf("same run retaught: %+v", second)
	}
	// Simulate replacement, including PID reuse: the restarted pane has a new
	// process start instant. Exercise the production connection resolver again.
	app.panes = []PaneView{&Pane{name: "eng3", pid: os.Getpid(), alive: true, startedAt: now.Add(time.Second)}}
	after, err := app.identifyPostConnection(server)
	if err != nil || before.runKey == after.runKey || before.agent != after.agent {
		t.Fatalf("restart identity: %+v -> %+v (%v)", before, after, err)
	}
	third := app.applyPostRequest(IPCRequest{Action: "post", Text: "third question?"}, after, now)
	if !third.OK || len(third.Notices) != 1 {
		t.Fatalf("new run did not teach: %+v", third)
	}
	mine := app.applyPostRequest(IPCRequest{Action: "post_mine"}, after, now)
	if !mine.OK || len(strings.Split(mine.Data, "\n")) != 3 {
		t.Fatalf("restart lost persistent ownership: %+v", mine)
	}
}
