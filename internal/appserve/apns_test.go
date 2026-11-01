package appserve

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPNs is an HTTP/2 APNs that records every request and answers each
// device as told (200 by default).
type fakeAPNs struct {
	srv *httptest.Server

	mu      sync.Mutex
	reqs    []apnsReq
	answer  map[string]int    // device -> status
	reasons map[string]string // device -> reason
	hold    chan struct{}     // when set, every request waits on it
}

type apnsReq struct {
	Device, Proto string
	Header        http.Header
	Body          []byte
}

func newFakeAPNs(t *testing.T) *fakeAPNs {
	t.Helper()
	f := &fakeAPNs{answer: map[string]int{}, reasons: map[string]string{}}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		device := strings.TrimPrefix(r.URL.Path, "/3/device/")
		f.mu.Lock()
		f.reqs = append(f.reqs, apnsReq{Device: device, Proto: r.Proto, Header: r.Header.Clone(), Body: body})
		code, reason, hold := f.answer[device], f.reasons[device], f.hold
		f.mu.Unlock()
		if hold != nil {
			<-hold
		}
		if code == 0 {
			code = 200
		}
		w.WriteHeader(code)
		if reason != "" {
			json.NewEncoder(w).Encode(map[string]string{"reason": reason})
		}
	}))
	f.srv.EnableHTTP2 = true
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPNs) requests() []apnsReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apnsReq(nil), f.reqs...)
}

// fixtureKey writes a fresh P-256 key as Apple's .p8 is shaped, mode 0600,
// and returns its path and file bytes.
func fixtureKey(t *testing.T, mode os.FileMode) (string, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	p := filepath.Join(t.TempDir(), "AuthKey_TEST.p8")
	if err := os.WriteFile(p, pemBytes, mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
	return p, pemBytes, key
}

// waitFor is done's channel with a bound.
func waitFor(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pushes did not finish")
	}
}

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) add(s string) { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }
func (l *logLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func testSender(t *testing.T, fa *fakeAPNs, dir string, clk *lockedClock, log *logLines) (*Sender, *Devices) {
	t.Helper()
	path, _, _ := fixtureKey(t, 0o600)
	cfg := APNsConfig{KeyPath: path, KeyID: "KEYID12345", TeamID: "TEAM123456", Topic: "com.example.initech"}
	key, err := LoadAPNsKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	devs, err := LoadDevices(dir, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	return NewSender(cfg, key, fa.srv.URL, fa.srv.Client(), devs, clk.Now, log.add), devs
}

func pushAndWait(t *testing.T, s *Sender, p Payload) {
	t.Helper()
	done := make(chan struct{})
	s.Push(p, func() { close(done) })
	waitFor(t, done)
}

// ini-r0q.16: a push goes to every registered device over HTTP/2 to
// /3/device/<token> with the provider token, the topic and an alert type,
// and its body is exactly the posted shape: a title, the pane, the rig, pane
// and reason as data, and nothing else.
func TestSender_PushesThePostedShapeToEveryDevice(t *testing.T) {
	fa := newFakeAPNs(t)
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	s, devs := testSender(t, fa, t.TempDir(), clk, &logLines{})
	devs.Add("aaaa")
	devs.Add("bbbb")
	pushAndWait(t, s, NewPayload("initech", "super", "waiting"))
	reqs := fa.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want one per device", len(reqs))
	}
	for _, r := range reqs {
		if r.Proto != "HTTP/2.0" {
			t.Errorf("proto %s, want HTTP/2.0", r.Proto)
		}
		h := r.Header
		if !strings.HasPrefix(h.Get("authorization"), "bearer ") || h.Get("apns-topic") != "com.example.initech" ||
			h.Get("apns-push-type") != "alert" || h.Get("apns-priority") != "10" || h.Get("apns-expiration") == "" {
			t.Errorf("headers = %v", h)
		}
		var got map[string]any
		json.Unmarshal(r.Body, &got)
		want := `{"aps":{"alert":{"body":"super","title":"initech: initech"},"sound":"default","thread-id":"initech"},"data":{"pane":"super","reason":"waiting","rig":"initech"}}`
		norm, _ := json.Marshal(got)
		if string(norm) != want {
			t.Errorf("payload = %s\nwant      %s", norm, want)
		}
	}
	if reqs[0].Device == reqs[1].Device {
		t.Errorf("both pushes went to %s", reqs[0].Device)
	}
}

// ini-r0q.16 AC6: the provider token is an ES256 JWT with the key id and the
// team that verifies against the key, made in memory and remade before it is
// 40 minutes old, never per push.
func TestSender_TheProviderTokenIsSignedAndRefreshed(t *testing.T) {
	fa := newFakeAPNs(t)
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	path, _, key := fixtureKey(t, 0o600)
	cfg := APNsConfig{KeyPath: path, KeyID: "KEYID12345", TeamID: "TEAM123456", Topic: "t"}
	loaded, err := LoadAPNsKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	devs, _ := LoadDevices(t.TempDir(), clk.Now)
	devs.Add("aaaa")
	s := NewSender(cfg, loaded, fa.srv.URL, fa.srv.Client(), devs, clk.Now, func(string) {})
	jwtOf := func() string {
		r := fa.requests()
		return strings.TrimPrefix(r[len(r)-1].Header.Get("authorization"), "bearer ")
	}
	pushAndWait(t, s, NewPayload("r", "p", "stuck"))
	first := jwtOf()
	parts := strings.Split(first, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	head, _ := base64.RawURLEncoding.DecodeString(parts[0])
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if string(head) != `{"alg":"ES256","kid":"KEYID12345"}` || !strings.Contains(string(claims), `"iss":"TEAM123456"`) || !strings.Contains(string(claims), `"iat":1800000000`) {
		t.Errorf("head %s claims %s", head, claims)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&key.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the jwt does not verify against the key")
	}
	clk.Add(39 * time.Minute)
	pushAndWait(t, s, NewPayload("r", "p", "stuck"))
	if jwtOf() != first {
		t.Error("the token was remade before 40 minutes")
	}
	clk.Add(2 * time.Minute)
	pushAndWait(t, s, NewPayload("r", "p", "stuck"))
	if jwtOf() == first {
		t.Error("the token was not remade after 40 minutes")
	}
}

// ini-r0q.16 AC5: a 410, or a 400 BadDeviceToken, unregisters that device and
// logs once, and it is pushed no more; any other error is logged once an
// hour per device and never retried; no device logs once an hour; APNs
// unreachable is logged and not retried.
func TestSender_RejectionsAndErrors(t *testing.T) {
	fa := newFakeAPNs(t)
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	log := &logLines{}
	dir := t.TempDir()
	s, devs := testSender(t, fa, dir, clk, log)

	pushAndWait(t, s, NewPayload("r", "p", "waiting"))
	pushAndWait(t, s, NewPayload("r", "p", "waiting"))
	if len(fa.requests()) != 0 || len(log.all()) != 1 || !strings.Contains(log.all()[0], "no device registered") {
		t.Fatalf("no device: %d requests, log %q", len(fa.requests()), log.all())
	}

	for _, d := range []string{"0410", "0400", "0500", "0200"} {
		devs.Add(d)
	}
	fa.mu.Lock()
	fa.answer["0410"], fa.answer["0400"], fa.reasons["0400"] = 410, 400, "BadDeviceToken"
	fa.answer["0500"], fa.reasons["0500"] = 500, "InternalServerError"
	fa.mu.Unlock()
	pushAndWait(t, s, NewPayload("r", "p", "waiting"))
	if got := devs.Tokens(); len(got) != 2 || got[0] != "0500" || got[1] != "0200" {
		t.Errorf("after the rejections: %v, want 0500 and 0200", got)
	}
	if d, _ := LoadDevices(dir, clk.Now); len(d.Tokens()) != 2 {
		t.Errorf("the file still holds %v", d.Tokens())
	}
	before := len(fa.requests())
	for i := 0; i < 3; i++ {
		pushAndWait(t, s, NewPayload("r", "p", "waiting"))
	}
	if n := len(fa.requests()) - before; n != 6 {
		t.Errorf("three pushes to two devices made %d requests, want 6 (no retries)", n)
	}
	var unregistered, failedLines int
	for _, l := range log.all() {
		if strings.Contains(l, "unregistered") {
			unregistered++
		}
		if strings.Contains(l, "did not go") {
			failedLines++
		}
	}
	if unregistered != 2 || failedLines != 1 {
		t.Errorf("log: %d unregistered, %d failure lines; want 2 and 1 in the hour: %q", unregistered, failedLines, log.all())
	}
	clk.Add(time.Hour)
	pushAndWait(t, s, NewPayload("r", "p", "waiting"))
	failedLines = 0
	for _, l := range log.all() {
		if strings.Contains(l, "did not go") {
			failedLines++
		}
	}
	if failedLines != 2 {
		t.Errorf("an hour later: %d failure lines, want 2", failedLines)
	}

	dead := NewSender(s.cfg, s.key, "https://127.0.0.1:1", fa.srv.Client(), devs, clk.Now, log.add)
	n := len(log.all())
	pushAndWait(t, dead, NewPayload("r", "p", "waiting"))
	pushAndWait(t, dead, NewPayload("r", "p", "waiting"))
	unreach := 0
	for _, l := range log.all()[n:] {
		if strings.Contains(l, "APNs unreachable") {
			unreach++
		}
	}
	if unreach != 2 {
		t.Errorf("unreachable: %d lines for two devices over two pushes, want one per device", unreach)
	}
}

// ini-r0q.16 AC5: the key missing, not 0600, not PEM, or not P-256, or a
// field unset, is an error naming the problem and never the key's bytes.
func TestLoadAPNsKey_RefusesWhatItCannotUse(t *testing.T) {
	good, raw, _ := fixtureKey(t, 0o600)
	open, _, _ := fixtureKey(t, 0o644)
	notPEM := filepath.Join(t.TempDir(), "k.p8")
	os.WriteFile(notPEM, []byte("not a key"), 0o600)
	rsaish := filepath.Join(t.TempDir(), "r.p8")
	other, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(other)
	os.WriteFile(rsaish, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	base := APNsConfig{KeyID: "K", TeamID: "T", Topic: "x"}
	for name, c := range map[string]struct {
		path, want string
		cfg        func(APNsConfig) APNsConfig
	}{
		"missing":   {filepath.Join(t.TempDir(), "none.p8"), "no such file", nil},
		"0644":      {open, "must be 0600", nil},
		"not PEM":   {notPEM, "not a PEM", nil},
		"P-384":     {rsaish, "not a P-256", nil},
		"no key_id": {good, "key_id is not set", func(c APNsConfig) APNsConfig { c.KeyID = ""; return c }},
	} {
		cfg := base
		cfg.KeyPath = c.path
		if c.cfg != nil {
			cfg = c.cfg(cfg)
		}
		_, err := LoadAPNsKey(cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Errorf("%s: the error carries key bytes", name)
		}
	}
	base.KeyPath = good
	if _, err := LoadAPNsKey(base); err != nil {
		t.Errorf("a good key: %v", err)
	}
	_ = raw
	if (APNsConfig{}).Host() != APNsProduction || (APNsConfig{Environment: "Sandbox"}).Host() != APNsSandbox {
		t.Error("environment: production by default, sandbox when it says so")
	}
}

// ini-r0q.16 AC6: the key's bytes appear in no request APNs receives and no
// log line, through pushes that succeed, are rejected and fail: neither the
// file, its base64 body, nor the private scalar.
func TestSender_TheKeyNeverLeaves(t *testing.T) {
	fa := newFakeAPNs(t)
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	log := &logLines{}
	path, raw, key := fixtureKey(t, 0o600)
	cfg := APNsConfig{KeyPath: path, KeyID: "KEYID12345", TeamID: "TEAM123456", Topic: "t"}
	loaded, err := LoadAPNsKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	devs, _ := LoadDevices(t.TempDir(), clk.Now)
	for _, d := range []string{"aa01", "aa02", "aa03"} {
		devs.Add(d)
	}
	fa.mu.Lock()
	fa.answer["aa02"], fa.answer["aa03"], fa.reasons["aa03"] = 410, 500, "Oops"
	fa.mu.Unlock()
	s := NewSender(cfg, loaded, fa.srv.URL, fa.srv.Client(), devs, clk.Now, log.add)
	pushAndWait(t, s, NewPayload("r", "p", "waiting"))
	clk.Add(time.Hour)
	pushAndWait(t, s, NewPayload("r", "p", "stuck"))

	block, _ := pem.Decode(raw)
	secrets := map[string]string{
		"the file":       string(raw),
		"its base64":     base64.StdEncoding.EncodeToString(block.Bytes),
		"the scalar hex": key.D.Text(16),
		"the scalar b64": base64.RawURLEncoding.EncodeToString(key.D.Bytes()),
	}
	var seen []string
	for _, r := range fa.requests() {
		hb, _ := json.Marshal(r.Header)
		seen = append(seen, string(r.Body), string(hb))
	}
	seen = append(seen, log.all()...)
	if len(fa.requests()) == 0 || len(log.all()) == 0 {
		t.Fatalf("nothing to check: %d requests, %d log lines", len(fa.requests()), len(log.all()))
	}
	for name, secret := range secrets {
		for _, s := range seen {
			if strings.Contains(s, secret) || (len(secret) > 40 && strings.Contains(s, secret[8:40])) {
				t.Fatalf("%s appears in %.80q", name, s)
			}
		}
	}
}
