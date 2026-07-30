// remote_render_test.go verifies the full data path from daemon PTY through
// yamux stream to RemotePane emulator. This is the localhost end-to-end test
// that catches rendering deadlocks/starvation that unit tests miss.
package tui

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

func TestRemotePane_EndToEnd_EmulatorHasContent(t *testing.T) {
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("integration test: requires PTY and daemon, run locally")
	}
	if raceDetectorEnabled {
		// ini-ls0c/ini-adb9: same shape as the other skips in this file --
		// the select below asserts rp.Render completes within a fixed 2s
		// wall-clock budget, wrapping real PTY/daemon I/O plus emulator
		// rendering, and -race's own instrumentation overhead is a large
		// enough fraction of that budget to false-fail under load with
		// nothing actually wrong. See remoteRenderDeadlockBound's doc
		// comment for why the bound stays tight instead of growing to
		// compensate.
		//
		// CAVEAT: after this skip, this test's coverage survives only
		// under the `go test ./... -count=1` Makefile target (no -short,
		// no -race) -- not under `go test -race ./internal/tui/` (what QA
		// runs) and not under `make check`/`make test`. Don't read the
		// double skip guard as dead code and delete it.
		t.Skip("ini-ls0c/ini-adb9: -race overhead confounds the deadline; see remoteRenderDeadlockBound's doc comment")
	}

	// Start a daemon with one agent that echoes identifiable output.
	td := startTestDaemon(t, "", "eng1")

	// Connect as a client.
	tc, _ := connectTestClient(t, td.addr, "testclient", "")
	sm := tc.readStreamMap(t)

	if len(sm.Streams) == 0 {
		t.Fatal("no streams in stream map")
	}

	// readStreamMap already consumed replay_start and replay_done.

	// Accept the agent stream.
	stream, err := tc.session.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	defer stream.Close()

	// Create a dummy ControlMux (we won't send commands in this test).
	dummyS, dummyC := net.Pipe()
	defer dummyS.Close()
	defer dummyC.Close()
	dummyMux := NewControlMux(dummyC)
	defer dummyMux.Close()

	// Create a RemotePane from the stream.
	rp := NewRemotePane("eng1", "testhost", stream, dummyMux, 80, 24)
	rp.region = Region{X: 0, Y: 0, W: 80, H: 25}
	rp.Start()

	// Wait for data to arrive via the channel.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(rp.dataCh) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Drain the data channel into the emulator (simulates what Render does).
	drained := 0
	for {
		select {
		case chunk := <-rp.dataCh:
			rp.emu.Write(chunk)
			drained += len(chunk)
		default:
			goto done
		}
	}
done:
	t.Logf("drained %d bytes into emulator", drained)

	if drained == 0 {
		t.Fatal("no bytes received from daemon (dataCh was empty)")
	}

	// Check emulator content: the test agent runs 'echo eng1-ready; cat',
	// so the emulator should contain "eng1-ready".
	cols := rp.emu.Width()
	rows := rp.emu.Height()
	var allText strings.Builder
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			cell := rp.emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				allText.WriteString(cell.Content)
			} else {
				allText.WriteByte(' ')
			}
		}
		allText.WriteByte('\n')
	}
	content := allText.String()
	if !strings.Contains(content, "eng1-ready") {
		t.Errorf("emulator should contain 'eng1-ready', got:\n%s",
			content[:min(len(content), 500)])
	}

	// Now test that Render doesn't block. Use a SimulationScreen.
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 25)

	renderDone := make(chan struct{})
	go func() {
		rp.Render(s, false, false, 1, Selection{})
		close(renderDone)
	}()

	select {
	case <-renderDone:
		t.Log("Render completed without blocking")
	case <-time.After(2 * time.Second):
		t.Fatal("Render blocked for 2+ seconds (deadlock)")
	}

	// Verify the screen has non-empty content from the render.
	var screenText strings.Builder
	for x := 0; x < 80; x++ {
		c, _, _ := s.Get(x, 0)
		screenText.WriteString(c)
	}
	row0 := strings.TrimSpace(screenText.String())
	if row0 == "" {
		t.Error("screen row 0 is empty after Render (content not drawn)")
	} else {
		t.Logf("screen row 0: %q", row0[:min(len(row0), 60)])
	}
}

// TestRemotePane_MultiPane_RenderDoesNotBlock reproduces the production scenario:
// 9 panes (mix of agents) with heavy data flow, multiple render frames.
// This catches the frame-5 stall caused by unbounded dataCh drain.
func TestRemotePane_MultiPane_RenderDoesNotBlock(t *testing.T) {
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("integration test: requires PTY and daemon, run locally")
	}
	if raceDetectorEnabled {
		// ini-ls0c / ini-adb9: each frame below must complete within
		// remoteRenderDeadlockBound. -race's own 5-10x instrumentation
		// overhead is a large enough fraction of that budget to false-fail
		// under a loaded parallel run even when nothing is actually wrong --
		// reproduced repeatedly (18+ times across two independent
		// investigations), zero WARNING: DATA RACE alongside any failure.
		// Skipping here rather than loosening the bound to compensate: a
		// bound wide enough to absorb -race overhead would also silently
		// pass a genuine multi-second render stall, which is exactly the
		// regression this test exists to catch. TestRemoteRenderFrame_DeadlockBoundFires
		// proves remoteRenderFrame's bound mechanism itself still works;
		// TestRemotePane_DAQueryDoesNotDeadlock covers the actual deadlock
		// class (io.Pipe fill) this test also guards against, and isn't
		// skipped here since its blocking assertion has the opposite shape
		// (asserting something does NOT complete quickly, which -race
		// overhead cannot false-fail).
		//
		// CAVEAT for whoever reads this next to three skip guards on one
		// test: this is not dead code. After this skip, the coverage this
		// test provides survives in exactly ONE place -- the `go test ./...
		// -count=1` Makefile target, which runs neither -short nor -race.
		// That is NOT the target QA runs (`go test -race ./internal/tui/`,
		// which is also what surfaced ini-adb9) and NOT `make check`/`make
		// test`. Losing that specific target's coverage is the deliberate,
		// correct trade here -- -race's overhead is precisely the confound --
		// but don't read "skipped under -short AND -race" as "never runs"
		// and delete it; it still runs, just not under either of those two.
		t.Skip("ini-ls0c/ini-adb9: -race overhead confounds the deadline; see comment above")
	}

	// Start a daemon with 5 agents (simulates multi-agent workbench).
	agents := []string{"eng1", "eng2", "eng3", "qa1", "super"}
	td := startTestDaemon(t, "", agents...)

	// Connect as a client.
	tc, _ := connectTestClient(t, td.addr, "testclient", "")
	sm := tc.readStreamMap(t)

	if len(sm.Streams) != len(agents) {
		t.Fatalf("stream_map has %d entries, want %d", len(sm.Streams), len(agents))
	}

	// Accept all agent streams and create RemotePanes.
	dummyS, dummyC := net.Pipe()
	defer dummyS.Close()
	defer dummyC.Close()
	dummyMux := NewControlMux(dummyC)
	defer dummyMux.Close()

	var panes []*RemotePane
	for i := 0; i < len(agents); i++ {
		stream, err := tc.session.Accept()
		if err != nil {
			t.Fatalf("accept stream %d: %v", i, err)
		}
		defer stream.Close()

		// Determine agent name from stream order (daemon opens in agent order).
		name := fmt.Sprintf("agent%d", i)
		for _, s := range sm.Streams {
			// Use stream map names if available.
			name = s
			break
		}

		rp := NewRemotePane(name, "testhost", stream, dummyMux, 80, 24)
		rp.region = Region{X: 0, Y: i * 25, W: 80, H: 25}
		rp.Start()
		panes = append(panes, rp)
	}

	// Wait for data to arrive on at least one pane.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rp := range panes {
			if len(rp.dataCh) > 0 {
				goto dataArrived
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
dataArrived:

	// Flood extra data into each pane's dataCh to simulate heavy replay.
	// This is the key stress: fill the channel with large chunks so the
	// drain loop must process significant data under the budget limit.
	for _, rp := range panes {
		for j := 0; j < 30; j++ {
			// 32KB of terminal output per chunk (worst case).
			chunk := make([]byte, 32*1024)
			for k := range chunk {
				chunk[k] = 'A' + byte(k%26)
			}
			select {
			case rp.dataCh <- chunk:
			default:
				// Channel full, stop filling.
			}
		}
	}

	// Create a simulation screen large enough for all panes.
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 25*len(panes))

	// Render 10 frames (well past frame 5 where production blocks).
	// remoteRenderDeadlockBound is a DEADLOCK bound, not a performance
	// assertion -- see its doc comment. TestRemoteRenderFrame_DeadlockBoundFires
	// forces the exact hang this guards against and proves the bound actually
	// fires (deterministically, not by absence over N probabilistic runs).
	for frame := 1; frame <= 10; frame++ {
		if err := remoteRenderFrame(panes, s, frame, remoteRenderDeadlockBound); err != nil {
			t.Fatal(err)
		}
	}

	t.Logf("all 10 frames rendered across %d panes without blocking", len(panes))

	// Verify at least one pane has content on screen.
	var foundContent bool
	for _, rp := range panes {
		cols := rp.emu.Width()
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := rp.emu.CellAt(col, 0)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			}
		}
		if strings.TrimSpace(line.String()) != "" {
			foundContent = true
			break
		}
	}
	if !foundContent {
		t.Error("no pane has visible content after rendering")
	}
}

// remoteRenderDeadlockBound bounds how long one frame's DrainData+Render may
// take before a caller treats it as evidence of the deadlock class
// TestRemotePane_DAQueryDoesNotDeadlock reproduces directly (io.Pipe fills,
// Emulator.Write blocks forever inside the SafeEmulator write lock).
//
// KEPT TIGHT DELIBERATELY (ini-ls0c/ini-adb9): an earlier version of this
// fix widened this to 30s to absorb go test -race's 5-10x instrumentation
// overhead under load. That was wrong and was withdrawn before landing --
// "any finite bound eventually catches a TRUE infinite hang" is true, but it
// ignores the middle case: a real, finite, user-perceptible stall (say 25s,
// from an actual future regression) would then pass silently. A bound loose
// enough to never trigger under adversarial conditions is not a fix, it's an
// assertion that can no longer fail -- strictly worse than the flake it
// replaces, which at least told the truth on failure. The correct fix for
// the -race/load false-fail is skipping the affected callers under -race
// (see raceDetectorEnabled, and TestRemotePane_MultiPane_RenderDoesNotBlock's
// skip), not loosening what "too slow" means. This bound stays close to the
// original 2s (with modest jitter headroom) so it remains a meaningful
// assertion wherever it still runs.
//
// TestRemoteRenderFrame_DeadlockBoundFires forces the exact hang this bound
// exists to catch and proves it fires -- deterministically, not by absence
// over N probabilistic runs, which cannot distinguish "fixed" from "didn't
// happen to reproduce this time" for a low-base-rate condition.
const remoteRenderDeadlockBound = 5 * time.Second

// remoteRenderFrame drains and renders every pane for one frame, returning
// nil if it completes within bound or an error naming the frame if it
// doesn't. Extracted so TestRemoteRenderFrame_DeadlockBoundFires can drive
// the exact same code TestRemotePane_MultiPane_RenderDoesNotBlock uses, with
// a short bound instead of the real one, rather than re-implementing (and
// potentially drifting from) the mechanism under test.
func remoteRenderFrame(panes []*RemotePane, s tcell.Screen, frame int, bound time.Duration) error {
	renderDone := make(chan struct{})
	go func() {
		for _, rp := range panes {
			rp.DrainData()
		}
		for _, rp := range panes {
			rp.Render(s, false, false, 1, Selection{})
		}
		close(renderDone)
	}()

	select {
	case <-renderDone:
		return nil
	case <-time.After(bound):
		return fmt.Errorf("frame %d: Render blocked for %s+ (%d panes) -- this bound is a deadlock detector, not a performance assertion; see remoteRenderDeadlockBound's doc comment", frame, bound, len(panes))
	}
}

// TestRemoteRenderFrame_DeadlockBoundFires forces the deadlock
// remoteRenderDeadlockBound exists to catch -- a bare SafeEmulator with no
// responseLoop draining its response pipe, fed a DA query, per
// TestRemotePane_DAQueryDoesNotDeadlock's bare_emulator_blocks subtest --
// and proves the bound terminates it rather than hanging forever or
// returning instantly. Uses a short bound so the test itself stays fast;
// remoteRenderFrame is the identical production-path helper
// TestRemotePane_MultiPane_RenderDoesNotBlock calls with the real 30s value,
// so this exercises the real mechanism, not a stand-in for it.
//
// This is the verification ini-ls0c's low base rate (~1-2 reproductions per
// 16 probabilistic runs) demands: a clean N-run batch after the fix proves
// almost nothing for a condition this rare (it's roughly the same
// observation you'd get WITHOUT the fix), because absence of a rare event in
// a small sample cannot distinguish "fixed" from "didn't happen to fire this
// time." Only forcing the condition on purpose and watching the bound
// actually engage constitutes proof.
func TestRemoteRenderFrame_DeadlockBoundFires(t *testing.T) {
	// Bare SafeEmulator: Start() is never called, so no responseLoop drains
	// its response pipe. A DA query write blocks forever inside
	// Emulator.Write -- confirmed by TestRemotePane_DAQueryDoesNotDeadlock's
	// bare_emulator_blocks subtest above.
	rp := &RemotePane{
		name:   "eng1",
		host:   "wb",
		emu:    vt.NewSafeEmulator(80, 24),
		dataCh: make(chan []byte, 1),
	}
	rp.dataCh <- []byte("\x1b[c") // DA1 query: CSI c.

	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)

	const shortBound = 150 * time.Millisecond
	start := time.Now()
	err := remoteRenderFrame([]*RemotePane{rp}, s, 1, shortBound)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected remoteRenderFrame to report the deadlock bound firing; it returned nil instead -- this means the test did not actually force the hang it claims to")
	}
	if elapsed < shortBound {
		t.Errorf("bound fired after %s, want >= %s -- returned too early, meaning DrainData did not actually block", elapsed, shortBound)
	}
	const slack = 2 * time.Second
	if elapsed > shortBound+slack {
		t.Errorf("bound fired after %s, want ~%s (within %s) -- overshot by a lot, the select/timeout wiring may be broken", elapsed, shortBound, slack)
	}
	t.Logf("deadlock bound correctly fired after %s (bound %s): %v", elapsed, shortBound, err)
}

// TestRemotePane_DAQueryDoesNotDeadlock reproduces the root cause: the VT
// emulator writes DA/DSR responses to an internal io.Pipe. Without a reader
// draining that pipe, io.Pipe.Write blocks inside Emulator.Write, which holds
// the SafeEmulator write lock forever, deadlocking the main goroutine.
func TestRemotePane_DAQueryDoesNotDeadlock(t *testing.T) {
	// First, prove the bug exists without responseLoop. Use a bare
	// SafeEmulator (no responseLoop draining the pipe).
	t.Run("bare_emulator_blocks", func(t *testing.T) {
		emu := vt.NewSafeEmulator(80, 24)
		// DA1 query: ESC [ c  (CSI c = "send primary device attributes")
		// The emulator processes this and writes a response to its pipe.
		da := []byte("\x1b[c")
		done := make(chan struct{})
		go func() {
			emu.Write(da)
			close(done)
		}()
		select {
		case <-done:
			t.Fatal("expected bare emulator Write to block on DA query (no pipe reader)")
		case <-time.After(200 * time.Millisecond):
			// Expected: Write blocks because nobody reads from the pipe.
			t.Log("confirmed: bare emulator blocks on DA query without pipe reader")
		}
	})

	// Now prove the fix: RemotePane.Start() launches responseLoop which
	// drains the pipe, so Write never blocks.
	t.Run("remote_pane_does_not_block", func(t *testing.T) {
		if raceDetectorEnabled {
			// ini-ls0c/ini-adb9: same shape and same fix as
			// TestRemotePane_MultiPane_RenderDoesNotBlock's skip -- this
			// subtest asserts DrainData+Render completes within
			// remoteRenderDeadlockBound, which -race's own overhead can
			// exceed under load with nothing actually wrong. Unlike the
			// sibling bare_emulator_blocks subtest above (which asserts the
			// OPPOSITE: that something does NOT complete quickly, a shape
			// -race overhead cannot false-fail), this one shares the
			// fragile shape and needs the same skip. See
			// remoteRenderDeadlockBound's doc comment for why the bound
			// itself stays tight instead of growing to compensate.
			//
			// CAVEAT: after this skip, this subtest's coverage survives
			// only under the `go test ./... -count=1` Makefile target (no
			// -short, no -race) -- not under `go test -race
			// ./internal/tui/` (what QA runs) and not under `make
			// check`/`make test`. Don't read this skip guard as dead code
			// and delete it.
			t.Skip("ini-ls0c/ini-adb9: -race overhead confounds the deadline; see remoteRenderDeadlockBound's doc comment")
		}

		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		ctrlS, ctrlC := net.Pipe()
		defer ctrlS.Close()
		defer ctrlC.Close()

		rp := NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 24)
		rp.region = Region{X: 0, Y: 0, W: 80, H: 25}
		rp.Start()

		// Write data containing DA query sequence directly to the dataCh
		// (simulating what readLoop does). Then call DrainData + Render.
		// Mix normal text + DA query + more text.
		payload := []byte("hello\x1b[cworld\r\n")
		rp.dataCh <- payload

		s := tcell.NewSimulationScreen("")
		s.Init()
		s.SetSize(80, 25)

		if err := remoteRenderFrame([]*RemotePane{rp}, s, 1, remoteRenderDeadlockBound); err != nil {
			t.Fatal(err)
		}
		t.Log("DrainData+Render completed (responseLoop drained DA response)")

		rp.Close()
	})
}
