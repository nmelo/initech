package tui

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ini-u2hy: every agent pane carries BEADS_ACTOR=<role> beside
// INITECH_AGENT=<role>, so bd records the agent as the actor of a bead
// change. Checked by effect: a real child writes its environment to a file,
// and the parent test process has BEADS_ACTOR=someone set, which the role
// must override.

// envDumpCommand is a child that writes its environment to path and stays up.
func envDumpCommand(path string) []string {
	return []string{"/bin/sh", "-c", "env > " + path + ".tmp && mv " + path + ".tmp " + path + "; echo ready; sleep 30"}
}

// childEnv waits (bounded) for the child's environment dump and parses it.
func childEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			env := map[string]string{}
			for _, line := range strings.Split(string(data), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					env[k] = v
				}
			}
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never wrote its environment to %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireRole(t *testing.T, env map[string]string, role string) {
	t.Helper()
	if env["BEADS_ACTOR"] != role {
		t.Errorf("child BEADS_ACTOR = %q, want %q", env["BEADS_ACTOR"], role)
	}
	if env["INITECH_AGENT"] != role {
		t.Errorf("child INITECH_AGENT = %q, want %q", env["INITECH_AGENT"], role)
	}
}

func skipUnlessRealChild(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a real process; runs in the full suite")
	}
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
}

// envBuilder is a paneConfigBuilder whose panes dump their environment to
// dir/<name>.env.
func envBuilder(dir string) func(string) (PaneConfig, error) {
	return func(name string) (PaneConfig, error) {
		return PaneConfig{Name: name, Dir: dir, AgentType: "generic", Command: envDumpCommand(filepath.Join(dir, name+".env"))}, nil
	}
}

func TestAgentEnv_AddedPaneHasItsRoleAsBeadsActor(t *testing.T) {
	skipUnlessRealChild(t)
	t.Setenv("BEADS_ACTOR", "someone")
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng3"), 0755)
	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1")}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
		agentEvents: make(chan AgentEvent, 8),
		sockPath:    "/tmp/test-u2hy.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			cfg, err := envBuilder(dir)(name)
			cfg.Dir = filepath.Join(dir, name)
			return cfg, err
		},
	}
	if err := tui.addPane("eng3"); err != nil {
		t.Fatalf("addPane: %v", err)
	}
	t.Cleanup(func() { tui.panes[1].Close() })
	requireRole(t, childEnv(t, filepath.Join(dir, "eng3.env")), "eng3")
}

func TestAgentEnv_RestartedPaneHasItsRoleAsBeadsActor(t *testing.T) {
	skipUnlessRealChild(t)
	t.Setenv("BEADS_ACTOR", "someone")
	dir := t.TempDir()
	old := newEmuPane("eng3", 80, 24)
	tui := &TUI{
		panes:             toPaneViews([]*Pane{old}),
		layoutState:       DefaultLayoutState([]string{"eng3"}),
		agentEvents:       make(chan AgentEvent, 8),
		sockPath:          "/tmp/test-u2hy.sock",
		paneConfigBuilder: envBuilder(dir),
	}
	server, client := net.Pipe()
	go func() { io.Copy(io.Discard, bufio.NewReader(client)) }()
	t.Cleanup(func() { server.Close(); client.Close() })
	tui.handleIPCRestart(server, IPCRequest{Action: "restart", Target: "eng3"})
	t.Cleanup(func() { tui.panes[0].Close() })
	requireRole(t, childEnv(t, filepath.Join(dir, "eng3.env")), "eng3")
}

func TestAgentEnv_WokenPaneHasItsRoleAsBeadsActor(t *testing.T) {
	skipUnlessRealChild(t)
	t.Setenv("BEADS_ACTOR", "someone")
	dir := t.TempDir()
	p := newEmuPane("eng3", 80, 24)
	p.suspended = true
	tui := &TUI{
		panes:             toPaneViews([]*Pane{p}),
		quitCh:            make(chan struct{}),
		agentEvents:       make(chan AgentEvent, 64),
		sockPath:          "/tmp/test-u2hy.sock",
		paneConfigBuilder: envBuilder(dir),
	}
	if err := tui.resumePane(p, "test"); err != nil {
		t.Fatalf("resumePane: %v", err)
	}
	t.Cleanup(func() { tui.panes[0].Close() })
	requireRole(t, childEnv(t, filepath.Join(dir, "eng3.env")), "eng3")
}

func TestAgentIdentityEnv_NamesTheRoleTwiceAndTheSocketOnlyWhenGiven(t *testing.T) {
	got := strings.Join(agentIdentityEnv("qa2"), " ")
	if got != "INITECH_AGENT=qa2 BEADS_ACTOR=qa2" {
		t.Errorf("without a socket: %q", got)
	}
	got = strings.Join(agentIdentityEnv("qa2", "/s.sock"), " ")
	if got != "INITECH_SOCKET=/s.sock INITECH_AGENT=qa2 BEADS_ACTOR=qa2" {
		t.Errorf("with a socket: %q", got)
	}
}

// TestAgentIdentityEnv_IsTheOnlyPlaceThatSetsAgentIdentity fails when a
// non-test Go file sets INITECH_AGENT= anywhere but agent_env.go, so a new
// pane path cannot set the identity by hand and forget BEADS_ACTOR.
func TestAgentIdentityEnv_IsTheOnlyPlaceThatSetsAgentIdentity(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := map[string]bool{
		filepath.Join("internal", "tui", "agent_env.go"): true,
		// The perf rig filters these variables out of its own env (HasPrefix);
		// it never sets one.
		filepath.Join("tools", "perf", "rig", "main.go"): true,
	}
	var seen int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && (info.Name() == ".git" || info.Name() == "vendor") {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), `"INITECH_AGENT=`) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		seen++
		if !allowed[rel] {
			t.Errorf("%s sets INITECH_AGENT= by hand; call agentIdentityEnv so BEADS_ACTOR comes with it", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Positive twin: the scan reaches the file that is known to match.
	if seen == 0 {
		t.Fatal("the scan found no file setting INITECH_AGENT=; it is not reading the tree")
	}
}
