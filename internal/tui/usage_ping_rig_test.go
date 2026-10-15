//go:build !windows

// usage_ping_rig_test.go is ini-62tn's reachability proof: the REAL initech
// binary, started under a PTY in a scratch project with HOME pointed at a temp
// dir, sends its usage ping to a local fake endpoint (never PostHog) after the
// first frame, exactly once; a second start inside 24 hours sends nothing; and
// telemetry: false or DO_NOT_TRACK=1 sends nothing and creates no id file.
//
// Gated behind INITECH_CENSUS=1 like the other real-binary rigs, and skipped
// under -short by buildInitechBinary.
// Run: INITECH_CENSUS=1 go test ./internal/tui/ -run TestUsagePingRig -v -count=1 -timeout 180s
package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type usageRigEndpoint struct {
	mu     sync.Mutex
	bodies [][]byte
	srv    *httptest.Server
}

func newUsageRigEndpoint(t *testing.T) *usageRigEndpoint {
	e := &usageRigEndpoint{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.bodies = append(e.bodies, b)
		e.mu.Unlock()
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *usageRigEndpoint) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.bodies)
}

// usageRigProject makes a one-agent scratch project and returns its root.
func usageRigProject(t *testing.T, extraYAML string) string {
	t.Helper()
	root, err := os.MkdirTemp("", "ini-up-") // short: the IPC socket path has a ~104 char limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if err := os.MkdirAll(filepath.Join(root, "eng1"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "project: usagerig\nroot: " + root + "\nroles: [eng1]\n" + extraYAML
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// runUsageRig starts bin in root under a PTY, waits until the event loop is
// entered plus settle, then kills it. extraEnv entries override os.Environ.
func runUsageRig(t *testing.T, bin, root string, settle time.Duration, extraEnv ...string) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = root
	drop := map[string]bool{"INITECH_SOCKET": true, "INITECH_AGENT": true, "HOME": true, "DO_NOT_TRACK": true, "INITECH_USAGE_ENDPOINT": true, "INITECH_MOCK_AGENT": true}
	var env []string
	for _, e := range os.Environ() {
		if k, _, _ := strings.Cut(e, "="); !drop[k] {
			env = append(env, e)
		}
	}
	cmd.Env = append(append(env, "TERM=xterm-256color", "INITECH_MOCK_AGENT=/bin/cat"), extraEnv...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start under pty: %v", err)
	}
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, ptmx); close(done) }()
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		ptmx.Close()
		<-done
	}()
	logPath := filepath.Join(root, ".initech", "initech.log")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(logPath); strings.Contains(string(b), "entering event loop") {
			time.Sleep(settle)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(logPath)
	t.Fatalf("TUI never entered its event loop. Log:\n%s", b)
}

func TestUsagePingRig_OneRequestThenNoneWithin24h(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the usage ping rig (builds and runs the real binary)")
	}
	bin := buildInitechBinary(t)
	ep := newUsageRigEndpoint(t)
	home := t.TempDir()
	root := usageRigProject(t, "")
	env := []string{"HOME=" + home, "INITECH_USAGE_ENDPOINT=" + ep.srv.URL}

	runUsageRig(t, bin, root, 2*time.Second, env...)
	if n := ep.count(); n != 1 {
		t.Fatalf("first start: endpoint got %d requests, want 1", n)
	}
	ep.mu.Lock()
	body := ep.bodies[0]
	ep.mu.Unlock()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, body)
	}
	var top, props []string
	for k := range got {
		top = append(top, k)
	}
	for k := range got["properties"].(map[string]any) {
		props = append(props, k)
	}
	sort.Strings(top)
	sort.Strings(props)
	if strings.Join(top, ",") != "api_key,distinct_id,event,properties,timestamp" ||
		strings.Join(props, ",") != "$geoip_disable,arch,os,version" || got["event"] != "usage_ping" {
		t.Fatalf("payload shape changed: %s", body)
	}
	dir := filepath.Join(home, ".config", "initech")
	st, err := os.Stat(filepath.Join(dir, "install_id"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("install_id: err=%v mode=%v, want 0600", err, st)
	}
	id, _ := os.ReadFile(filepath.Join(dir, "install_id"))
	if got["distinct_id"] != strings.TrimSpace(string(id)) {
		t.Fatalf("distinct_id %v is not the stored install id %q", got["distinct_id"], id)
	}
	if _, err := os.Stat(filepath.Join(dir, "last_ping")); err != nil {
		t.Fatalf("last_ping: %v", err)
	}

	runUsageRig(t, bin, root, 2*time.Second, env...)
	if n := ep.count(); n != 1 {
		t.Fatalf("second start within 24h: endpoint total %d requests, want still 1", n)
	}
}

func TestUsagePingRig_OffSwitchesSendNothingAndWriteNoID(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the usage ping rig (builds and runs the real binary)")
	}
	bin := buildInitechBinary(t)
	for _, c := range []struct {
		name, yaml string
		env        []string
	}{
		{"telemetry false", "telemetry: false\n", nil},
		{"DO_NOT_TRACK=1", "", []string{"DO_NOT_TRACK=1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ep := newUsageRigEndpoint(t)
			home := t.TempDir()
			root := usageRigProject(t, c.yaml)
			runUsageRig(t, bin, root, 2*time.Second, append([]string{"HOME=" + home, "INITECH_USAGE_ENDPOINT=" + ep.srv.URL}, c.env...)...)
			if n := ep.count(); n != 0 {
				t.Fatalf("endpoint got %d requests, want 0", n)
			}
			if _, err := os.Stat(filepath.Join(home, ".config", "initech", "install_id")); !os.IsNotExist(err) {
				t.Fatalf("install_id exists with the ping off: %v", err)
			}
		})
	}
}

// TestUsagePingRig_UnreachableEndpointIsSilent: a port nobody listens on; the
// TUI still reaches its event loop, and nothing about the ping lands in the
// captured stderr log.
func TestUsagePingRig_UnreachableEndpointIsSilent(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the usage ping rig (builds and runs the real binary)")
	}
	bin := buildInitechBinary(t)
	home := t.TempDir()
	root := usageRigProject(t, "")
	runUsageRig(t, bin, root, 2*time.Second, "HOME="+home, "INITECH_USAGE_ENDPOINT=http://127.0.0.1:1/capture")
	if b, err := os.ReadFile(filepath.Join(root, ".initech", "stderr.log")); err == nil && strings.Contains(strings.ToLower(string(b)), "usage") {
		t.Fatalf("stderr carries the ping failure:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "initech", "last_ping")); !os.IsNotExist(err) {
		t.Fatalf("last_ping written after a failed send: %v", err)
	}
}
