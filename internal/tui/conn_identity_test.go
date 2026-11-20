package tui

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestConnIdentity_AnAncestorPaneIsFound(t *testing.T) {
	pane := connIdentity{agent: "eng3"}
	parent := func(pid int) (int, error) { return map[int]int{200: 100, 100: 42}[pid], nil }
	got, err := identifyAncestor(200, []connPaneProcess{{42, pane}}, parent)
	if err != nil || got != pane {
		t.Fatalf("identity = %+v, %v", got, err)
	}
	// A different CLI process inside the same pane is the same agent.
	got, err = identifyAncestor(100, []connPaneProcess{{42, pane}}, parent)
	if err != nil || got != pane {
		t.Fatalf("second command = %+v, %v", got, err)
	}
}

func TestConnIdentity_UnrelatedAndCyclicProcessesRefused(t *testing.T) {
	t.Setenv("INITECH_AGENT", "eng3")
	for _, parent := range []func(int) (int, error){
		func(int) (int, error) { return 1, nil },
		func(pid int) (int, error) { return pid, nil },
	} {
		_, err := identifyAncestor(200, []connPaneProcess{{42, connIdentity{"eng3"}}}, parent)
		if err == nil || !strings.Contains(err.Error(), "could not identify your pane") {
			t.Fatalf("identity refusal = %v", err)
		}
	}
}

func TestConnIdentity_WindowsTCPRefusal(t *testing.T) {
	// Independent spec copy: changing the production constant must fail this AC.
	const want = "this command is not available on Windows in this release: initech cannot tell which agent's pane a call comes from over this transport"
	if connWindowsRefusal != want {
		t.Errorf("Windows refusal constant = %q, want %q", connWindowsRefusal, want)
	}
	err := connPlatformError("windows")
	if err == nil || err.Error() != want {
		t.Fatalf("Windows refusal = %v", err)
	}
}

// A real test subprocess, inside a real pane shell, is the caller. This
// measures peer credentials + ancestry on the actual transport without a live
// fleet or an AI harness; the agent is the pane's, whatever the caller's
// environment says (INITECH_AGENT=super here).
func TestConnIdentity_RealPaneConnectionIgnoresForgedEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires supported Unix peer credentials and a PTY")
	}
	dir, err := os.MkdirTemp("/tmp", "conn-peer-")
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
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second)) // a hang guard
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPane(PaneConfig{Name: "eng3", AgentType: "generic", Command: []string{"/bin/sh", "-c", `"$1" -test.run=^TestConnIdentityProcessHelper$; sleep 1`, "conn-helper", executable}, Env: []string{"INITECH_CONN_TEST_SOCKET=" + endpoint, "INITECH_AGENT=super"}}, 24, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Start()
	app := &TUI{panes: []PaneView{p}}
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	scanner := NewIPCScanner(conn)
	if !scanner.Scan() || scanner.Text() != "hello" {
		t.Fatalf("helper did not call: %v", scanner.Err())
	}
	identity, err := app.identifyConnection(conn)
	if err != nil || identity.agent != "eng3" {
		t.Fatalf("peer attribution = %+v, %v; want eng3 (the pane), not the caller's INITECH_AGENT", identity, err)
	}
	if _, err := conn.Write([]byte("checked\n")); err != nil {
		t.Fatal(err)
	}
	t.Logf("tier 1 measured: %s Unix peer -> pane PID %d -> agent %s", runtime.GOOS, p.pid, identity.agent)
}

func TestConnIdentityProcessHelper(t *testing.T) {
	endpoint := os.Getenv("INITECH_CONN_TEST_SOCKET")
	if endpoint == "" {
		return
	}
	conn, err := net.Dial("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() || scanner.Text() != "checked" {
		t.Fatalf("no answer: %v", scanner.Err())
	}
}
