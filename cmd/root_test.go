package cmd

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

func TestExecute_SuccessDoesNotExit(t *testing.T) {
	restoreExecute := stubExecuteRoot(t, func() error { return nil })
	defer restoreExecute()

	var exited bool
	restoreExit := stubExitRoot(t, func(code int) { exited = true })
	defer restoreExit()

	restoreStderr, stderr := captureStderr(t)
	Execute()
	restoreStderr()

	if exited {
		t.Fatal("Execute should not exit on success")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestExecute_ErrorPrintsAndExits(t *testing.T) {
	restoreExecute := stubExecuteRoot(t, func() error { return fmt.Errorf("boom") })
	defer restoreExecute()

	var exitCode int
	restoreExit := stubExitRoot(t, func(code int) { exitCode = code })
	defer restoreExit()

	restoreStderr, stderr := captureStderr(t)
	Execute()
	restoreStderr()

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("stderr = %q, want boom", stderr.String())
	}
}

func TestRunTUI_MissingConfig(t *testing.T) {
	restoreWD := chdirForTest(t, t.TempDir())
	defer restoreWD()

	err := runTUI(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no initech.yaml found") {
		t.Fatalf("runTUI missing-config error = %v", err)
	}
}

func TestRunTUI_NoValidRoleDirectories(t *testing.T) {
	skipWindows(t)
	dir := shortProjectDir(t)
	cfg := &config.Project{
		Name:  "demo",
		Root:  dir,
		Roles: []string{"eng1"},
	}
	if err := config.Write(filepath.Join(dir, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	err := runTUI(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no valid role directories found") {
		t.Fatalf("runTUI no-valid-role error = %v", err)
	}
}

func TestRunTUI_PassesConfigToTUI(t *testing.T) {
	skipWindows(t)
	dir := shortProjectDir(t)
	mustWriteFile(t, filepath.Join(dir, "eng1", "CLAUDE.md"), "# eng1")
	cfg := &config.Project{
		Name: "demo",
		Root: dir,
		Roles: []string{"eng1"},
		Resource: config.ResourceConfig{
			AutoSuspend:       true,
			PressureThreshold: 91,
		},
		Beads: config.BeadsConfig{Prefix: "dem"},
	}
	if err := config.Write(filepath.Join(dir, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	t.Setenv("INITECH_MOCK_AGENT", "/bin/echo")
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var gotCfg tui.Config
	restoreTUIRun := stubTUIRun(t, func(cfg tui.Config) error {
		gotCfg = cfg
		return nil
	})
	defer restoreTUIRun()

	resetLayout = true
	verbose = true
	autoSuspend = false
	pprofAddr = ""
	cmd := &cobra.Command{}

	if err := runTUI(cmd, nil); err != nil {
		t.Fatalf("runTUI: %v", err)
	}

	if gotCfg.ProjectName != "demo" || gotCfg.ProjectRoot != dir {
		t.Fatalf("tui config project = (%q,%q), want (demo,%s)", gotCfg.ProjectName, gotCfg.ProjectRoot, dir)
	}
	if len(gotCfg.Agents) != 1 || gotCfg.Agents[0].Name != "eng1" {
		t.Fatalf("tui config agents = %#v, want eng1", gotCfg.Agents)
	}
	if gotCfg.Agents[0].Dir != filepath.Join(dir, "eng1") {
		t.Fatalf("agent dir = %q, want %q", gotCfg.Agents[0].Dir, filepath.Join(dir, "eng1"))
	}
	if len(gotCfg.Agents[0].Command) != 1 || gotCfg.Agents[0].Command[0] != "/bin/echo" {
		t.Fatalf("agent command = %#v, want [/bin/echo]", gotCfg.Agents[0].Command)
	}
	if !gotCfg.ResetLayout || !gotCfg.Verbose {
		t.Fatalf("reset/verbose = (%v,%v), want true/true", gotCfg.ResetLayout, gotCfg.Verbose)
	}
	if !gotCfg.AutoSuspend || gotCfg.PressureThreshold != 91 {
		t.Fatalf("autoSuspend/threshold = (%v,%d), want (true,91)", gotCfg.AutoSuspend, gotCfg.PressureThreshold)
	}
	if gotCfg.Project == nil || gotCfg.Project.Name != "demo" {
		t.Fatalf("project = %#v, want loaded project", gotCfg.Project)
	}
	if gotCfg.PaneConfigBuilder == nil {
		t.Fatal("PaneConfigBuilder should be set")
	}
}

func TestRunTUI_AutoSuspendFlagOverridesConfig(t *testing.T) {
	skipWindows(t)
	dir := shortProjectDir(t)
	mustWriteFile(t, filepath.Join(dir, "eng1", "CLAUDE.md"), "# eng1")
	cfg := &config.Project{
		Name: "demo",
		Root: dir,
		Roles: []string{"eng1"},
		Resource: config.ResourceConfig{
			AutoSuspend: false,
		},
	}
	if err := config.Write(filepath.Join(dir, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}
	t.Setenv("INITECH_MOCK_AGENT", "/bin/echo")
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var gotCfg tui.Config
	restoreTUIRun := stubTUIRun(t, func(cfg tui.Config) error {
		gotCfg = cfg
		return nil
	})
	defer restoreTUIRun()

	autoSuspend = true
	cmd := &cobra.Command{}
	cmd.Flags().Bool("auto-suspend", false, "")
	if err := cmd.Flags().Set("auto-suspend", "true"); err != nil {
		t.Fatalf("Set auto-suspend flag: %v", err)
	}

	if err := runTUI(cmd, nil); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	if !gotCfg.AutoSuspend {
		t.Fatal("CLI auto-suspend flag should override config false")
	}
}

func TestRunTUI_RejectsNonLocalhostPprof(t *testing.T) {
	skipWindows(t)
	dir := shortProjectDir(t)
	mustWriteFile(t, filepath.Join(dir, "eng1", "CLAUDE.md"), "# eng1")
	cfg := &config.Project{Name: "demo", Root: dir, Roles: []string{"eng1"}}
	if err := config.Write(filepath.Join(dir, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	t.Setenv("INITECH_MOCK_AGENT", "/bin/echo")
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	restoreTUIRun := stubTUIRun(t, func(cfg tui.Config) error {
		t.Fatal("tuiRun should not be called when pprof address is rejected")
		return nil
	})
	defer restoreTUIRun()

	pprofAddr = "0.0.0.0:6060"
	defer func() { pprofAddr = "" }()

	err := runTUI(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to bind to non-localhost address") {
		t.Fatalf("runTUI pprof security error = %v", err)
	}
}

func TestRunTUI_UsesPprofListenerWhenConfigured(t *testing.T) {
	skipWindows(t)
	dir := shortProjectDir(t)
	mustWriteFile(t, filepath.Join(dir, "eng1", "CLAUDE.md"), "# eng1")
	cfg := &config.Project{Name: "demo", Root: dir, Roles: []string{"eng1"}}
	if err := config.Write(filepath.Join(dir, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	t.Setenv("INITECH_MOCK_AGENT", "/bin/echo")
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	restoreTUIRun := stubTUIRun(t, func(cfg tui.Config) error { return nil })
	defer restoreTUIRun()

	var listenedAddr string
	restoreListen := stubListenTCP(t, func(network, addr string) (net.Listener, error) {
		listenedAddr = addr
		return &fakeListener{addr: fakeAddr(addr)}, nil
	})
	defer restoreListen()

	served := make(chan struct{}, 1)
	restoreServe := stubServeHTTP(t, func(l net.Listener, h http.Handler) error {
		served <- struct{}{}
		return nil
	})
	defer restoreServe()

	restoreStderr, stderr := captureStderr(t)

	pprofAddr = "localhost:6060"
	defer func() { pprofAddr = "" }()

	if err := runTUI(&cobra.Command{}, nil); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	restoreStderr()
	if listenedAddr != "localhost:6060" {
		t.Fatalf("pprof listen addr = %q, want localhost:6060", listenedAddr)
	}
	if !strings.Contains(stderr.String(), "pprof server listening on http://localhost:6060/debug/pprof") {
		t.Fatalf("stderr = %q, want pprof message", stderr.String())
	}
	select {
	case <-served:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("serveHTTP should be invoked")
	}
}

type fakeListener struct {
	addr net.Addr
}

func (l *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *fakeListener) Close() error              { return nil }
func (l *fakeListener) Addr() net.Addr            { return l.addr }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func stubExecuteRoot(t *testing.T, fn func() error) func() {
	t.Helper()
	orig := executeRoot
	executeRoot = fn
	return func() { executeRoot = orig }
}

func stubExitRoot(t *testing.T, fn func(int)) func() {
	t.Helper()
	orig := exitRoot
	exitRoot = fn
	return func() { exitRoot = orig }
}

func stubTUIRun(t *testing.T, fn func(tui.Config) error) func() {
	t.Helper()
	orig := tuiRun
	tuiRun = fn
	return func() { tuiRun = orig }
}

func stubListenTCP(t *testing.T, fn func(string, string) (net.Listener, error)) func() {
	t.Helper()
	orig := listenTCP
	listenTCP = fn
	return func() { listenTCP = orig }
}

func stubServeHTTP(t *testing.T, fn func(net.Listener, http.Handler) error) func() {
	t.Helper()
	orig := serveHTTP
	serveHTTP = fn
	return func() { serveHTTP = orig }
}

func captureStderr(t *testing.T) (func(), *bytes.Buffer) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = buf.ReadFrom(r)
	}()
	return func() {
		_ = w.Close()
		os.Stderr = orig
		<-done
		_ = r.Close()
	}, &buf
}
