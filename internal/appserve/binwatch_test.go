package appserve

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	oldBuild = "/opt/homebrew/Cellar/initech/2.54.1/bin/initech"
	newBuild = "/opt/homebrew/Cellar/initech/2.54.2/bin/initech"
	theLink  = "/opt/homebrew/bin/initech"
)

// resolveSeq answers each call with the next answer ("" is an error), then
// repeats the last, counting the calls.
func resolveSeq(answers ...string) (func(string) (string, error), *atomic.Int32) {
	var n atomic.Int32
	return func(string) (string, error) {
		i := int(n.Add(1)) - 1
		if i >= len(answers) {
			i = len(answers) - 1
		}
		if answers[i] == "" {
			return "", errors.New("no such file or directory")
		}
		return answers[i], nil
	}, &n
}

// ticks is a channel holding n ticks.
func ticks(n int) chan time.Time {
	c := make(chan time.Time, n)
	for range n {
		c <- time.Time{}
	}
	return c
}

// ini-tqp AC1: the link resolving to a new build is noticed on the tick that
// sees it, after a moment where it did not resolve and one where it was still
// the old build; the line names both paths.
func TestBinaryReplaced_TheNewBuildOnTheTickThatSeesIt(t *testing.T) {
	resolve, calls := resolveSeq("", oldBuild, newBuild)
	got := BinaryReplaced(context.Background(), theLink, oldBuild, resolve, ticks(3))
	if got != newBuild || calls.Load() != 3 {
		t.Fatalf("replaced = %q after %d reads, want %s after 3", got, calls.Load(), newBuild)
	}
	line := ReplacedLine(oldBuild, got)
	if !strings.Contains(line, oldBuild) || !strings.Contains(line, newBuild) || strings.Contains(line, "\n") {
		t.Errorf("line %q: want one line naming both builds", line)
	}
}

// ini-tqp AC2: a link that never resolves, one that keeps resolving to the
// same build, and a clock that never ticks each give nothing until the
// service stops.
func TestBinaryReplaced_NothingUntilTheBuildChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		answers []string
		ticks   int
		reads   int32
	}{
		"does not resolve": {[]string{""}, 5, 5},
		"same build":       {[]string{oldBuild}, 5, 5},
		"never ticks":      {[]string{newBuild}, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			resolve, calls := resolveSeq(tc.answers...)
			ctx, cancel := context.WithCancel(context.Background())
			out := make(chan string, 1)
			tk := ticks(tc.ticks)
			go func() { out <- BinaryReplaced(ctx, theLink, oldBuild, resolve, tk) }()
			deadline := time.Now().Add(5 * time.Second)
			for calls.Load() < tc.reads && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			select {
			case got := <-out:
				t.Fatalf("returned %q before the service stopped", got)
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			if got := <-out; got != "" || calls.Load() != tc.reads {
				t.Errorf("at stop: %q after %d reads, want nothing after %d", got, calls.Load(), tc.reads)
			}
		})
	}
}

// ini-tqp AC2: the watch arms only on a link that resolves; a dev build run by
// its own path, or a link to nothing, arms nothing and says so once.
func TestArmBinaryWatch_OnlyALinkThatResolves(t *testing.T) {
	dir := t.TempDir()
	build := filepath.Join(dir, "Cellar", "initech")
	os.MkdirAll(filepath.Dir(build), 0o755)
	os.WriteFile(build, []byte("#!/bin/sh\n"), 0o755)
	link := filepath.Join(dir, "initech")
	os.Symlink(build, link)
	dangling := filepath.Join(dir, "gone")
	os.Symlink(filepath.Join(dir, "nothing"), dangling)
	realBuild, _ := filepath.EvalSymlinks(build)

	for name, tc := range map[string]struct {
		path  string
		armed bool
		start string
	}{
		"link":     {link, true, realBuild},
		"dev":      {build, false, ""},
		"dangling": {dangling, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			var log bytes.Buffer
			start, ok := ArmBinaryWatch(tc.path, os.Lstat, filepath.EvalSymlinks, &log)
			if ok != tc.armed || start != tc.start {
				t.Errorf("armed %v at %q, want %v at %q", ok, start, tc.armed, tc.start)
			}
			lines := strings.Count(log.String(), "\n")
			if tc.armed && lines != 0 || !tc.armed && (lines != 1 || !strings.Contains(log.String(), "an upgrade does not restart")) {
				t.Errorf("log %q", log.String())
			}
		})
	}
}

// tlsClient talks to a test service on loopback over the throwaway
// self-signed certificate the test just made, which names no loopback
// address, so it does not verify it; nothing here leaves the test.
func tlsClient() *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}}
}

// ini-tqp AC1: once drain closes, a request in flight finishes and is
// answered before the serve returns; a request that will not finish is cut
// at grace.
func TestServeTLSUntil_InFlightFinishesFirstAndGraceBoundsTheRest(t *testing.T) {
	cert, _, err := EnsureCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, grace := range map[string]time.Duration{"finishes": 5 * time.Second, "stuck": 100 * time.Millisecond} {
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var handled atomic.Bool
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				if name == "finishes" {
					time.Sleep(200 * time.Millisecond)
				} else {
					<-release
				}
				io.WriteString(w, "done")
				handled.Store(true)
			})
			defer close(release)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			drain := make(chan struct{})
			served := make(chan error, 1)
			go func() { served <- ServeTLSUntil(ln, h, cert, drain, grace) }()
			body := make(chan string, 1)
			go func() {
				resp, err := tlsClient().Get("https://" + ln.Addr().String() + "/slow")
				if err != nil {
					body <- "error: " + err.Error()
					return
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				body <- string(b)
			}()
			<-started
			at := time.Now()
			close(drain)
			select {
			case err := <-served:
				if !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("serve returned %v, want http.ErrServerClosed", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the serve never returned")
			}
			took := time.Since(at)
			if name == "finishes" {
				if !handled.Load() {
					t.Error("the serve returned before the request in flight was answered")
				}
				if got := <-body; got != "done" {
					t.Errorf("the request in flight got %q, want done", got)
				}
			} else if took > 3*time.Second {
				t.Errorf("a stuck request held the exit %v, want about the grace %v", took, grace)
			}
		})
	}
}

// ini-tqp AC6: a restart onto a new build pushes nothing for what is already
// there: a service that starts with a pane already stuck and a bead already
// waiting pushes neither on its first poll and bd read, nor on the next ones
// while they stay; the pane getting stuck again later still pushes (the
// positive twin: the pusher is listening).
func TestRestart_PushesNothingForWhatIsAlreadyThere(t *testing.T) {
	stuck := PaneState{Name: "super", State: "idle", Banner: "permission_prompt"}
	s, b, rec := pushRig(t, []PaneState{stuck})
	b.set("/r/alpha", WaitingBead{ID: "ini-old", Title: "already waiting", Assignee: "super"})
	s.readWaiting(b)
	s.Poll()
	s.readWaiting(b)
	if got := settle(t, rec, 0); len(got) != 0 {
		t.Fatalf("a start pushed what was already there: %+v", got)
	}
	b.mu.Lock()
	b.panes["/r/alpha"] = []PaneState{{Name: "super", State: "running", Banner: "none"}}
	b.mu.Unlock()
	s.Poll()
	b.mu.Lock()
	b.panes["/r/alpha"] = []PaneState{stuck}
	b.mu.Unlock()
	s.Poll()
	if got := settle(t, rec, 1); len(got) != 1 || got[0].APS.Alert.Body != "super" {
		t.Errorf("stuck again after recovering: %+v, want one push for super", got)
	}
}
