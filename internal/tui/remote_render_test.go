// remote_render_test.go verifies the full data path from daemon PTY through
// yamux stream to RemotePane emulator. This is the localhost end-to-end test
// that catches rendering deadlocks/starvation that unit tests miss.
package tui

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

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
