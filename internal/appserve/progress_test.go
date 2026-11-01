package appserve

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// progressBackend is a fakeBackend that also reads progress, counting calls.
type progressBackend struct {
	fakeBackend
	efforts     map[string][]string // rig root -> active efforts
	effortCalls int
	reads       []string // "rig prefix", in order
	failPrefix  string
}

func (b *progressBackend) ActiveEfforts(r Rig) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.effortCalls++
	return b.efforts[r.Root], nil
}

func (b *progressBackend) Progress(r Rig, prefix string) (progress.Report, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads = append(b.reads, r.Name+" "+prefix)
	if prefix == b.failPrefix && prefix != "" {
		return progress.Report{}, errors.New("bd unreachable")
	}
	return progress.Report{Rig: r.Name, Prefix: prefix, Total: 3}, nil
}

// fakeTimer is an After that moves clk by each wait at once and stops the
// loop once total has passed; it records every wait.
func fakeTimer(clk *lockedClock, total time.Duration, cancel func()) (func(time.Duration) <-chan time.Time, *[]time.Duration) {
	var waits []time.Duration
	var elapsed time.Duration
	return func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		elapsed += d
		clk.Add(d)
		ch := make(chan time.Time, 1)
		if elapsed > total {
			cancel()
			return make(chan time.Time)
		}
		ch <- clk.Now()
		return ch
	}, &waits
}

// AC8: progress is read every 5 minutes, no faster, per rig and only for
// efforts with a bead in progress: over ten minutes three runs (the first at
// once), a rig with no active effort reads no report, a stopped rig is not
// asked, and the status poll's timer is not used for it.
func TestProgress_EveryFiveMinutesOnlyForActiveEfforts(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &progressBackend{efforts: map[string][]string{"/r/a": {"ini-x.", "ini-w."}, "/r/gone": {"ini-z."}}}
	b.rigs = []Rig{{Name: "a", Root: "/r/a"}, {Name: "b", Root: "/r/b"}, {Name: "gone", Root: "/r/gone"}}
	b.down = map[string]bool{"/r/gone": true}
	ctx, cancel := context.WithCancel(context.Background())
	after, waits := fakeTimer(clk, 10*time.Minute, cancel)
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now, After: after})
	done := make(chan struct{})
	go func() { s.RunProgress(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunProgress did not stop")
	}
	for _, d := range *waits {
		if d != 5*time.Minute {
			t.Fatalf("waited %s, want 5m", d)
		}
	}
	want := []string{"a ini-w.", "a ini-x.", "a ini-w.", "a ini-x.", "a ini-w.", "a ini-x."}
	if strings.Join(b.reads, ",") != strings.Join(want, ",") {
		t.Errorf("reads over ten minutes = %v, want %v", b.reads, want)
	}
	if b.effortCalls != 6 { // rigs a and b, three runs
		t.Errorf("%d effort lists, want 6", b.effortCalls)
	}
}

// GET /v1/progress is the latest run as a list of reports and starts no
// read; empty before a run. GET /v1/progress/{rig} is that rig's part;
// ?prefix= an effort in the run, or one read on request at most once per
// cache window. A failed effort is left out of the run. Behind the token.
func TestProgress_ServesTheLatestRunAndReadsNothingOnRequest(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &progressBackend{efforts: map[string][]string{"/r/a": {"ini-x.", "ini-bad."}}, failPrefix: "ini-bad."}
	b.rigs = []Rig{{Name: "a", Root: "/r/a"}, {Name: "b", Root: "/r/b"}}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	h := s.Handler()
	auth := "Bearer " + testToken

	if code, body := get(t, h, "/v1/progress", auth); code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("before a run: %d %s, want []", code, body)
	}
	if len(b.reads) != 0 {
		t.Fatalf("a request read progress: %v", b.reads)
	}
	s.readProgress(b)
	reads := len(b.reads)
	_, body := get(t, h, "/v1/progress", auth)
	var reps []progress.Report
	if err := json.Unmarshal([]byte(body), &reps); err != nil || len(reps) != 1 || reps[0].Rig != "a" || reps[0].Prefix != "ini-x." {
		t.Fatalf("latest run = %s (%v), want only a's ini-x.", body, err)
	}
	if _, body := get(t, h, "/v1/progress/b", auth); strings.TrimSpace(body) != "[]" {
		t.Errorf("rig b has no active effort: %s", body)
	}
	if _, body := get(t, h, "/v1/progress/a", auth); !strings.Contains(body, `"prefix":"ini-x."`) {
		t.Errorf("rig a: %s", body)
	}
	if _, body := get(t, h, "/v1/progress/a?prefix=ini-x.", auth); !strings.HasPrefix(body, "{") {
		t.Errorf("an effort in the run is one report: %s", body)
	}
	if len(b.reads) != reads {
		t.Errorf("serving the run read progress again: %v", b.reads[reads:])
	}

	for i := 0; i < 3; i++ {
		if code, _ := get(t, h, "/v1/progress/a?prefix=ini-old.", auth); code != 200 {
			t.Errorf("on-request read: %d", code)
		}
		clk.Add(time.Second)
	}
	if n := len(b.reads) - reads; n != 1 {
		t.Errorf("three requests inside the window read %d times, want 1", n)
	}
	clk.Add(DefaultCacheFor)
	get(t, h, "/v1/progress/a?prefix=ini-old.", auth)
	if n := len(b.reads) - reads; n != 2 {
		t.Errorf("after the window: %d reads, want 2", n)
	}

	if code, _ := get(t, h, "/v1/progress/gone", auth); code != 404 {
		t.Errorf("unknown rig: %d", code)
	}
	if code, body := get(t, h, "/v1/progress", ""); code != 401 || body != "" {
		t.Errorf("no token: %d %q", code, body)
	}
}
