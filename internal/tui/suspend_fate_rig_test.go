//go:build !windows

package tui

// suspend_fate_rig_test.go is the ini-9imx TRIAGE rig: what happens to a
// message sent to a suspended agent, per entry path, with the sender's
// feedback captured alongside the fate. Findings-only bead -- this rig is the
// evidence generator, committed (gated) so the fix bead inherits the
// reproduction instead of a description of one.
//
// REAL PATHS THROUGHOUT (the real-workload fixture rule): suspension happens
// via the REAL policy (checkSuspendPolicy under forced pressure numbers --
// the same victim block production runs: sendMu-held Close + suspended flag),
// resume via the REAL resumePane (real respawn, real queue drain), sends via
// the REAL IPC handler with the response captured (the response IS what
// initech send prints and exits on), and the pane is a REAL PTY process that
// echoes what it receives, so delivery is observable in output rather than
// inferred from state.
//
//	INITECH_9IMX=1 go test ./internal/tui/ -run TestSuspendFate -v -count=1 -timeout 120s

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// echoPane starts a REAL PTY process that echoes each input line as GOT:<line>,
// so message arrival is observable in the emulator.
func echoPane(t *testing.T, name string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{
		Name:    name,
		Command: []string{"sh", "-c", `while read l; do echo "GOT:$l"; done`},
	}, 24, 80)
	if err != nil {
		t.Fatalf("start echo pane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	return p
}

// suspendViaRealPolicy drives the REAL auto-suspend path: pressure above
// threshold, the pane idle and beadless, checkSuspendPolicy picks it as the
// victim and runs the production Close+flags block.
func suspendViaRealPolicy(t *testing.T, tui *TUI, p *Pane) {
	t.Helper()
	p.mu.Lock()
	p.activity = StateIdle
	p.lastOutputTime = time.Now().Add(-30 * time.Minute)
	p.memoryRSS = 500000
	p.mu.Unlock()
	tui.systemMemTotal, tui.systemMemAvail, tui.pressureThreshold = 100000, 5000, 80
	tui.checkSuspendPolicy()
	if !p.IsSuspended() {
		t.Fatal("the REAL suspend policy did not suspend the candidate; the rig cannot proceed " +
			"(a hand-rolled stand-in here would hide exactly the buffering defects under test)")
	}
}

func ipcSend(t *testing.T, tui *TUI, target, text string) IPCResponse {
	t.Helper()
	conn := &fakeConn{}
	tui.handleIPCSend(conn, IPCRequest{Action: "send", Target: target, Text: text, Enter: true})
	var resp IPCResponse
	line := conn.written[:conn.findNewline()]
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unparseable IPC response %q: %v", conn.written, err)
	}
	return resp
}

func paneOutput(tui *TUI, name string) string {
	var out string
	tui.runOnMain(func() {
		for _, pv := range tui.panes {
			if pv.Name() == name {
				if lp, ok := pv.(*Pane); ok {
					out = strings.Join(nonEmpty(snapRows(lp.emu)), "\n")
				}
			}
		}
	})
	return out
}

// TestSuspendFate_IPCSendPath is matrix row 1: `initech send` to a suspended
// agent, via the real handler. Designed behavior: queue + resume-on-message +
// an honest "resumed and delivered" response. The assertions verify the
// DESIGN actually holds end to end with a real respawn -- if any leg fails,
// that failure is a triage finding, not a broken test.
func TestSuspendFate_IPCSendPath(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1 to run the ini-9imx suspension fate rig")
	}
	p := echoPane(t, "eng1")
	tui := newResourceTestTUI(100000, 5000, 80)
	tui.panes = toPaneViews([]*Pane{p})
	time.Sleep(500 * time.Millisecond)

	suspendViaRealPolicy(t, tui, p)

	resp := ipcSend(t, tui, "eng1", "hello-after-suspend")
	t.Logf("SENDER FEEDBACK (suspended target): OK=%v Error=%q Data=%q", resp.OK, resp.Error, resp.Data)

	time.Sleep(1500 * time.Millisecond) // real respawn + queue drain
	out := paneOutput(tui, "eng1")
	t.Logf("RESPawned pane output:\n%s", out)

	if resp.OK && !strings.Contains(out, "GOT:hello-after-suspend") {
		t.Errorf("FINDING: sender told %q but the message never reached the resumed process -- "+
			"success-reported loss, the worst shape", resp.Data)
	}
	if !resp.OK {
		t.Logf("FINDING: send to suspended agent FAILS with %q (loud, at least -- but deliver/report "+
			"chains treat this as fatal)", resp.Error)
	}
}

// TestSuspendFate_DirectSendTextPath is matrix row 2: the path forward_send
// (window-2 sends, cross-machine sends via daemon.go:466/783, tui.go:912)
// actually takes -- pv.SendText with NO suspended pre-check. This drives the
// same call those sites make against a really-suspended pane and records the
// fate: the primitive writes into a closed PTY (Close() closes but never nils
// ptmx, so even sendPaneTextLocked's nil guard does not fire).
func TestSuspendFate_DirectSendTextPath(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	p := echoPane(t, "eng1")
	tui := newResourceTestTUI(100000, 5000, 80)
	tui.panes = toPaneViews([]*Pane{p})
	time.Sleep(500 * time.Millisecond)
	suspendViaRealPolicy(t, tui, p)

	p.SendText("forwarded-hello", true) // exactly what the bypass sites do

	p.mu.Lock()
	queued := len(p.messageQueue)
	p.mu.Unlock()
	t.Logf("after direct SendText on suspended pane: queue=%d ptmx_nil=%v", queued, p.ptmx == nil)

	// Resume the real way and see if it ever arrives.
	if err := tui.resumePane(p, "triage"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	out := paneOutput(tui, "eng1")
	if strings.Contains(out, "GOT:forwarded-hello") {
		t.Log("direct SendText message survived to resume (queued somewhere)")
	} else if queued == 0 {
		t.Errorf("FINDING: direct SendText (the forward_send / daemon-send path) against a "+
			"suspended pane is SILENTLY LOST -- not queued, not delivered on resume, no error "+
			"to any caller. Output after resume:\n%s", out)
	}
}

// TestSuspendFate_InterruptAndBead is matrix rows 3-4: interrupt targets the
// PTY (fate on a suspended pane recorded); bead display is TUI state only and
// should be unaffected by suspension.
func TestSuspendFate_InterruptAndBead(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	p := echoPane(t, "eng1")
	tui := newResourceTestTUI(100000, 5000, 80)
	tui.panes = toPaneViews([]*Pane{p})
	time.Sleep(500 * time.Millisecond)
	suspendViaRealPolicy(t, tui, p)

	conn := &fakeConn{}
	tui.handleIPCInterrupt(conn, IPCRequest{Action: "interrupt", Target: "eng1"})
	var resp IPCResponse
	json.Unmarshal(conn.written[:conn.findNewline()], &resp)
	t.Logf("INTERRUPT on suspended: OK=%v Error=%q Data=%q -- an interrupt for a process that "+
		"does not exist", resp.OK, resp.Error, resp.Data)

	conn2 := &fakeConn{}
	tui.handleIPCBead(conn2, IPCRequest{Action: "bead", Target: "eng1", Text: "ini-test"})
	var resp2 IPCResponse
	json.Unmarshal(conn2.written[:conn2.findNewline()], &resp2)
	t.Logf("BEAD DISPLAY on suspended: OK=%v Error=%q (TUI-state only; expected unaffected)",
		resp2.OK, resp2.Error)
}
