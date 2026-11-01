package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// The push setup from its real entry point, appServePushes, over the real
// appBackend and the real Server.Poll: rigs in a temp projects folder with a
// TUI answering, bd's waiting read faked, one registered device, and Apple
// stood in for by an HTTP/2 server that counts what it gets.

// waitingRunner answers bd's waiting_on_nelson read with beads.
type waitingRunner struct {
	mu    sync.Mutex
	beads string
}

func (w *waitingRunner) set(b string) { w.mu.Lock(); w.beads = b; w.mu.Unlock() }

func (w *waitingRunner) RunInDir(dir, name string, args ...string) (string, error) {
	if name == "bd" && strings.Contains(strings.Join(args, " "), "waiting_on_nelson") {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.beads, nil
	}
	return "", errors.New("not run")
}

func (w *waitingRunner) Run(name string, args ...string) (string, error) {
	return w.RunInDir("", name, args...)
}

type pushesRig struct {
	projects string
	key      string // a P-256 key, 0600
	srv      *appserve.Server
	b        *appBackend
	runner   *waitingRunner
	step     *stepper
	log      *lockedBuffer
	pushes   *atomic.Int32
	cancel   context.CancelFunc
	done     chan struct{}
}

// apnsYAML is an apns block naming keyPath, with topic.
func apnsYAML(keyPath, topic string) string {
	return "apns:\n  key_id: KEYID12345\n  team_id: TEAM123456\n  topic: " + topic + "\n  key_path: " + keyPath + "\n"
}

// writeRig writes rig name's initech.yaml with extra appended, and a TUI on
// its socket so it runs.
func (r *pushesRig) writeRig(t *testing.T, name, extra string) {
	t.Helper()
	dir := filepath.Join(r.projects, name)
	os.MkdirAll(dir, 0o755)
	cfg := "project: " + name + "\nroot: " + dir + "\nroles:\n  - eng1\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	sock := tui.SocketPath(dir, name)
	if _, err := os.Stat(sock); err != nil {
		serveFakeTUI(t, sock, tui.IPCResponse{OK: true, Data: `[{"name":"super","activity":"idle","alive":true}]`})
	}
}

// writeKey writes a fresh P-256 key, PKCS#8 as Apple's .p8 is, mode 0600.
func writeKey(t *testing.T, path string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newPushesRig sets up rig "r" with extra in its initech.yaml, polls once
// (the first poll) and starts appServePushes.
func newPushesRig(t *testing.T, extra string) *pushesRig {
	t.Helper()
	return newPushesRigWith(t, extra, nil)
}

// newPushesRigWith is newPushesRig with before run ahead of the first poll.
func newPushesRigWith(t *testing.T, extra string, before func(*pushesRig)) *pushesRig {
	t.Helper()
	skipWindows(t)
	projects, _ := ownRigFolder(t)
	r := &pushesRig{projects: projects, key: filepath.Join(t.TempDir(), "AuthKey_TEST.p8"), runner: &waitingRunner{beads: "[]"},
		step: &stepper{}, log: &lockedBuffer{}, pushes: &atomic.Int32{}, done: make(chan struct{})}
	r.writeRig(t, "r", strings.ReplaceAll(extra, "@KEY@", r.key))

	apple := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/3/device/") {
			r.pushes.Add(1)
		}
	}))
	apple.EnableHTTP2 = true
	apple.StartTLS()
	t.Cleanup(apple.Close)
	oldHost, oldClient := appServeAPNsHost, appServeAPNsClient
	t.Cleanup(func() { appServeAPNsHost, appServeAPNsClient = oldHost, oldClient })
	appServeAPNsHost, appServeAPNsClient = apple.URL, apple.Client()

	state := t.TempDir()
	devs, err := appserve.LoadDevices(state, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devs.Add(strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	r.b = &appBackend{dirs: []string{projects}, runner: r.runner}
	r.srv = appserve.New(appserve.Config{Backend: r.b, HasToken: true, StateDir: state, After: r.step.after})
	if before != nil {
		before(r)
	}
	r.srv.Poll()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() { appServePushes(ctx, r.srv, r.b, r.log); close(r.done) }()
	t.Cleanup(func() { cancel(); <-r.done })
	return r
}

// until waits for ok, failing with why and the log.
func (r *pushesRig) until(t *testing.T, ok func() bool, why string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s; log:\n%s", why, r.log.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *pushesRig) count(s string) int { return strings.Count(r.log.String(), s) }

// pollQuietly polls n times and gives the setup time to react to each.
func (r *pushesRig) pollQuietly(n int) {
	for range n {
		r.srv.Poll()
		time.Sleep(20 * time.Millisecond)
	}
}

// aBeadEnters is a bead entering waiting_on_nelson over two bd reads: the
// first read is the baseline.
func (r *pushesRig) aBeadEnters() {
	r.step.once(r.srv.RunWaiting)
	r.runner.set(`[{"id":"ini-w1","title":"Approve the release","assignee":"super"}]`)
	r.step.once(r.srv.RunWaiting)
}

const (
	notYet = "initech app-serve: no pushes yet: "
	pushOn = "initech app-serve: pushes go to "
)

// ini-632 AC1, AC3: from appServePushes and Server.Poll, a first scan that
// finds no apns block says so once and looks again at every poll; the poll
// that reads the block turns pushes on, said once; a bead entering
// waiting_on_nelson after that is pushed. Red at base: the setup returned
// after the first poll with "no pushes" for good.
func TestAppServePushes_ABlockFoundAfterAHeldScanTurnsPushesOn(t *testing.T) {
	r := newPushesRig(t, "")
	r.until(t, func() bool { return r.count(notYet) == 1 }, "no not-yet line after the first poll")
	if !strings.Contains(r.log.String(), "no apns block") || !strings.Contains(r.log.String(), "looking again at each poll") {
		t.Errorf("the not-yet line does not say why and that it looks again: %q", r.log.String())
	}
	r.pollQuietly(3)
	writeKey(t, r.key)
	r.writeRig(t, "r", apnsYAML(r.key, "com.example.initech"))
	r.srv.Poll()
	r.until(t, func() bool { return r.count(pushOn) == 1 }, "pushes did not come on at the poll that read the block")
	r.pollQuietly(2)
	if n, m := r.count(notYet), r.count(pushOn); n != 1 || m != 1 {
		t.Errorf("%d not-yet and %d on lines, want one each; log:\n%s", n, m, r.log.String())
	}
	r.aBeadEnters()
	r.until(t, func() bool { return r.pushes.Load() == 1 }, "a bead entering waiting_on_nelson after pushes came on was not pushed")
}

// ini-632 AC2: with no usable settings at any poll, one line however many
// polls, no push, and the setup returns when the service stops.
func TestAppServePushes_NothingUsableIsOneLineAndAQuietWait(t *testing.T) {
	for name, extra := range map[string]string{
		"no block":    "",
		"key not 600": apnsYAML("@KEY@", "com.example.initech"),
	} {
		t.Run(name, func(t *testing.T) {
			r := newPushesRig(t, extra)
			if name == "key not 600" {
				writeKey(t, r.key)
				os.Chmod(r.key, 0o644)
			}
			r.until(t, func() bool { return r.count(notYet) == 1 }, "no not-yet line")
			r.pollQuietly(5)
			r.aBeadEnters()
			time.Sleep(100 * time.Millisecond)
			if n := r.count("\n"); n != 1 || r.pushes.Load() != 0 {
				t.Errorf("%d lines over six polls and %d pushes, want one line and none; log:\n%s", n, r.pushes.Load(), r.log.String())
			}
			if strings.Contains(r.log.String(), "BEGIN") {
				t.Error("the log carries the key file's bytes")
			}
			r.cancel()
			select {
			case <-r.done:
			case <-time.After(5 * time.Second):
				t.Fatal("the setup did not return when the service stopped")
			}
		})
	}
}

// ini-632 AC2: two rigs with different apns blocks say so once; when they
// agree, the next poll turns pushes on.
func TestAppServePushes_TwoBlocksThenOneTurnsPushesOn(t *testing.T) {
	r := newPushesRigWith(t, apnsYAML("@KEY@", "com.example.initech"), func(r *pushesRig) {
		writeKey(t, r.key)
		r.writeRig(t, "s", apnsYAML(r.key, "com.example.other"))
	})
	r.until(t, func() bool { return strings.Contains(r.log.String(), "hold different apns blocks") }, "two blocks were not named")
	r.writeRig(t, "s", apnsYAML(r.key, "com.example.initech"))
	r.srv.Poll()
	r.until(t, func() bool { return r.count(pushOn) == 1 }, "pushes did not come on once the blocks agreed")
	if n := r.count(notYet); n != 1 {
		t.Errorf("%d not-yet lines, want 1; log:\n%s", n, r.log.String())
	}
}

// ini-632 AC2: a key file that appears after the block turns pushes on at
// the next poll.
func TestAppServePushes_AKeyThatAppearsLaterTurnsPushesOn(t *testing.T) {
	r := newPushesRig(t, apnsYAML("@KEY@", "com.example.initech"))
	r.until(t, func() bool { return r.count(notYet) == 1 }, "a missing key said nothing")
	r.pollQuietly(2)
	writeKey(t, r.key)
	r.srv.Poll()
	r.until(t, func() bool { return r.count(pushOn) == 1 }, "pushes did not come on once the key loaded")
	if n := r.count(notYet); n != 1 {
		t.Errorf("%d not-yet lines, want 1; log:\n%s", n, r.log.String())
	}
}

// ini-632 AC4, the positive twin: a block and key present at the first poll
// turn pushes on at once, as before: one line, no not-yet line.
func TestAppServePushes_ABlockAtTheFirstPollIsAsBefore(t *testing.T) {
	skipWindows(t)
	key := filepath.Join(t.TempDir(), "AuthKey_TEST.p8")
	writeKey(t, key)
	r := newPushesRig(t, apnsYAML(key, "com.example.initech"))
	r.until(t, func() bool { return r.count(pushOn) == 1 }, "pushes did not come on at the first poll")
	r.pollQuietly(2)
	if r.count(notYet) != 0 || r.count("\n") != 1 {
		t.Errorf("log:\n%s", r.log.String())
	}
	r.aBeadEnters()
	r.until(t, func() bool { return r.pushes.Load() == 1 }, "a bead entering was not pushed")
}
