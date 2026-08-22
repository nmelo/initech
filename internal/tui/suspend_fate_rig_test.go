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

// shellForFixture picks the shell the echo fixture runs under. It is a
// VARIABLE so the fixture can be isolated as a cause: on ubuntu /bin/sh is
// dash, on macOS it is bash in POSIX mode, and running the identical fixture
// under bash on the SAME runner makes the shell the only thing that changed
// (shipper's discriminator, ini-0lko). Defaults to sh, which is what this rig
// has always used.
func shellForFixture() string {
	if sh := os.Getenv("INITECH_9IMX_SHELL"); sh != "" {
		return sh
	}
	return "sh"
}

// echoPane starts a REAL PTY process that echoes each input line as GOT:<line>,
// so message arrival is observable in the emulator.
func echoPane(t *testing.T, name string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{
		Name:    name,
		Command: []string{shellForFixture(), "-c", `while read l; do echo "GOT:$l"; done`},
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

// childLiveness answers the one question an ABSENCE of output cannot: is the
// child alive and READING, or was there never a process for the message to
// reach? Without this, "the pane printed nothing" is consistent with both a
// real delivery loss and a rig whose child never came back -- and a rig that
// cannot tell those apart reports the second as the first, which is a
// manufactured finding.
//
// The probe is a nonce echoed back with the GOT: prefix. That prefix is
// content ONLY THE CHILD CAN PRODUCE: terminal echo reproduces the input line
// verbatim, never a transformation of it, so a GOT: hit cannot be the
// terminal answering on the child's behalf. This is the confound that put
// ini-hbj4's darwin greens in doubt (its fixture asserted injected bytes
// merely APPEARED, which local echo satisfies with the child receiving
// nothing); the prefix shape is immune to it.
//
// Tri-state on purpose. A SUSPENDED pane queues the probe instead of
// delivering it, so silence there means nothing at all -- collapsing that into
// "dead" would invent a rig fault out of correct behavior.
type liveness int

const (
	livenessReading   liveness = iota // child answered the probe: alive and consuming input
	livenessSilent                    // process alive, did not answer: running but not reading
	livenessSuspended                 // pane suspended: the probe was queued, verdict impossible
	livenessNoProcess                 // no process was ever spawned: implicates the respawn path
	livenessExited                    // a process WAS spawned and has already exited: implicates the fixture
	livenessNoPane                    // no pane by that name at all: the fixture itself is wrong
)

func (l liveness) String() string {
	switch l {
	case livenessReading:
		return "child answered the probe (alive and reading)"
	case livenessSuspended:
		return "pane is SUSPENDED, so the probe was queued -- inconclusive by construction"
	case livenessNoProcess:
		return "NO process was ever spawned (nil pid) -- this is about the respawn path, not " +
			"about delivery and not about the fixture"
	case livenessExited:
		return "a process WAS spawned and has already EXITED -- the respawn worked and the child " +
			"quit, which points at the fixture's shell loop rather than at the product"
	case livenessNoPane:
		return "there is no pane by that name at all -- the fixture is wrong, not the product"
	default:
		// Deliberately does NOT pick between the two remaining owners. Alive
		// and silent is equally consistent with a fixture shell that is not
		// echoing (dash vs bash) and with a respawned PTY that is not wired to
		// the child's stdio on this platform -- and the second would be a real
		// product defect. Naming one here would decide by assertion what only a
		// discriminating run can decide.
		return "the process IS ALIVE and did not answer the probe -- so it respawned and is not " +
			"reading. Two candidates remain and this rig cannot separate them: the fixture's " +
			"shell loop, or the respawned PTY not reaching the child's stdio. Run the same " +
			"fixture under a different shell on the same runner to tell them apart"
	}
}

func childLiveness(t *testing.T, tui *TUI, name, nonce string) liveness {
	t.Helper()
	var pv PaneView
	tui.runOnMain(func() {
		for _, cand := range tui.panes {
			if cand.Name() == name {
				pv = cand
			}
		}
	})
	if pv == nil {
		return livenessNoPane
	}
	// "No live reader" has causes with DIFFERENT OWNERS and the verdict is
	// useless without knowing which: the product respawned no process at all,
	// or it respawned one that is not consuming input (a fixture whose shell
	// loop does not survive respawn on this platform).
	//
	// IsAlive() CANNOT ARBITRATE THAT. It returns the cached p.alive field --
	// a flag our own code writes -- not an observation of the process, so a
	// child can be alive and reading while the flag says dead, and a verdict
	// built on it would name an owner on the strength of our bookkeeping
	// (shipper's read on ini-0lko; verified at pane.go IsAlive and
	// liveness_unix.go). childProcessAlive is the real question: signal 0 to
	// the pid we spawned. Both are logged, because when they DISAGREE that is
	// a third cause with a third owner -- state management, not delivery and
	// not the fixture -- and it would otherwise hide behind whichever one the
	// verdict happened to read.
	var proc *os.Process
	if lp, ok := pv.(*Pane); ok {
		lp.mu.Lock()
		if lp.cmd != nil {
			proc = lp.cmd.Process
		}
		lp.mu.Unlock()
	}
	realAlive := childProcessAlive(proc)
	flagAlive := pv.IsAlive()
	// The shell is named in the output on purpose: a diagnostic that varies
	// the shell and does not WITNESS which one ran leaves its own conclusion
	// resting on an env var being honoured. Cheap to log, and it is the
	// load-bearing link when this rig is used to argue a platform finding.
	t.Logf("LIVENESS CONTEXT for %s: shell=%s signal0=%v cachedFlag=%v suspended=%v capturedBytes=%d",
		name, shellForFixture(), realAlive, flagAlive, pv.IsSuspended(), len(paneOutput(tui, name)))
	if realAlive != flagAlive {
		t.Logf("LIVENESS DISAGREEMENT for %s: signal0=%v but cached alive flag=%v. Neither "+
			"'no process' nor 'not reading' -- the flag disagrees with the process, which is "+
			"state management and owned by neither.", name, realAlive, flagAlive)
	}
	if pv.IsSuspended() {
		return livenessSuspended
	}
	// A shell whose read loop EXITS also answers signal 0 with "dead", so
	// "not alive" alone would blame the respawn path for a fixture that
	// spawned fine and then quit -- the exact misattribution this split
	// exists to prevent, one level down. A non-nil pid means something WAS
	// spawned; only a nil one implicates the respawn path itself.
	if !realAlive {
		if proc == nil {
			return livenessNoProcess
		}
		return livenessExited
	}
	probe := "liveness-" + nonce
	pv.SendText(probe, true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(paneOutput(tui, name), "GOT:"+probe) {
			return livenessReading
		}
		time.Sleep(100 * time.Millisecond)
	}
	return livenessSilent
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
		// "success-reported loss" is a strong claim and an empty capture does
		// not support it on its own: no output is equally consistent with the
		// child never having respawned. Discriminate before claiming (ini-0lko).
		switch live := childLiveness(t, tui, "eng1", "ipcsend"); live {
		case livenessReading:
			t.Errorf("FINDING (product): sender told %q but the message never reached the resumed "+
				"process, and the child IS alive and reading -- a liveness probe sent afterwards "+
				"came back. Success-reported loss, the worst shape.", resp.Data)
		default:
			t.Errorf("RIG FAULT, not a product finding: sender told %q and the message did not "+
				"appear, but %s. An absent line proves nothing about delivery when nothing was "+
				"listening.", resp.Data, live)
		}
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

// TestSuspendFate_InterruptAndBeadNameSuspendedStateAndRemedy is matrix rows
// 3-4, and it is a GATE on ini-g7fl deliverable #3 (feedback truth): both
// surfaces must name the SUSPENDED state and the resume-on-message remedy, not
// merely fail.
//
// It was Logf-only when it shipped, and qa1's compile-verified mutant -- delete
// the IsSuspended branch in handleIPCInterrupt -- survived the whole suite while
// the response silently degraded to the generic "is not running". A test that
// prints the very string that is allowed to rot is a camera, not a gate: the
// vacuous-guard family in test form. Two lessons kept as assertions rather than
// as prose: each cell asserts the suspended-aware CONTENT (so the mutant dies),
// AND asserts the generic fallback text is ABSENT (so a future refactor cannot
// satisfy the first check by appending "suspended" to the wrong message).
//
// Renamed from TestSuspendFate_InterruptAndBead: the old name claimed exactly
// the cells it did not discriminate.
func TestSuspendFate_InterruptAndBeadNameSuspendedStateAndRemedy(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	p := echoPane(t, "eng1")
	tui := newResourceTestTUI(100000, 5000, 80)
	tui.panes = toPaneViews([]*Pane{p})
	time.Sleep(500 * time.Millisecond)
	suspendViaRealPolicy(t, tui, p)

	// Row 3: interrupt. The degraded form under qa1's mutant is
	// "agent eng1 is not running" -- true of a corpse, wrong for a pane that
	// resumes on its next message, and it points the operator at a restart that
	// would discard the queue.
	conn := &fakeConn{}
	tui.handleIPCInterrupt(conn, IPCRequest{Action: "interrupt", Target: "eng1"})
	var resp IPCResponse
	if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
		t.Fatalf("unparseable interrupt response %q: %v", conn.written, err)
	}
	t.Logf("INTERRUPT on suspended: OK=%v Error=%q", resp.OK, resp.Error)
	if resp.OK {
		t.Errorf("interrupt on a suspended pane reported OK=true: there is no process to interrupt, "+
			"so this is the false-success shape ini-g7fl removed. Error=%q", resp.Error)
	}
	if !strings.Contains(resp.Error, "suspended") {
		t.Errorf("interrupt feedback does not name the SUSPENDED state, so the operator cannot tell "+
			"a resumable pane from a dead one. Got %q", resp.Error)
	}
	if !strings.Contains(resp.Error, "resumes on message") {
		t.Errorf("interrupt feedback does not name the resume-on-message remedy, which is the whole "+
			"point of distinguishing suspended from dead. Got %q", resp.Error)
	}
	if strings.Contains(resp.Error, "is not running") {
		t.Errorf("interrupt feedback degraded to the generic not-running text (qa1's surviving mutant "+
			"is back). Got %q", resp.Error)
	}

	// Row 4: bead display. Degraded form: "is not alive ...; restart it", which
	// prescribes exactly the action that discards the queue.
	conn2 := &fakeConn{}
	tui.handleIPCBead(conn2, IPCRequest{Action: "bead", Target: "eng1", Text: "ini-test"})
	var resp2 IPCResponse
	if err := json.Unmarshal(conn2.written[:conn2.findNewline()], &resp2); err != nil {
		t.Fatalf("unparseable bead response %q: %v", conn2.written, err)
	}
	t.Logf("BEAD DISPLAY on suspended: OK=%v Error=%q", resp2.OK, resp2.Error)
	if resp2.OK {
		t.Errorf("bead display on a suspended pane reported OK=true; the display state is frozen "+
			"during suspension, so a silent success is a lie. Error=%q", resp2.Error)
	}
	if !strings.Contains(resp2.Error, "suspended") {
		t.Errorf("bead-display feedback does not name the SUSPENDED state. Got %q", resp2.Error)
	}
	if !strings.Contains(resp2.Error, "resumes on message") {
		t.Errorf("bead-display feedback does not name the resume-on-message remedy. Got %q", resp2.Error)
	}
	if strings.Contains(resp2.Error, "restart it") {
		t.Errorf("bead-display feedback still prescribes a restart, which discards the queue and the "+
			"suspension bookkeeping -- the exact advice ini-g7fl replaced. Got %q", resp2.Error)
	}
}

// ── ini-g7fl: per-entry-point coverage of the three former bypass sites ──
//
// The i7fr lesson, in the AC deliberately: a guard proven at one site says
// nothing about a site that does not route through it. Each former bypass
// gets a test that fails if THAT site loses the message -- all three go red
// together against a primitive-guard-deleted mutant, and each proves its own
// site actually routes through the primitive.

// suspendedFixture returns a wired TUI + a really-suspended echo pane, with
// resume-on-message wired exactly as production adoption does.
func suspendedFixture(t *testing.T) (*TUI, *Pane) {
	t.Helper()
	p := echoPane(t, "eng1")
	tui := newResourceTestTUI(100000, 5000, 80)
	tui.panes = toPaneViews([]*Pane{p})
	p.eventCh = tui.agentEvents
	tui.wireSuspendResume(p) // the production adoption wiring
	time.Sleep(500 * time.Millisecond)
	suspendViaRealPolicy(t, tui, p)
	return tui, p
}

// awaitDelivery polls for the auto-resumed pane to echo the message --
// delivery via the CALLBACK, no manual resume anywhere.
func awaitDelivery(t *testing.T, tui *TUI, marker string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(paneOutput(tui, "eng1"), "GOT:"+marker) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	// The capture is empty or wrong. Before calling that a delivery failure,
	// establish that there was a live reader to deliver TO (ini-0lko).
	switch live := childLiveness(t, tui, "eng1", marker); live {
	case livenessReading:
		t.Fatalf("FINDING (product): message %q never reached the auto-resumed process, and the "+
			"child IS alive and reading -- a liveness probe sent afterwards came back. So the "+
			"message was genuinely lost, not merely unobserved. queue-without-resume is delivery "+
			"deferred to never. Output:\n%s", marker, paneOutput(tui, "eng1"))
	default:
		t.Fatalf("RIG FAULT, not a product finding: message %q never arrived, but %s. This rig "+
			"cannot speak to delivery when there is no reader to deliver to -- fix the rig before "+
			"reading anything into this. Output:\n%s", marker, live, paneOutput(tui, "eng1"))
	}
}

// TestSuspendGuard_ForwardSendSite drives the REAL forward-delivery entry
// point (window-2-originated sends) against a suspended pane.
func TestSuspendGuard_ForwardSendSite(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	tui, _ := suspendedFixture(t)
	if err := tui.deliverForwardedSend("eng1", "via-forward", true); err != nil {
		t.Fatalf("forward delivery errored: %v", err)
	}
	awaitDelivery(t, tui, "via-forward")
}

// TestSuspendGuard_DaemonIPCSendSite drives the daemon's serve-mode IPC send
// (daemon.go handleIPCSend equivalent: findPane + SendText) against a
// suspended pane registered in a Daemon.
func TestSuspendGuard_DaemonIPCSendSite(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	tui, p := suspendedFixture(t)
	d := &Daemon{panes: []*Pane{p}}
	conn := &fakeConn{}
	d.HandleSend(conn, IPCRequest{Action: "send", Target: "eng1", Text: "via-daemon-ipc", Enter: true})
	awaitDelivery(t, tui, "via-daemon-ipc")
}

// TestSuspendGuard_DaemonControlSendSite drives the control-stream send shape
// (daemon.go: findPane + SendText in the "send" case) against a suspended pane.
func TestSuspendGuard_DaemonControlSendSite(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	tui, p := suspendedFixture(t)
	d := &Daemon{panes: []*Pane{p}}
	cp := d.findPane("eng1")
	if cp == nil {
		t.Fatal("daemon cannot find the pane")
	}
	cp.SendText("via-daemon-ctrl", true) // the exact case-body shape at daemon.go "send"
	awaitDelivery(t, tui, "via-daemon-ctrl")
}

// TestSuspendGuard_ResumeFailureKeepsQueue pins the resume-failure edge: when
// respawn fails, the queued messages SURVIVE for the next attempt and the
// failure is loud in the log -- never a silent drop.
func TestSuspendGuard_ResumeFailureKeepsQueue(t *testing.T) {
	if os.Getenv("INITECH_9IMX") != "1" {
		t.Skip("set INITECH_9IMX=1")
	}
	tui, p := suspendedFixture(t)
	// Break respawn: the rebuilt config points at a nonexistent binary.
	tui.paneConfigBuilder = func(name string) (PaneConfig, error) {
		return PaneConfig{Name: name, Command: []string{"/nonexistent/binary-g7fl"}}, nil
	}
	p.SendText("must-survive", true)
	time.Sleep(3 * time.Second) // let the async resume fail

	// Read the CURRENT pane by name: resumePane REPLACES the pane in t.panes,
	// so the original pointer's queue is the old corpse's -- the first draft
	// of this test read it and could not see the re-queue at all.
	var cur *Pane
	tui.runOnMain(func() {
		for _, pv := range tui.panes {
			if pv.Name() == "eng1" {
				cur, _ = pv.(*Pane)
			}
		}
	})
	if cur == nil {
		t.Fatal("pane vanished from t.panes")
	}
	cur.mu.Lock()
	queued := len(cur.messageQueue)
	cur.mu.Unlock()
	if queued != 1 {
		t.Fatalf("after a FAILED resume the queue holds %d messages, want 1 -- a failed respawn "+
			"must not cost the message; the next resume (any trigger) delivers it", queued)
	}
	if !cur.IsSuspended() {
		t.Fatal("failed resume left the pane un-suspended; the next send would write into the void")
	}
}

// TestClose_NilsPtmx pins the defense-in-depth half on its own layer (the
// jhm6 rule): after Close, the PTY field is nil, so any dead-pane write path
// that slips past the suspension guard hits sendPaneTextLocked's explicit
// early-return instead of writing into a closed descriptor with the error
// discarded (the closed-but-not-nil shape, measured in the 9imx triage).
// Ungated: cheap, no suspension machinery needed.
func TestClose_NilsPtmx(t *testing.T) {
	p, err := NewPane(PaneConfig{Name: "x", Command: []string{"sh", "-c", "sleep 1"}}, 24, 80)
	if err != nil {
		t.Fatalf("pane: %v", err)
	}
	p.Start()
	p.Close()
	p.mu.Lock()
	nil_ := p.ptmx == nil
	p.mu.Unlock()
	if !nil_ {
		t.Fatal("Close left ptmx non-nil; a later write goes into the closed descriptor " +
			"with the error discarded instead of hitting the guarded early-return")
	}
	p.SendText("into the void", true) // must be a safe no-op, not a panic
}
