package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// ini-jr08: what the race test binary holds over a shard. Off unless
// INITECH_TEST_PROFILE names a file prefix (scripts/shardtests run -profile
// sets it); then memory is sampled every testProfileEvery into
// <prefix>.memstats.csv, and at exit a last sample before and after a GC and
// the goroutines still alive go to <prefix>.memstats.csv and
// <prefix>.goroutines.txt. The sampler's ticker is a real clock on purpose:
// the thing measured is the binary's memory over wall time, not a test.

const testProfileEvery = 5 * time.Second

// startTestProfile starts the sampler when the variable is set and returns
// what TestMain calls after m.Run; with it unset it starts nothing.
func startTestProfile() (stop func()) {
	return startTestProfileAt(os.Getenv("INITECH_TEST_PROFILE"), testProfileEvery)
}

func startTestProfileAt(prefix string, every time.Duration) (stop func()) {
	if prefix == "" {
		return func() {}
	}
	f, err := os.Create(prefix + ".memstats.csv")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test profile:", err)
		return func() {}
	}
	fmt.Fprintln(f, "time,label,heap_alloc,heap_inuse,heap_idle,heap_released,heap_sys,sys,goroutines,num_gc")
	sample := func(label string) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Fprintf(f, "%s,%s,%d,%d,%d,%d,%d,%d,%d,%d\n", time.Now().UTC().Format(time.RFC3339Nano), label,
			m.HeapAlloc, m.HeapInuse, m.HeapIdle, m.HeapReleased, m.HeapSys, m.Sys, runtime.NumGoroutine(), m.NumGC)
	}
	sample("start")
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				sample("tick")
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-exited
		sample("end")
		runtime.GC()
		sample("end-after-gc")
		f.Close()
		if g, err := os.Create(prefix + ".goroutines.txt"); err == nil {
			pprof.Lookup("goroutine").WriteTo(g, 1) //nolint:errcheck // a profile, best effort
			g.Close()
		}
	}
}

// Off by default: no prefix starts no sampler; with a prefix one runs until
// stop (the positive twin).
func TestTestProfile_OffStartsNothing(t *testing.T) {
	n0 := runtime.NumGoroutine()
	stop := startTestProfileAt("", time.Hour)
	if n := runtime.NumGoroutine(); n != n0 {
		t.Fatalf("off started %d goroutines", n-n0)
	}
	stop()
	on := startTestProfileAt(filepath.Join(t.TempDir(), "pkg"), time.Hour)
	if n := runtime.NumGoroutine(); n != n0+1 {
		t.Errorf("on: %d goroutines started, want the sampler", n-n0)
	}
	on()
}

// On: the samples file has its header, the start and both end samples, and
// the goroutine dump names a goroutine.
func TestTestProfile_OnWritesTheSamplesAndTheGoroutines(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "pkg")
	startTestProfileAt(prefix, time.Hour)() // no tick: start and end only
	csv, err := os.ReadFile(prefix + ".memstats.csv")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"time,label,heap_alloc", ",start,", ",end,", ",end-after-gc,"} {
		if !strings.Contains(string(csv), want) {
			t.Errorf("samples lack %q:\n%s", want, csv)
		}
	}
	g, err := os.ReadFile(prefix + ".goroutines.txt")
	if err != nil || !strings.Contains(string(g), "goroutine profile: total") {
		t.Fatalf("goroutine dump %q, %v", g, err)
	}
}
