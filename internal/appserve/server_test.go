package appserve

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend is a Backend over canned rigs, counting every read.
type fakeBackend struct {
	mu          sync.Mutex
	rigs        []Rig
	down        map[string]bool        // rig root -> no TUI answers
	panes       map[string][]PaneState // rig root -> its pane list
	agents      map[string][]map[string]any
	screen      string
	statusCalls int
	paneCalls   int
	screenCalls int
}

func (f *fakeBackend) FindRigs() []Rig { return f.rigs }

func (f *fakeBackend) Panes(r Rig) ([]PaneState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCalls++
	if f.down[r.Root] {
		return nil, errors.New("no TUI")
	}
	return append([]PaneState(nil), f.panes[r.Root]...), nil
}

func (f *fakeBackend) Status(r Rig) ([]map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	var out []map[string]any
	for _, a := range f.agents[r.Root] {
		c := map[string]any{}
		for k, v := range a {
			c[k] = v
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeBackend) Screen(r Rig, pane string, lines int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screenCalls++
	return SplitScreen(f.screen), nil
}

const testToken = "the-device-token"

// newTestServer is a server over b with testToken issued and a clock the
// test moves.
func newTestServer(b Backend, now *time.Time) *Server {
	return New(Config{Backend: b, Digest: sha256.Sum256([]byte(testToken)), HasToken: true,
		Now: func() time.Time { return *now }})
}

func get(t *testing.T, h http.Handler, path, auth string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

// AC1, AC7 (token check): without the token, with a wrong one, with the
// token in another scheme, or before any token is issued, every route is
// refused with 401 and no body; with the token the same route answers.
func TestToken_EveryRouteRefusedWithNoBodyWithoutTheToken(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := &fakeBackend{rigs: []Rig{{Name: "demo", Root: "/r/demo"}}}
	h := newTestServer(b, &now).Handler()
	for _, path := range []string{"/v1/rigs", "/v1/status", "/v1/status/demo", "/v1/nothing"} {
		for _, auth := range []string{"", "Bearer ", "Bearer wrong", "Basic " + testToken, testToken, "Bearer " + testToken + "x"} {
			code, body := get(t, h, path, auth)
			if code != http.StatusUnauthorized || body != "" {
				t.Errorf("%s with %q: %d %q, want 401 and no body", path, auth, code, body)
			}
		}
	}
	if b.statusCalls+b.paneCalls != 0 {
		t.Errorf("a refused request read initech: %d status, %d pane reads", b.statusCalls, b.paneCalls)
	}
	if code, _ := get(t, h, "/v1/rigs", "Bearer "+testToken); code != http.StatusOK {
		t.Errorf("with the token: %d, want 200", code)
	}

	for name, cfg := range map[string]Config{
		"no token issued":              {Backend: b},
		"a digest but no token issued": {Backend: b, Digest: sha256.Sum256([]byte(testToken))},
	} {
		if code, body := get(t, New(cfg).Handler(), "/v1/rigs", "Bearer "+testToken); code != http.StatusUnauthorized || body != "" {
			t.Errorf("%s: %d %q, want 401 and no body", name, code, body)
		}
	}
}

// AC1: status lists every running rig with its agents, a rig whose TUI does
// not answer is left out of status and listed not running in rigs, and an
// agent at a dialog or banner carries its last screen lines while an idle one
// carries null.
func TestStatus_EveryRunningRigWithScreenLinesAtADialog(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := &fakeBackend{
		rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}, {Name: "beta", Root: "/r/beta"}, {Name: "gone", Root: "/r/gone"}},
		down: map[string]bool{"/r/gone": true},
		agents: map[string][]map[string]any{
			"/r/alpha": {{"role": "eng1", "state": "idle", "banner": "none"}, {"role": "super", "state": "idle", "banner": "permission_prompt"}},
			"/r/beta":  {{"role": "qa1", "state": "waiting", "banner": nil}},
		},
		screen: "Do you want to proceed?\n❯ 1. Yes\n  2. No\n\n\n",
	}
	h := newTestServer(b, &now).Handler()
	code, body := get(t, h, "/v1/status", "Bearer "+testToken)
	if code != 200 {
		t.Fatalf("status %d %s", code, body)
	}
	var got struct{ Rigs []RigStatus }
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rigs) != 2 || got.Rigs[0].Rig != "alpha" || got.Rigs[1].Rig != "beta" {
		t.Fatalf("rigs %+v, want alpha and beta (gone has no TUI)", got.Rigs)
	}
	screens := map[string]any{}
	for _, rg := range got.Rigs {
		for _, a := range rg.Agents {
			screens[a["role"].(string)] = a["screen"]
		}
	}
	if screens["eng1"] != nil {
		t.Errorf("idle eng1 with banner none carries a screen: %v", screens["eng1"])
	}
	for _, role := range []string{"super", "qa1"} {
		lines, ok := screens[role].([]any)
		if !ok || len(lines) != 3 || lines[0] != "Do you want to proceed?" {
			t.Errorf("%s screen = %v, want the three dialog lines", role, screens[role])
		}
	}

	_, body = get(t, h, "/v1/rigs", "Bearer "+testToken)
	var rigs []RigEntry
	json.Unmarshal([]byte(body), &rigs)
	if len(rigs) != 3 || rigs[2].Rig != "gone" || rigs[2].Running || !rigs[0].Running {
		t.Errorf("rigs = %+v, want three with gone not running", rigs)
	}

	if code, _ := get(t, h, "/v1/status/beta", "Bearer "+testToken); code != 200 {
		t.Errorf("status/beta: %d", code)
	}
	if code, _ := get(t, h, "/v1/status/gone", "Bearer "+testToken); code != 404 {
		t.Errorf("status/gone: %d, want 404", code)
	}
}

// AC7 (cache window): status reads a rig at most once per window; a read
// inside the window is the cached one, a read after it reads again.
func TestStatus_ReadAtMostOncePerCacheWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}},
		agents: map[string][]map[string]any{"/r/alpha": {{"role": "eng1", "state": "idle"}}}}
	h := newTestServer(b, &now).Handler()
	for i := 0; i < 5; i++ {
		get(t, h, "/v1/status", "Bearer "+testToken)
		now = now.Add(time.Second)
	}
	if b.statusCalls != 1 || b.paneCalls != 1 {
		t.Errorf("five reads over 5 s: %d status and %d pane reads, want 1 each", b.statusCalls, b.paneCalls)
	}
	now = now.Add(DefaultCacheFor)
	get(t, h, "/v1/status", "Bearer "+testToken)
	if b.statusCalls != 2 {
		t.Errorf("after the window: %d status reads, want 2", b.statusCalls)
	}
}

// AC6 feed-it-nothing: no rig at all, and every rig down, are an empty
// list, never null and never an error.
func TestStatus_NoRunningRigIsAnEmptyList(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for name, b := range map[string]*fakeBackend{
		"no rig":   {},
		"all down": {rigs: []Rig{{Name: "a", Root: "/r/a"}}, down: map[string]bool{"/r/a": true}},
	} {
		h := newTestServer(b, &now).Handler()
		if code, body := get(t, h, "/v1/status", "Bearer "+testToken); code != 200 || strings.TrimSpace(body) != `{"rigs":[]}` {
			t.Errorf("%s: status %d %s, want {\"rigs\":[]}", name, code, body)
		}
		if b.statusCalls != 0 {
			t.Errorf("%s: read status of a rig that is not running", name)
		}
	}
	h := newTestServer(&fakeBackend{}, &now).Handler()
	if code, body := get(t, h, "/v1/rigs", "Bearer "+testToken); code != 200 || strings.TrimSpace(body) != `[]` {
		t.Errorf("rigs with none: %d %s", code, body)
	}
}

// The token is stored as its digest only (0600, folder 0700), and a new
// token revokes the old one.
func TestIssueToken_DigestOnlyAndANewOneRevokesTheOld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, err := IssueToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 43 {
		t.Errorf("token %d chars, want 43", len(first))
	}
	raw, _ := os.ReadFile(filepath.Join(dir, TokenFile))
	if strings.Contains(string(raw), first) {
		t.Fatal("the token itself is stored")
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, TokenFile): 0o600} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", path, st.Mode().Perm(), want)
		}
	}
	d, err := LoadDigest(dir)
	if err != nil || !tokenMatches("Bearer "+first, d) {
		t.Fatalf("issued token does not match its digest: %v", err)
	}
	second, _ := IssueToken(dir)
	d, _ = LoadDigest(dir)
	if tokenMatches("Bearer "+first, d) || !tokenMatches("Bearer "+second, d) {
		t.Error("after a new token, the old one still matches or the new one does not")
	}
	if _, err := LoadDigest(t.TempDir()); !errors.Is(err, ErrNoToken) {
		t.Errorf("no token issued: %v, want ErrNoToken", err)
	}
	if got := PairURL("100.101.1.2", 7392, "tok", "pin"); got != "initech://pair?host=100.101.1.2&port=7392&token=tok&fp=pin" {
		t.Errorf("PairURL = %s", got)
	}
}

// AC5, AC7 (bind): tailnet mode takes the tailnet address and nothing else;
// local-network the LAN's private IPv4; neither ever loopback or the
// unspecified address; no tailnet address is an error naming local-network.
func TestBindAddr_TailnetOnlyNeverAllOrLoopback(t *testing.T) {
	addrs := []IfaceAddr{
		{"lo0", net.ParseIP("127.0.0.1"), true},
		{"lo0", net.ParseIP("::1"), true},
		{"en0", net.ParseIP("192.168.1.50"), true},
		{"en1", net.ParseIP("10.0.0.9"), true},
		{"utun4", net.ParseIP("fd7a:115c:a1e0::1"), true},
		{"utun4", net.ParseIP("100.101.1.2"), true},
		{"utun9", net.ParseIP("100.64.9.9"), false},
		{"anpi0", net.ParseIP("0.0.0.0"), true},
	}
	if ip, err := BindAddr(ListenTailnet, addrs); err != nil || ip.String() != "100.101.1.2" {
		t.Errorf("tailnet: %v %v, want 100.101.1.2", ip, err)
	}
	if ip, err := BindAddr(ListenTailnet, addrs[:4]); !errors.Is(err, ErrNoTailnet) || ip != nil || !strings.Contains(err.Error(), "--listen local-network") {
		t.Errorf("no tailnet address: %v %v, want ErrNoTailnet naming local-network", ip, err)
	}
	if ip, err := BindAddr(ListenTailnet, []IfaceAddr{{"utun4", net.ParseIP("fd7a:115c:a1e0::1"), true}}); err != nil || ip.String() != "fd7a:115c:a1e0::1" {
		t.Errorf("tailnet v6 only: %v %v", ip, err)
	}
	// Carrier NAT and phone hotspots hand out 100.64.0.0/10 on a LAN
	// interface: without Tailscale's fd7a: address beside it, that is not
	// the tailnet.
	cgnat := []IfaceAddr{{"en0", net.ParseIP("100.72.3.4"), true}, {"en0", net.ParseIP("fe80::1"), true}}
	if ip, err := BindAddr(ListenTailnet, cgnat); !errors.Is(err, ErrNoTailnet) {
		t.Errorf("a carrier-NAT 100.64/10 address on en0 was taken as the tailnet: %v %v", ip, err)
	}
	if ip, err := BindAddr(ListenTailnet, append(cgnat, addrs...)); err != nil || ip.String() != "100.101.1.2" {
		t.Errorf("carrier NAT on en0 beside Tailscale on utun4: %v %v, want utun4's 100.101.1.2", ip, err)
	}
	if ip, err := BindAddr(ListenLocalNetwork, addrs); err != nil || ip.String() != "192.168.1.50" {
		t.Errorf("local-network: %v %v, want en0's 192.168.1.50", ip, err)
	}
	if _, err := BindAddr(ListenLocalNetwork, addrs[:2]); err == nil {
		t.Error("local-network with loopback only: no error")
	}
	if _, err := BindAddr("all", addrs); err == nil {
		t.Error("an unknown mode was accepted")
	}
	for _, ip := range []string{"0.0.0.0", "::", "127.0.0.1", "::1"} {
		if ln, err := Listen(net.ParseIP(ip), 0); err == nil {
			ln.Close()
			t.Errorf("Listen(%s) was allowed", ip)
		}
	}
}

// AC5: the real listener binds the one LAN address it was given, and the
// public port does not answer on loopback.
func TestListen_RealBindIsTheOneAddressNotLoopback(t *testing.T) {
	addrs, err := SystemAddrs()
	if err != nil {
		t.Fatal(err)
	}
	ip, err := BindAddr(ListenLocalNetwork, addrs)
	if err != nil {
		t.Skipf("no private LAN address on this machine: %v", err)
	}
	ln, err := Listen(ip, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ta := ln.Addr().(*net.TCPAddr)
	if !ta.IP.Equal(ip) {
		t.Fatalf("bound %v, want %v", ta.IP, ip)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err != nil {
		t.Fatalf("the bound address does not answer: %v", err)
	} else {
		c.Close()
	}
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ta.Port)), time.Second); err == nil {
		c.Close()
		t.Errorf("the public port answers on loopback")
	}
}
