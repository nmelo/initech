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
		c, _, _, _ := s.GetContent(x, 0)
		screenText.WriteRune(c)
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
	for frame := 1; frame <= 10; frame++ {
		renderDone := make(chan struct{})
		go func() {
			for _, rp := range panes {
				rp.Render(s, false, false, 1, Selection{})
			}
			close(renderDone)
		}()

		select {
		case <-renderDone:
			// Good.
		case <-time.After(2 * time.Second):
			t.Fatalf("frame %d: Render blocked for 2+ seconds (%d panes)", frame, len(panes))
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
		// (simulating what readLoop does). Then call Render which drains
		// dataCh into the emulator.
		// Mix normal text + DA query + more text.
		payload := []byte("hello\x1b[cworld\r\n")
		rp.dataCh <- payload

		s := tcell.NewSimulationScreen("")
		s.Init()
		s.SetSize(80, 25)

		done := make(chan struct{})
		go func() {
			rp.Render(s, false, false, 1, Selection{})
			close(done)
		}()

		select {
		case <-done:
			t.Log("Render completed (responseLoop drained DA response)")
		case <-time.After(2 * time.Second):
			t.Fatal("Render blocked for 2+ seconds (responseLoop not draining pipe)")
		}

		rp.Close()
	})
}
