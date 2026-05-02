package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// newTestDaemon returns a Daemon with ownership initialized and a temp project
// root. Suitable for testing configure_agent / stop_agent / restart_agent.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return &Daemon{
		project: &config.Project{
			Name: "test",
			Root: t.TempDir(),
		},
		ringBufs:   make(map[string]*RingBuf),
		multiSinks: make(map[string]*MultiSink),
		ownership:  newAgentOwnership(),
		version:    "test",
	}
}

func TestAgentOwnership_ClaimReleaseVerify(t *testing.T) {
	a := newAgentOwnership()
	cfg := PaneConfig{Name: "eng2", Command: []string{"/bin/sh"}}

	if prev, ok := a.claim("eng2", "alice", cfg); !ok {
		t.Fatalf("expected first claim to succeed, got prev=%q ok=%v", prev, ok)
	}
	if prev, ok := a.claim("eng2", "bob", cfg); ok || prev != "alice" {
		t.Errorf("second claim should fail with prev=alice, got prev=%q ok=%v", prev, ok)
	}
	if owner, ok := a.verify("eng2", "alice"); !ok || owner != "alice" {
		t.Errorf("verify alice: got owner=%q ok=%v, want alice/true", owner, ok)
	}
	if _, ok := a.verify("eng2", "bob"); ok {
		t.Error("verify bob should fail")
	}
	if got, ok := a.config("eng2"); !ok || got.Name != "eng2" {
		t.Errorf("config: got %+v ok=%v, want eng2", got, ok)
	}
	a.release("eng2")
	if _, ok := a.verify("eng2", "alice"); ok {
		t.Error("verify after release should fail")
	}
}

func TestHandleConfigureAgent_CreatesWorkspaceAndPane(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")

	cmd := ConfigureAgentCmd{
		ID:           "req-1",
		Action:       "configure_agent",
		Name:         "eng2",
		Command:      []string{"/bin/sh", "-c", "sleep 30"},
		Dir:          dir,
		ClaudeMD:     "# eng2 instructions\n",
		RootClaudeMD: "# Project root\n",
	}
	line, _ := json.Marshal(cmd)

	resp := d.handleConfigureAgent(line, "alice")

	if !resp.OK {
		t.Fatalf("expected OK, got error %q", resp.Error)
	}
	if resp.Action != "configure_agent_ok" || resp.Target != "eng2" {
		t.Errorf("response = %+v", resp)
	}

	if got, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); !strings.Contains(string(got), "eng2 instructions") {
		t.Errorf("CLAUDE.md not written or wrong content: %q", got)
	}
	rootMD := filepath.Join(d.project.Root, "CLAUDE.md")
	if got, _ := os.ReadFile(rootMD); !strings.Contains(string(got), "Project root") {
		t.Errorf("root CLAUDE.md not written: %q", got)
	}
	if d.findPane("eng2") == nil {
		t.Error("pane not added to daemon")
	}
	if owner, _ := d.ownership.verify("eng2", "alice"); owner != "alice" {
		t.Errorf("ownership not recorded: owner=%q", owner)
	}

	// Cleanup: stop the started process so the test doesn't leak.
	d.removePane("eng2")
}

func TestHandleConfigureAgent_NameRequired(t *testing.T) {
	d := newTestDaemon(t)
	cmd := ConfigureAgentCmd{Action: "configure_agent"}
	line, _ := json.Marshal(cmd)
	resp := d.handleConfigureAgent(line, "alice")
	if resp.OK || !strings.Contains(resp.Error, "name is required") {
		t.Errorf("expected name-required error, got %+v", resp)
	}
}

func TestHandleConfigureAgent_Collision(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")

	first, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Dir: dir,
	})
	if r := d.handleConfigureAgent(first, "alice"); !r.OK {
		t.Fatalf("first push failed: %v", r.Error)
	}
	defer d.removePane("eng2")

	second := first
	r := d.handleConfigureAgent(second, "bob")
	if r.OK || !strings.Contains(r.Error, "already exists") {
		t.Errorf("expected collision error, got %+v", r)
	}
}

func TestHandleConfigureAgent_IdempotentSameOwner(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")

	first, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command:  []string{"/bin/sh", "-c", "sleep 30"},
		Dir:      dir,
		ClaudeMD: "# original\n",
	})
	if r := d.handleConfigureAgent(first, "alice"); !r.OK {
		t.Fatalf("first push failed: %v", r.Error)
	}
	defer d.removePane("eng2")

	// Same owner re-pushes with new CLAUDE.md content. Should succeed
	// without collision and overwrite the file.
	second, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command:  []string{"/bin/sh", "-c", "sleep 30"},
		Dir:      dir,
		ClaudeMD: "# updated\n",
	})
	r := d.handleConfigureAgent(second, "alice")
	if !r.OK {
		t.Fatalf("idempotent re-push should succeed: %v", r.Error)
	}
	if r.Action != "configure_agent_ok" {
		t.Errorf("response action = %q, want configure_agent_ok", r.Action)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(got) != "# updated\n" {
		t.Errorf("CLAUDE.md = %q, want updated content", got)
	}
}

func TestHandleConfigureAgent_DifferentOwnerStillCollides(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")

	first, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Dir: dir,
	})
	if r := d.handleConfigureAgent(first, "alice"); !r.OK {
		t.Fatalf("first push failed: %v", r.Error)
	}
	defer d.removePane("eng2")

	// Bob tries to push the same agent — should still get a collision error.
	r := d.handleConfigureAgent(first, "bob")
	if r.OK {
		t.Error("different-owner push should be rejected")
	}
	if !strings.Contains(r.Error, "already exists") {
		t.Errorf("error = %q, want 'already exists'", r.Error)
	}
}

func TestRefreshClaudeMD_NoWriteIfUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "eng2")
	os.MkdirAll(dir, 0755)
	mdPath := filepath.Join(dir, "CLAUDE.md")
	content := "# stable content\n"
	os.WriteFile(mdPath, []byte(content), 0644)

	// Capture mtime.
	info1, _ := os.Stat(mdPath)
	mtime1 := info1.ModTime()

	// Refresh with identical content — should not rewrite.
	time.Sleep(10 * time.Millisecond)
	err := refreshClaudeMD(ConfigureAgentCmd{Dir: dir, ClaudeMD: content})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	info2, _ := os.Stat(mdPath)
	if !info2.ModTime().Equal(mtime1) {
		t.Error("file should not be rewritten when content is unchanged")
	}
}

func TestRefreshClaudeMD_WritesWhenDifferent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "eng2")
	os.MkdirAll(dir, 0755)
	mdPath := filepath.Join(dir, "CLAUDE.md")
	os.WriteFile(mdPath, []byte("# old\n"), 0644)

	err := refreshClaudeMD(ConfigureAgentCmd{Dir: dir, ClaudeMD: "# new\n"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, _ := os.ReadFile(mdPath)
	if string(got) != "# new\n" {
		t.Errorf("CLAUDE.md = %q, want '# new\\n'", got)
	}
}

func TestHandleStopAgent_OwnershipEnforced(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")
	cfgLine, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Dir: dir,
	})
	if r := d.handleConfigureAgent(cfgLine, "alice"); !r.OK {
		t.Fatalf("configure failed: %v", r.Error)
	}

	stopLine, _ := json.Marshal(StopAgentCmd{Action: "stop_agent", Name: "eng2"})

	// Bob is not the owner — should fail.
	if r := d.handleStopAgent(stopLine, "bob"); r.OK || !strings.Contains(r.Error, "owned by") {
		t.Errorf("non-owner stop should fail with ownership error, got %+v", r)
	}

	// Alice owns it — should succeed.
	r := d.handleStopAgent(stopLine, "alice")
	if !r.OK || r.Action != "stop_agent_ok" {
		t.Fatalf("owner stop failed: %+v", r)
	}
	if d.findPane("eng2") != nil {
		t.Error("pane should be removed after stop")
	}
	// Workspace files preserved.
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		// CLAUDE.md was written if ClaudeMD was set; here it wasn't, so just
		// check the directory still exists.
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("workspace dir should be preserved: %v", err)
	}
}

func TestHandleStopAgent_NotFound(t *testing.T) {
	d := newTestDaemon(t)
	line, _ := json.Marshal(StopAgentCmd{Action: "stop_agent", Name: "ghost"})
	if r := d.handleStopAgent(line, "alice"); r.OK || !strings.Contains(r.Error, "not found") {
		t.Errorf("expected not-found, got %+v", r)
	}
}

func TestHandleStopAgent_NameRequired(t *testing.T) {
	d := newTestDaemon(t)
	line, _ := json.Marshal(StopAgentCmd{Action: "stop_agent"})
	if r := d.handleStopAgent(line, "alice"); r.OK || !strings.Contains(r.Error, "name is required") {
		t.Errorf("expected name-required, got %+v", r)
	}
}

func TestHandleRestartAgent_RecreatesWithSameConfig(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")
	origCmd := []string{"/bin/sh", "-c", "sleep 30"}

	cfgLine, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command: origCmd, Dir: dir,
		Env: []string{"FOO=bar"},
	})
	if r := d.handleConfigureAgent(cfgLine, "alice"); !r.OK {
		t.Fatalf("configure failed: %v", r.Error)
	}
	defer d.removePane("eng2")

	// Capture original PID.
	origPane := d.findPane("eng2")
	if origPane == nil {
		t.Fatal("no pane after configure")
	}
	origPID := origPane.pid

	restartLine, _ := json.Marshal(RestartAgentCmd{Action: "restart_agent", Name: "eng2"})
	r := d.handleRestartAgent(restartLine, "alice")
	if !r.OK || r.Action != "restart_agent_ok" {
		t.Fatalf("restart failed: %+v", r)
	}

	// Brief wait for the new process to exist.
	time.Sleep(50 * time.Millisecond)

	newPane := d.findPane("eng2")
	if newPane == nil {
		t.Fatal("no pane after restart")
	}
	if newPane.pid == origPID {
		t.Errorf("pid should change after restart: still %d", origPID)
	}
	if !equalSlice(newPane.cfg.Command, origCmd) {
		t.Errorf("command not preserved: got %v, want %v", newPane.cfg.Command, origCmd)
	}
}

func TestHandleRestartAgent_OwnershipEnforced(t *testing.T) {
	d := newTestDaemon(t)
	dir := filepath.Join(d.project.Root, "eng2")
	cfgLine, _ := json.Marshal(ConfigureAgentCmd{
		Action: "configure_agent", Name: "eng2",
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Dir: dir,
	})
	if r := d.handleConfigureAgent(cfgLine, "alice"); !r.OK {
		t.Fatalf("configure failed: %v", r.Error)
	}
	defer d.removePane("eng2")

	line, _ := json.Marshal(RestartAgentCmd{Action: "restart_agent", Name: "eng2"})
	if r := d.handleRestartAgent(line, "bob"); r.OK || !strings.Contains(r.Error, "owned by") {
		t.Errorf("non-owner restart should fail, got %+v", r)
	}
}

func TestHandleRestartAgent_NotFound(t *testing.T) {
	d := newTestDaemon(t)
	line, _ := json.Marshal(RestartAgentCmd{Action: "restart_agent", Name: "ghost"})
	if r := d.handleRestartAgent(line, "alice"); r.OK || !strings.Contains(r.Error, "not found") {
		t.Errorf("expected not-found, got %+v", r)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
