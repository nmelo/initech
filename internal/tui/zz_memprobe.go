package tui

// RIG-ONLY instrument for ini-38nc. This file lives on branch ini-38nc-rig
// and never lands on main.
//
// memprobe adds one log line per perf minute: live heap after the last
// collection (/gc/heap/live:bytes, the number heap_inuse_kb cannot give),
// the collection count, and every local pane's scrollback length. With
// INITECH_MEMPROBE_LEAK=1 it also plants a leak (a copy of every PTY read,
// kept forever) so the instrument can be shown to see one.

import (
	"fmt"
	"os"
	"runtime/metrics"
	"strings"
	"sync"
)

var (
	memprobeLeakOn = os.Getenv("INITECH_MEMPROBE_LEAK") == "1"
	memprobeMu     sync.Mutex
	memprobeLeak   [][]byte
)

// memprobeWrite is the planted leak: off unless INITECH_MEMPROBE_LEAK=1.
func memprobeWrite(b []byte) {
	if !memprobeLeakOn {
		return
	}
	// 64x the read, so a working instrument cannot miss it beside scrollback.
	c := make([]byte, 64*len(b))
	copy(c, b)
	memprobeMu.Lock()
	memprobeLeak = append(memprobeLeak, c)
	memprobeMu.Unlock()
}

// memprobeRoll runs on the main loop, which must never wait on an emulator
// lock (ini-psjt): it only copies the pane list, and memprobeEmit reads the
// scrollback lengths on its own goroutine.
func (t *TUI) memprobeRoll() {
	var panes []*Pane
	for _, pv := range t.panes {
		if lp, ok := pv.(*Pane); ok {
			panes = append(panes, lp)
		}
	}
	t.safeGo(func() { memprobeEmit(panes) })
}

// memprobeEmit logs the memprobe line. Off the main loop: ScrollbackLen
// waits for each pane's emulator lock.
func memprobeEmit(panes []*Pane) {
	var sb []string
	for _, lp := range panes {
		if lp.emu != nil {
			sb = append(sb, fmt.Sprintf("%s=%d", lp.Name(), lp.emu.ScrollbackLen()))
		}
	}
	s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(s)
	var live, gcs uint64
	if s[0].Value.Kind() == metrics.KindUint64 {
		live = s[0].Value.Uint64()
	}
	if s[1].Value.Kind() == metrics.KindUint64 {
		gcs = s[1].Value.Uint64()
	}
	LogInfo("perf", "memprobe", "live_heap_kb", int64(live/1024), "gc_cycles", gcs, "leak_on", memprobeLeakOn, "sb", strings.Join(sb, ","))
}
