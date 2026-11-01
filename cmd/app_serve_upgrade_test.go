package cmd

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/spf13/cobra"
)

// lockedBuffer is a log the service and the test share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// upgradeRig is runAppServe set up from its real entry point on a loopback
// listener, a temp state folder, a folder with no rig, and an initech that is
// a link to build v1, looked at every few milliseconds.
type upgradeRig struct {
	link, v1, v2, state string
	log                 *lockedBuffer
	done                chan error
}

func startUpgradeRig(t *testing.T) *upgradeRig { return startUpgradeRigWith(t, "") }

// startUpgradeRigWith is startUpgradeRig with the service started as exe
// instead of the link ("" for the link).
func startUpgradeRigWith(t *testing.T, exe string) *upgradeRig {
	t.Helper()
	skipWindows(t)
	projects, _ := ownRigFolder(t)
	bin := t.TempDir()
	r := &upgradeRig{link: filepath.Join(bin, "bin", "initech"), v1: filepath.Join(bin, "Cellar", "2.54.1", "initech"),
		v2: filepath.Join(bin, "Cellar", "2.54.2", "initech"), state: t.TempDir(), log: &lockedBuffer{}, done: make(chan error, 1)}
	for _, p := range []string{r.v1, r.v2} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	}
	os.MkdirAll(filepath.Dir(r.link), 0o755)
	if err := os.Symlink("../Cellar/2.54.1/initech", r.link); err != nil {
		t.Fatal(err)
	}
	r.v1, _ = filepath.EvalSymlinks(r.v1)
	r.v2, _ = filepath.EvalSymlinks(r.v2)

	oldState, oldAddrs, oldListen, oldProjects := appServeStateDir, appServeAddrs, appServeListen, appServeProjects
	oldListenOn, oldExe, oldEvery, oldGrace, oldNotify := appServeListenOn, appServeExecutable, appServeBinaryEvery, appServeDrainGrace, appServeNotify
	t.Cleanup(func() {
		appServeStateDir, appServeAddrs, appServeListen, appServeProjects = oldState, oldAddrs, oldListen, oldProjects
		appServeListenOn, appServeExecutable, appServeBinaryEvery, appServeDrainGrace, appServeNotify = oldListenOn, oldExe, oldEvery, oldGrace, oldNotify
	})
	appServeStateDir = func() string { return r.state }
	appServeListen = appserve.ListenLocalNetwork
	appServeAddrs = func() ([]appserve.IfaceAddr, error) {
		return []appserve.IfaceAddr{{Iface: "en0", IP: net.ParseIP("192.168.1.50"), Up: true}}, nil
	}
	appServeListenOn = func(net.IP, int) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	appServeProjects = []string{projects}
	if exe == "" {
		exe = r.link
	}
	appServeExecutable = func() (string, error) { return exe, nil }
	appServeBinaryEvery = 10 * time.Millisecond
	appServeDrainGrace = time.Second
	appServeNotify = ""

	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	c.SetErr(r.log)
	go func() { r.done <- runAppServe(c, nil) }()
	r.waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(r.state, appserve.MarkerFile))
		return err == nil
	}, "the service never started serving")
	return r
}

func (r *upgradeRig) waitFor(t *testing.T, ok func() bool, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s; log:\n%s", why, r.log.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *upgradeRig) result(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("the service did not exit; log:\n%s", r.log.String())
		return nil
	}
}

// sigterm sends this process SIGTERM, as launchd's stop does; runAppServe
// has it caught, so the test process lives on.
func sigterm(t *testing.T) {
	t.Helper()
	self, err := os.FindProcess(os.Getpid())
	if err == nil {
		err = self.Signal(syscall.SIGTERM)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// moveLink renames a new link to target over the old one, as the upgrade
// script does: there is no moment without a link.
func (r *upgradeRig) moveLink(t *testing.T, target string) {
	t.Helper()
	tmp := r.link + ".new"
	if err := os.Symlink(target, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, r.link); err != nil {
		t.Fatal(err)
	}
}

// ini-tqp AC1, AC3: from the real entry point, runAppServe, an upgrade moving
// the link to a new build ends the service: one line naming both builds, a
// clean exit (nil, so exit 0 and no "Error:" line), the running marker
// cleared, so launchd's KeepAlive starts the new build and nothing reports a
// crash. Red at base: no watch, the service runs on.
func TestAppServe_AnUpgradeRestartsOntoTheNewBuild(t *testing.T) {
	r := startUpgradeRig(t)
	if strings.Contains(r.log.String(), "an upgrade does not restart") {
		t.Fatalf("the watch did not arm on a link; log:\n%s", r.log.String())
	}
	select {
	case err := <-r.done:
		t.Fatalf("exited before any upgrade: %v; log:\n%s", err, r.log.String())
	case <-time.After(100 * time.Millisecond):
	}
	r.moveLink(t, "../Cellar/2.54.2/initech")
	if err := r.result(t); err != nil {
		t.Fatalf("exit %v, want nil (a planned restart); log:\n%s", err, r.log.String())
	}
	log := r.log.String()
	if n := strings.Count(log, "was replaced"); n != 1 {
		t.Errorf("%d replaced lines, want 1; log:\n%s", n, log)
	}
	if !strings.Contains(log, appserve.ReplacedLine(r.v1, r.v2)) {
		t.Errorf("no line naming %s and %s; log:\n%s", r.v1, r.v2, log)
	}
	if _, err := os.Stat(filepath.Join(r.state, appserve.MarkerFile)); !os.IsNotExist(err) {
		t.Errorf("the running marker is still there (%v): the next start would report a crash", err)
	}
}

// ini-tqp AC3: SIGTERM while the watch waits still stops the service cleanly,
// as launchd's stop does, and says nothing about a replaced build.
func TestAppServe_SIGTERMWhileWatchingStopsCleanly(t *testing.T) {
	r := startUpgradeRig(t)
	time.Sleep(50 * time.Millisecond) // a few looks at the link
	sigterm(t)
	if err := r.result(t); err != nil {
		t.Fatalf("exit %v on SIGTERM, want nil; log:\n%s", err, r.log.String())
	}
	log := r.log.String()
	if strings.Contains(log, "was replaced") || !strings.Contains(log, "stopping on terminated") {
		t.Errorf("log:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(r.state, appserve.MarkerFile)); !os.IsNotExist(err) {
		t.Errorf("the running marker is still there after SIGTERM (%v)", err)
	}
}

// ini-tqp AC2: a dev build run by its own path arms no watch, said once at
// start; the link moving elsewhere then restarts nothing.
func TestAppServe_ADevBuildByItsOwnPathIsNotWatched(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "initech")
	os.WriteFile(dev, []byte("#!/bin/sh\n"), 0o755)
	r := startUpgradeRigWith(t, dev)
	if n := strings.Count(r.log.String(), "an upgrade does not restart the service"); n != 1 {
		t.Errorf("%d not-watched lines, want 1; log:\n%s", n, r.log.String())
	}
	os.WriteFile(dev+".new", []byte("#!/bin/sh\n"), 0o755)
	os.Rename(dev+".new", dev)
	select {
	case err := <-r.done:
		t.Fatalf("a dev build exited (%v); log:\n%s", err, r.log.String())
	case <-time.After(200 * time.Millisecond):
	}
	sigterm(t)
	if err := r.result(t); err != nil {
		t.Fatal(err)
	}
}
