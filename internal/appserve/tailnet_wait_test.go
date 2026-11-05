package appserve

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// ini-she: with no tailnet address the service waits in-process. These run
// inside a synctest bubble, so time.After is the bubble's fake clock and no
// test waits on a real one; the interface reader is injected.

// syncBuf is a log writer safe across the bubble's goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.WriteString(string(p))
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// tailnetReader answers ErrNoTailnet until give is called, then the address,
// and counts its calls. It goes through BindAddr, the real tailnet choice.
type tailnetReader struct {
	mu    sync.Mutex
	tries int
	addrs []IfaceAddr
}

func (r *tailnetReader) find() (net.IP, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tries++
	return BindAddr(ListenTailnet, r.addrs)
}

func (r *tailnetReader) give() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addrs = []IfaceAddr{
		{Iface: "utun4", IP: net.ParseIP("100.101.102.103"), Up: true},
		{Iface: "utun4", IP: net.ParseIP("fd7a:115c:a1e0::1"), Up: true},
		{Iface: "en0", IP: net.ParseIP("192.168.1.50"), Up: true},
	}
}

func (r *tailnetReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tries
}

type waitResult struct {
	ip  net.IP
	err error
}

func startWait(ctx context.Context, stop <-chan struct{}, r *tailnetReader, log *syncBuf, waits *int) chan waitResult {
	out := make(chan waitResult, 1)
	go func() {
		ip, err := WaitForTailnet(ctx, stop, r.find, time.After, log, func() { *waits++ })
		out <- waitResult{ip, err}
	}()
	return out
}

// Ten minutes of fake time with no address: at most 14 tries (2 s doubling
// to a 60 s cap), exactly one log line, one onWait, and no return.
func TestTailnetWait_TenMinutesWithoutTailscaleIsOneLineAndFewTries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r, log, waits := &tailnetReader{}, &syncBuf{}, 0
		out := startWait(ctx, nil, r, log, &waits)
		time.Sleep(10 * time.Minute) // the bubble's clock, not a real one
		synctest.Wait()
		select {
		case res := <-out:
			t.Fatalf("returned while there was no address: %v %v", res.ip, res.err)
		default:
		}
		// Tries at 0, 2, 6, 14, 30 and 62 s, then every 60 s to 542 s.
		if n := r.count(); n != 14 {
			t.Errorf("%d tries in ten minutes, want 14 (2 s doubling to a 60 s cap)", n)
		}
		if got := strings.Count(log.String(), "\n"); got != 1 || !strings.Contains(log.String(), TailnetWaitLine) {
			t.Errorf("log over ten minutes:\n%s\nwant exactly the one wait line", log.String())
		}
		if waits != 1 {
			t.Errorf("onWait ran %d times, want 1", waits)
		}
	})
}

// No interfaces at all (feed it nothing) is the same wait, and the address
// arriving at the first retry binds once: the tailnet address, never another.
func TestTailnetWait_TheAddressAtTheFirstRetryIsTaken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, log, waits := &tailnetReader{}, &syncBuf{}, 0
		out := startWait(t.Context(), nil, r, log, &waits)
		synctest.Wait()
		if r.count() != 1 {
			t.Fatalf("%d tries before the first retry, want 1", r.count())
		}
		r.give()
		time.Sleep(TailnetRetryFirst)
		synctest.Wait()
		res := <-out
		if res.err != nil || !res.ip.Equal(net.ParseIP("100.101.102.103")) {
			t.Fatalf("got %v %v, want the tailnet address", res.ip, res.err)
		}
		if r.count() != 2 {
			t.Errorf("%d tries, want 2", r.count())
		}
	})
}

// An address there from the start is taken at once, with no wait line.
func TestTailnetWait_AnAddressAtStartLogsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, log, waits := &tailnetReader{}, &syncBuf{}, 0
		r.give()
		res := <-startWait(t.Context(), nil, r, log, &waits)
		if res.err != nil || log.String() != "" || waits != 0 {
			t.Fatalf("got %v, log %q, waits %d", res.err, log.String(), waits)
		}
	})
}

// SIGTERM (ctx ending) during the wait returns within a second of fake time,
// even inside the 60 s cap.
func TestTailnetWait_StopsWithinASecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		r, log, waits := &tailnetReader{}, &syncBuf{}, 0
		out := startWait(ctx, nil, r, log, &waits)
		time.Sleep(5 * time.Minute) // into the capped waits
		synctest.Wait()
		start := time.Now()
		cancel()
		res := <-out
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("err %v, want context.Canceled", res.err)
		}
		if took := time.Since(start); took >= time.Second {
			t.Errorf("returned %v after the stop, want under 1 s", took)
		}
	})
}

// The stop channel (an upgrade) ends the wait with ErrWaitStopped.
func TestTailnetWait_AnUpgradeStopsTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stop := make(chan struct{})
		r, log, waits := &tailnetReader{}, &syncBuf{}, 0
		out := startWait(t.Context(), stop, r, log, &waits)
		synctest.Wait()
		close(stop)
		if res := <-out; !errors.Is(res.err, ErrWaitStopped) {
			t.Fatalf("err %v, want ErrWaitStopped", res.err)
		}
	})
}

// Any error other than no tailnet address is returned, not waited on.
func TestTailnetWait_AnotherErrorIsReturned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("read interfaces: boom")
		_, err := WaitForTailnet(t.Context(), nil, func() (net.IP, error) { return nil, boom }, time.After, &syncBuf{}, nil)
		if !errors.Is(err, boom) {
			t.Fatalf("err %v, want boom", err)
		}
	})
}

// --notify hears of the wait at most once an hour, under its own stamp.
func TestTailnetWait_NotifyIsHourly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: --notify runs under /bin/sh; launchd is macOS")
	}
	state := t.TempDir()
	marks := filepath.Join(t.TempDir(), "notified")
	notify := "echo x >> " + marks
	for range 3 {
		ReportTailnetWait(state, notify, &syncBuf{})
	}
	data, _ := os.ReadFile(marks)
	if n := strings.Count(string(data), "\n"); n != 1 {
		t.Fatalf("notify ran %d times in a row, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(state, TailnetNotifiedAtFile)); err != nil {
		t.Fatalf("no tailnet stamp: %v", err)
	}
}

// When the listener ends on its own (the tailnet address went away), the
// connections it had open end with it: a request held open (the feed) sees
// its context end, so the listener bound when the address returns serves
// alone. Event-based: the handler reports its start and its end.
func TestServeTLSUntil_TheListenerGoneEndsOpenConnections(t *testing.T) {
	cert, _, err := EnsureCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, ended := make(chan struct{}), make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(ended)
	})
	served := make(chan error, 1)
	go func() { served <- ServeTLSUntil(ln, h, cert, nil, time.Second) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a test against its own self-signed cert
	go func() {
		if resp, err := client.Get("https://" + ln.Addr().String() + "/"); err == nil {
			io.Copy(io.Discard, resp.Body) //nolint:errcheck // read until the server ends it
			resp.Body.Close()
		}
	}()
	<-started
	ln.Close()
	<-served
	<-ended // never comes if the open connection outlives the listener
}
