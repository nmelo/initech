//go:build !windows

package tui

// The ini-a9d8 composed rig, real binary and real PTY, exercising the one
// cell eng1 flagged as ARGUED-NOT-MEASURED: paste to a SUSPENDED remote
// agent. Plus the awake case as its baseline.
//
// WHY THIS DRIVES `initech send --no-enter` RATHER THAN A SECOND WINDOW
// PASTING INTO THE FIRST. An earlier version of this rig spawned two real
// TUI windows and injected a raw bracketed-paste escape sequence
// (ESC[200~...ESC[201~) into window 2's own pty input, meaning to exercise
// handlePaste -> FlushPaste through tcell's real input parser. That
// parser layer is orthogonal to what ini-a9d8 changed and is already
// unit-tested on its own terms (paste_test.go); what it is NOT a
// substitute for is verifying what this bead's own DONE comment flagged as
// UNMEASURED: whether the daemon-side suspend/queue/resume path correctly
// carries Enter:false end to end against a REAL process.
//
// RemotePane.FlushPaste (ipc.go, remote_pane.go) constructs
// ControlCmd{Action:"send", Enter:false} and sends it over the control mux
// -- already unit-tested exactly (TestPaste_RemotePaneSendsTheBodyUnsubmitted).
// On the daemon side that command reaches Pane.SendText(text, false)
// (daemon.go's "send" case), which is the SAME primitive local sends,
// forwarded sends, and `initech send --no-enter` all go through -- the
// suspension guard lives at the PRIMITIVE (ini-g7fl, pane.go SendText) for
// exactly this reason: every entry point inherits it without having to
// remember it exists. So `initech send eng1 "..." --no-enter` against a
// real suspended agent exercises the identical daemon-side mechanism
// FlushPaste's wire request would hit -- the untested gap eng1 named --
// without needing a second window or a paste event at all.
//
// WHY NOT A HEADLESS DAEMON. Tried first, and it cannot do this: `initech
// suspend` requires Daemon.HandleExtended, which is `return false`
// unconditionally (daemon.go) -- suspend/resume management is TUI-only by
// design, exposed on a real (non-headless) window's own local socket. So
// this rig runs ONE real (non-headless) TUI process, driven entirely
// through its own IPC socket via the real `initech` CLI (send, suspend,
// peek, status) -- no keyboard/paste emulation anywhere in this file.
//
// A shell mock agent is the right substitute here, not a compromise: this
// bead is about whether text reaches a suspended-then-resumed agent
// UNSUBMITTED through orchestration machinery with nothing Claude-specific
// in it. sh's own cooked-mode PTY line-buffering gives a decisive,
// shell-agnostic signal: a send with no Enter cannot execute, whatever the
// shell is, so a marker command's OUTPUT appearing is unambiguous evidence
// of a wrongly-submitted send.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// a9d8Root writes a minimal one-agent project: a real shell, generic agent
// type with bracketed paste off (the daemon injects raw bytes into `sh`,
// which understands none of Claude's paste protocol), and a window_listen
// port so the process is a full TUI with its own IPC socket -- not headless,
// which is the whole reason this setup works where a headless daemon does
// not.
func a9d8Root(t *testing.T, port int) string {
	t.Helper()
	// A SHORT root under plain /tmp, never t.TempDir() and never
	// os.MkdirTemp("", ...) (which honors $TMPDIR -- on macOS that resolves
	// to /var/folders/.../T/, itself already most of the budget below): the
	// socket lives at <root>/.initech/initech.sock and macOS caps AF_UNIX
	// sun_path at 104 bytes. t.TempDir() additionally embeds the test's
	// full name, which blows the budget outright and fails as the
	// unhelpful "bind: invalid argument" -- this fleet's own remote-daemon
	// rig procedure names the same trap.
	root, err := os.MkdirTemp("/tmp", "a9d8rig")
	if err != nil {
		t.Fatalf("mkdir rig root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	os.MkdirAll(filepath.Join(root, "eng1"), 0o755)
	os.WriteFile(filepath.Join(root, "eng1", "CLAUDE.md"), []byte("# eng1\n"), 0o644)
	cfg := "project: a9d8rig\n" +
		"root: " + root + "\n" +
		"peer_name: a9d8window\n" +
		"window_listen: \"127.0.0.1:" + strconv.Itoa(port) + "\"\n" +
		"roles:\n" +
		"    - eng1\n" +
		"role_overrides:\n" +
		"    eng1:\n" +
		"        command: [\"sh\"]\n" +
		"        agent_type: generic\n" +
		"        no_bracketed_paste: true\n"
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)
	return root
}

// a9d8CLI runs the built binary against the rig's own socket, exactly as an
// operator's shell would: cwd inside the rig root and INITECH_SOCKET set
// explicitly in the SAME exec.Cmd, never relying on an inherited or
// ambient value (the ini-grg3 live-fleet leak this fleet's own CLAUDE.md
// warns about). --allow-dev-delivery is required because this binary is a
// local test build, and this rig is the isolated session that flag exists
// for.
func a9d8CLI(t *testing.T, bin, root, socket string, args ...string) string {
	t.Helper()
	full := append([]string{"--allow-dev-delivery"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"INITECH_SOCKET=" + socket,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("a9d8CLI %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestA9d8Rig_PasteEquivalentReachesAwakeAndSuspendedAgent covers both
// cells: an awake send with no Enter lands unsubmitted (baseline, already
// unit-tested, re-confirmed live here), then the SAME send to that agent
// once suspended queues and is delivered unsubmitted after the automatic
// resume the primitive itself triggers (Pane.SendText's suspended branch
// calls the resume callback directly -- there is no separate wake step to
// script).
func TestA9d8Rig_PasteEquivalentReachesAwakeAndSuspendedAgent(t *testing.T) {
	if os.Getenv("INITECH_A9D8") == "" {
		t.Skip("composed rig: set INITECH_A9D8=1 (real binary, real PTY, ~35s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := a9d8Root(t, port)

	_, pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(6 * time.Second)
	pty.Write([]byte("n")) // decline the consent overlay, if one is showing
	time.Sleep(2 * time.Second)

	socket := filepath.Join(root, ".initech", "initech.sock")
	deadline := time.Now().Add(15 * time.Second)
	var statErr error
	for time.Now().Before(deadline) {
		if _, statErr = os.Stat(socket); statErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if statErr != nil {
		logPath := filepath.Join(root, ".initech", "initech.log")
		logBytes, _ := os.ReadFile(logPath)
		t.Fatalf("rig socket never appeared at %s: %v -- window never started up.\n"+
			"window screen:\n%s\ninitech.log:\n%s",
			socket, statErr, nineISXScreen(w1emu), tail(string(logBytes), 3000))
	}

	// ── CASE 1: AWAKE.
	awakeMarker := "AWAKE_9F3K"
	out := a9d8CLI(t, bin, root, socket, "send", "eng1", "echo "+awakeMarker, "--no-enter")
	if !strings.Contains(out, "delivered") {
		t.Fatalf("send --no-enter did not report delivery: %s", out)
	}
	time.Sleep(2 * time.Second)

	peeked := a9d8CLI(t, bin, root, socket, "peek", "eng1", "-n", "10")
	if !strings.Contains(peeked, "echo "+awakeMarker) {
		t.Fatalf("AWAKE CASE: eng1's pane never shows the sent text %q -- it did not reach "+
			"the agent at all.\npeek:\n%s", "echo "+awakeMarker, peeked)
	}
	if strings.Contains(peeked, "\n"+awakeMarker+"\n") {
		t.Fatalf("AWAKE CASE: the text EXECUTED (marker %q appears as command output, not "+
			"just as sent text) -- a no-enter send must land unsubmitted, the operator "+
			"presses enter themselves.\npeek:\n%s", awakeMarker, peeked)
	}
	t.Logf("AWAKE CASE: no-enter send reached eng1 and did not execute. PASS.")

	// POSITIVE CONTROL: prove the instrument can see execution when it
	// genuinely happens, or the absence check above proves nothing. An
	// empty-text send with Enter (no --no-enter) submits the pending line.
	a9d8CLI(t, bin, root, socket, "send", "eng1", "")
	time.Sleep(2 * time.Second)
	peeked = a9d8CLI(t, bin, root, socket, "peek", "eng1", "-n", "10")
	if !strings.Contains(peeked, "\n"+awakeMarker+"\n") {
		t.Fatalf("INSTRUMENT CHECK FAILED: submitting the pending line did not produce the "+
			"marker's output -- the absence assertion above measured nothing, since this "+
			"instrument cannot see execution even when it happens.\npeek:\n%s", peeked)
	}
	t.Logf("POSITIVE CONTROL: instrument correctly sees execution when it happens.")

	// ── CASE 2: SUSPENDED, the cell eng1 flagged as argued-not-measured.
	suspOut := a9d8CLI(t, bin, root, socket, "suspend", "eng1")
	if !strings.Contains(suspOut, "suspended eng1") {
		t.Fatalf("SUSPEND SETUP: suspend did not report success -- cannot trust the rest "+
			"of this case without confirming the suspend actually landed.\noutput:\n%s", suspOut)
	}
	t.Logf("SUSPEND SETUP: eng1 confirmed suspended.")

	// The no-enter send itself is what wakes the agent: Pane.SendText's
	// suspended branch enqueues AND calls the resume callback directly --
	// there is no separate wake step to script.
	suspMarker := "SUSP_7QX2"
	sendOut := a9d8CLI(t, bin, root, socket, "send", "eng1", "echo "+suspMarker, "--no-enter")
	if !strings.Contains(sendOut, "delivered") {
		t.Fatalf("send --no-enter to a suspended agent did not report delivery: %s", sendOut)
	}

	// Give the auto-resume (spawn + waitForInit + queue drain) real time --
	// resource.go documents ~1.1s measured on real Claude for a keystroke
	// wake; a fresh spawn plus queue drain is not faster.
	time.Sleep(8 * time.Second)

	peeked = a9d8CLI(t, bin, root, socket, "peek", "eng1", "-n", "20")
	if !strings.Contains(peeked, "echo "+suspMarker) {
		t.Fatalf("SUSPENDED CASE: the queued send never reached eng1 after resume -- it was "+
			"lost in the suspend/queue/resume path rather than delivered unsubmitted.\n"+
			"peek:\n%s", peeked)
	}
	if strings.Contains(peeked, "\n"+suspMarker+"\n") {
		t.Fatalf("SUSPENDED CASE: the queued send EXECUTED after resume (marker output "+
			"present) instead of landing unsubmitted -- Enter:false was not preserved "+
			"through the suspend/queue/resume path.\npeek:\n%s", peeked)
	}
	t.Logf("SUSPENDED CASE: queued send delivered unsubmitted after automatic resume. PASS.")

	statusOut := a9d8CLI(t, bin, root, socket, "status")
	if !strings.Contains(statusOut, "eng1") || !strings.Contains(statusOut, "yes") {
		t.Fatalf("SUSPENDED CASE: eng1 does not show alive after the auto-resume, which "+
			"undermines trust in the delivery above even though the text arrived.\n"+
			"status:\n%s", statusOut)
	}

	// FINAL POSITIVE CONTROL: the resumed agent is a genuinely live process,
	// not a pane that merely LOOKS resumed. Submit the queued line for real.
	a9d8CLI(t, bin, root, socket, "send", "eng1", "")
	time.Sleep(2 * time.Second)
	peeked = a9d8CLI(t, bin, root, socket, "peek", "eng1", "-n", "20")
	if !strings.Contains(peeked, "\n"+suspMarker+"\n") {
		t.Fatalf("FINAL CONTROL: submitting the queued line post-resume did not produce "+
			"the marker's output -- the resumed process cannot execute at all, which "+
			"would undermine the delivery claim above.\npeek:\n%s", peeked)
	}
	t.Logf("FINAL CONTROL: resumed agent is genuinely live and executes. PASS.")
}

// tail returns the last n bytes of s (or all of s if shorter), for readable
// failure output on long log captures.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
