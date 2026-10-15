package usage

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeEndpoint records every request body it receives.
type fakeEndpoint struct {
	mu     sync.Mutex
	bodies [][]byte
	srv    *httptest.Server
}

func newFakeEndpoint(t *testing.T) *fakeEndpoint {
	t.Helper()
	f := &fakeEndpoint{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, b)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEndpoint) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeEndpoint) body(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func envWith(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestEnabled_ConfigAndDoNotTrack(t *testing.T) {
	no, yes := false, true
	cases := []struct {
		name      string
		telemetry *bool
		dnt       string
		want      bool
	}{
		{"absent key, no env: on", nil, "", true},
		{"telemetry true: on", &yes, "", true},
		{"telemetry false: off", &no, "", false},
		{"DO_NOT_TRACK=1: off", nil, "1", false},
		{"DO_NOT_TRACK=true: off", nil, "true", false},
		{"DO_NOT_TRACK wins over telemetry true", &yes, "1", false},
		{"DO_NOT_TRACK=0: on", nil, "0", true},
		{"DO_NOT_TRACK=false: on", nil, "false", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Enabled(c.telemetry, envWith(map[string]string{"DO_NOT_TRACK": c.dnt}))
			if got != c.want {
				t.Fatalf("Enabled = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPing_FirstStart_SendsOneEventAndStoresID is the feed-it-nothing case:
// a fresh config dir, no last ping. One request, an id file with mode 0600,
// and a last_ping file beside it.
func TestPing_FirstStart_SendsOneEventAndStoresID(t *testing.T) {
	f := newFakeEndpoint(t)
	dir := filepath.Join(t.TempDir(), "initech")
	if err := Ping(Options{Version: "v9.9.9", Endpoint: f.srv.URL, Dir: dir}); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if f.count() != 1 {
		t.Fatalf("requests = %d, want 1", f.count())
	}
	st, err := os.Stat(filepath.Join(dir, idFile))
	if err != nil {
		t.Fatalf("install id file: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("install id mode = %o, want 600", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, lastPingFile)); err != nil {
		t.Fatalf("last_ping file: %v", err)
	}
}

// TestPing_GoldenPayload pins every key that leaves the machine. Adding,
// renaming or dropping a property fails here: that change needs its own
// operator yes.
func TestPing_GoldenPayload(t *testing.T) {
	f := newFakeEndpoint(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, idFile), []byte("0123456789abcdef0123456789abcdef-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if err := Ping(Options{Version: "v2.30.0", Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(now)}); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(f.body(0), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	want := map[string]any{
		"api_key":     posthogAPIKey,
		"event":       "usage_ping",
		"distinct_id": "0123456789abcdef0123456789abcdef-id",
		"timestamp":   "2026-10-15T12:00:00Z",
		"properties": map[string]any{
			"version":        "v2.30.0",
			"os":             runtime.GOOS,
			"arch":           runtime.GOARCH,
			"$geoip_disable": true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload changed.\n got: %s\nwant: %v", f.body(0), want)
	}
	// Positive twin of the exact-equality check: the property names are
	// listed so a diff names the key that moved.
	props := got["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"$geoip_disable", "arch", "os", "version"}) {
		t.Fatalf("property keys = %v", keys)
	}
}

func TestPing_SecondStartWithin24h_SendsNothing(t *testing.T) {
	f := newFakeEndpoint(t)
	dir := t.TempDir()
	start := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	if err := Ping(Options{Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(start)}); err != nil {
		t.Fatalf("first Ping: %v", err)
	}
	err := Ping(Options{Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(start.Add(23*time.Hour + 59*time.Minute))})
	if err != ErrTooSoon {
		t.Fatalf("second Ping err = %v, want ErrTooSoon", err)
	}
	if f.count() != 1 {
		t.Fatalf("requests = %d, want 1", f.count())
	}
	// Positive twin: past 24 hours the gate opens again.
	if err := Ping(Options{Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(start.Add(24 * time.Hour))}); err != nil {
		t.Fatalf("third Ping: %v", err)
	}
	if f.count() != 2 {
		t.Fatalf("requests after 24h = %d, want 2", f.count())
	}
}

func TestPing_LastPingInTheFuture_DoesNotSilenceThePing(t *testing.T) {
	f := newFakeEndpoint(t)
	dir := t.TempDir()
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(dir, lastPingFile), []byte(now.Add(30*24*time.Hour).Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ping(Options{Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(now)}); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if f.count() != 1 {
		t.Fatalf("requests = %d, want 1", f.count())
	}
}

// TestPing_FailedSend_DoesNotStartTheClock: a send that fails leaves no
// last_ping, so the next start tries again instead of waiting a day.
func TestPing_FailedSend_DoesNotStartTheClock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := Ping(Options{Endpoint: srv.URL, Dir: dir}); err == nil {
		t.Fatal("Ping against a 503 returned nil")
	}
	if _, err := os.Stat(filepath.Join(dir, lastPingFile)); !os.IsNotExist(err) {
		t.Fatalf("last_ping after a failed send: err = %v, want not-exist", err)
	}
}

// TestPing_UnreachableEndpoint_IsSilentAndBounded: nothing on stdout or
// stderr, an error returned to the caller (who drops it), and the call ends
// well inside the 3 second timeout because the port refuses.
func TestPing_UnreachableEndpoint_IsSilentAndBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nobody listens here now

	out := captureStdio(t, func() {
		start := time.Now()
		if err := Ping(Options{Endpoint: "http://" + addr + "/capture", Dir: t.TempDir()}); err == nil {
			t.Error("Ping to a closed port returned nil")
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("Ping took %v against a closed port", d)
		}
	})
	if out != "" {
		t.Fatalf("Ping wrote to stdout/stderr: %q", out)
	}
}

// TestPing_HangingEndpoint_GivesUpAtTheTimeout bounds the worst case: an
// endpoint that accepts and never answers costs one client timeout.
func TestPing_HangingEndpoint_GivesUpAtTheTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	err := Ping(Options{Endpoint: srv.URL, Dir: t.TempDir(), Client: &http.Client{Timeout: 200 * time.Millisecond}})
	if err == nil {
		t.Fatal("Ping to a hanging endpoint returned nil")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Ping took %v; the client timeout was 200ms", d)
	}
}

func TestPing_DebugLineOnFailureOnly(t *testing.T) {
	var lines []string
	debug := func(msg string, args ...any) { lines = append(lines, msg) }
	f := newFakeEndpoint(t)
	if err := Ping(Options{Endpoint: f.srv.URL, Dir: t.TempDir(), Debug: debug}); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("debug lines on success: %v", lines)
	}
	_ = Ping(Options{Endpoint: "http://127.0.0.1:1/capture", Dir: t.TempDir(), Debug: debug})
	if len(lines) != 1 {
		t.Fatalf("debug lines on failure = %d, want 1", len(lines))
	}
}

// TestPing_RealEndpointRefusedUnderTest is the structural guard: no test
// anywhere can reach PostHog through this package, even by forgetting to set
// an endpoint. The id file is not created either.
//
// The client's transport refuses every request and records it, so even with
// the guard broken (the mutation that proves this test) nothing reaches the
// network: the test fails on the recorded attempt instead.
func TestPing_RealEndpointRefusedUnderTest(t *testing.T) {
	t.Setenv(EndpointEnv, "")
	dir := t.TempDir()
	var attempts int
	refuse := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("test transport: no network")
	})
	err := Ping(Options{Dir: dir, Client: &http.Client{Transport: refuse}})
	if attempts != 0 {
		t.Fatalf("a request to %s was attempted from a test", DefaultEndpoint)
	}
	if err != errRealEndpointInTest {
		t.Fatalf("err = %v, want errRealEndpointInTest", err)
	}
	if _, err := os.Stat(filepath.Join(dir, idFile)); !os.IsNotExist(err) {
		t.Fatalf("id file created on a refused send: %v", err)
	}
}

func TestPing_EndpointEnvOverride(t *testing.T) {
	f := newFakeEndpoint(t)
	t.Setenv(EndpointEnv, f.srv.URL)
	if err := Ping(Options{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if f.count() != 1 {
		t.Fatalf("requests = %d, want 1", f.count())
	}
}

func TestPing_ReusesStoredID(t *testing.T) {
	f := newFakeEndpoint(t)
	dir := t.TempDir()
	t0 := time.Now()
	_ = Ping(Options{Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(t0)})
	_ = Ping(Options{Endpoint: f.srv.URL, Dir: dir, Now: fixedClock(t0.Add(25 * time.Hour))})
	if f.count() != 2 {
		t.Fatalf("requests = %d, want 2", f.count())
	}
	var a, b payload
	_ = json.Unmarshal(f.body(0), &a)
	_ = json.Unmarshal(f.body(1), &b)
	if a.DistinctID == "" || a.DistinctID != b.DistinctID {
		t.Fatalf("install id changed between pings: %q vs %q", a.DistinctID, b.DistinctID)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// captureStdio runs fn with os.Stdout and os.Stderr redirected to a pipe and
// returns what was written.
func captureStdio(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	w.Close()
	return <-done
}
