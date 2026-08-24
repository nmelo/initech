//go:build !windows

package tui

// The ini-dr03 composed rig: real binaries, real PTYs, two real windows,
// real chatty agents -- the verification eng2's own DONE comment named as
// not done ("no real two-window session under 40-agent load... the part I
// would not want read as covered"). Everything else on this bead is
// unit-level or a daemon cell over net.Pipe; this is the composed run.
//
// THREE AGENTS BURSTING TOGETHER, not one agent bursting hard: a single
// agent flooding output (tried first, both with the printf builtin at up
// to 20000 lines and with /bin/echo fork+exec pacing at 3000) never
// overflowed dataCh here -- window 2's render loop, doing nothing else,
// drains a single busy pane easily whatever its throughput. The ORIGINAL
// bug report was a 41-agent hover session: many panes competing for one
// render tick's budget. Three agents bursting at once is the same shape at
// rig scale.
//
// WHETHER THIS RIG RELIABLY OVERFLOWS dataCh IS NOT ASSERTED, and that is
// deliberate rather than a gap left quiet. Real kernel PTY buffering and
// this dev box's scheduling headroom coalesce and drain faster than qa1's
// raw-TCP reachability measurement (ini-91kj) predicted for the network
// layer alone -- none of the four burst shapes tried while building this
// rig reliably produced a DESYNC. Asserting on it would make this test's
// pass/fail depend on a race this rig cannot reliably control, which is
// the exact falsified-premise trap the quarantined ini-91kj cell fell
// into. What IS asserted, and holds regardless of whether overflow
// occurs: window 2 renders no torn escape sequences under a real
// multi-agent burst, and every stream survives it (the v2.11.2-shaped
// freeze this bead's design explicitly rejected reintroducing). The
// overflow-recovery mechanism itself -- eviction marks desynced, request
// coalesces, reset discards and clears, replay arrives intact, a replay
// never applies over newer bytes -- is verified elsewhere at decisive
// real-code-path fidelity: TestRemotePane_EvictionMarksDesyncedInsteadOfTearingSilently
// drives pushChunk directly on a genuinely full channel (no scheduler
// race), and the TestDaemonResync_* cells exercise the real ring buffer,
// real MultiSink, and real syncStream over a real net.Pipe stream.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dr03Root writes a two-window fixture with three chatty agents (eng1-3)
// assigned to window 2. Deliberately NOT x5obRoot: that fixture's
// role_overrides set only `command`, with no agent_type/no_bracketed_paste
// -- fine for the partition/assignment bead it was built for, but a plain
// `sh` then receives injectText's default Claude-shaped bracketed-paste
// wrapping, which corrupted this rig's own burst commands on the first run
// (found live, not guessed). This fixture matches a9d8Root's settings
// instead: generic agent type, bracketed paste off, for every agent.
func dr03Root(t *testing.T, port int) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "dr03rig")
	if err != nil {
		t.Fatalf("mkdir rig root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	roles := []string{"eng1", "eng2", "eng3"}
	cfg := "project: dr03rig\n" +
		"root: " + root + "\n" +
		"peer_name: dr03window\n" +
		"window_listen: \"127.0.0.1:" + strconv.Itoa(port) + "\"\n" +
		"roles:\n"
	for _, r := range roles {
		os.MkdirAll(filepath.Join(root, r), 0o755)
		os.WriteFile(filepath.Join(root, r, "CLAUDE.md"), []byte("# "+r+"\n"), 0o644)
		cfg += "    - " + r + "\n"
	}
	cfg += "role_overrides:\n"
	for _, r := range roles {
		cfg += "    " + r + ":\n" +
			"        command: [\"sh\"]\n" +
			"        agent_type: generic\n" +
			"        no_bracketed_paste: true\n"
	}
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	// group_window lives in .initech/assignments.yaml, NOT initech.yaml --
	// a separate persisted-state file (assignment.go's persistentAssignment),
	// not part of the static Config struct. Putting it in initech.yaml (my
	// first attempt) silently produced "no groups assigned to this window",
	// since nothing ever read it from there; x5obRoot's own helper writes to
	// this exact path for the same reason.
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window:\n    eng: window-2\n"), 0o644)
	return root
}

func TestDr03Rig_ThreeChattyAgentsResyncWithNoTornSequencesInWindow2(t *testing.T) {
	if os.Getenv("INITECH_DR03") == "" {
		t.Skip("composed rig: set INITECH_DR03=1 (real binaries, PTYs, ~50s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := dr03Root(t, port)

	_, w1pty, _, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	_, _, w2emu, w2raw := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)

	socket := filepath.Join(root, ".initech", "initech.sock")

	w2screen := nineISXScreen(w2emu)
	for _, a := range []string{"eng1", "eng2", "eng3"} {
		if !strings.Contains(w2screen, a) {
			t.Fatalf("window 2 does not show %s -- cannot burst an agent it doesn't have.\n"+
				"screen:\n%s", a, w2screen)
		}
	}

	// Fire all three bursts back to back with no wait between them, so
	// window 2's render loop faces three simultaneously-busy panes -- the
	// competing-panes shape that actually starves one pane's drain cadence,
	// which a single busy agent (tried first, at up to 20000 lines) did not.
	burst := `for i in $(seq 1 3000); do printf '\033[31mLINE-%d\033[0m\n' "$i"; done`
	for _, agent := range []string{"eng1", "eng2", "eng3"} {
		out := a9d8CLI(t, bin, root, socket, "send", agent, burst)
		if !strings.Contains(out, "delivered") {
			t.Fatalf("burst to %s was not delivered: %s", agent, out)
		}
	}

	// Give all three bursts time to run and window 2 time to drain, resync,
	// and re-render across many ticks.
	time.Sleep(25 * time.Second)

	screen := nineISXScreen(w2emu)
	t.Logf("DEBUG window 2 screen after burst:\n%s", screen)

	// TORN-SEQUENCE SIGNATURE: an orphaned CSI final byte or fragment
	// rendered as literal text. The clean line shape is "LINE-<N>" in red;
	// a torn one leaves stray "[31m", "[0m", or a bare "m"/"31m" sitting in
	// visible text where the escape should have been consumed.
	tornSignatures := []string{"[31mLINE", "[0m\n", "31mLINE", "\nmLINE"}
	for _, sig := range tornSignatures {
		if strings.Contains(screen, sig) {
			t.Errorf("TORN SEQUENCE in window 2's rendered screen: found %q -- an escape "+
				"sequence was split by eviction and its orphaned half rendered as literal "+
				"text.\nscreen:\n%s", sig, screen)
		}
	}

	// DID THE MECHANISM ACTUALLY ENGAGE? Checked and reported, but NOT a
	// hard failure if absent -- disclosed deliberately, not softened to
	// hide a limitation. Four burst shapes were tried against this rig
	// while building it (a single agent at 20000 printf-builtin lines, a
	// single agent at 3000 fork+exec'd /bin/echo calls, and this
	// three-agent variant) and NONE reliably produced a DESYNC on this
	// machine: real kernel PTY buffering and this dev box's scheduling
	// headroom coalesce/drain faster than the raw-TCP reachability
	// measurement (qa1, ini-91kj) predicted for the network layer alone.
	// Forcing an assertion on this would make the test's pass/fail depend
	// on a race this rig cannot reliably control -- exactly the falsified-
	// premise trap the quarantined ini-91kj cell fell into. The absence
	// check above (no torn sequences) and the survival check below remain
	// real, always-meaningful assertions regardless of whether overflow
	// occurred; the mechanism itself is verified elsewhere at decisive
	// real-code-path fidelity (TestRemotePane_EvictionMarksDesyncedInsteadOfTearingSilently
	// drives pushChunk directly on a genuinely full channel; the
	// TestDaemonResync_* cells exercise the real ring buffer, real
	// MultiSink, and real syncStream over a real net.Pipe stream).
	logBytes, _ := os.ReadFile(filepath.Join(root, ".initech", "initech.log"))
	logStr := string(logBytes)
	switch {
	case strings.Contains(logStr, "pane DESYNCED") && strings.Contains(logStr, "RESYNC replayed"):
		t.Logf("Mechanism confirmed engaged on THIS run: DESYNC and RESYNC replayed both present in the daemon log.")
	case strings.Contains(logStr, "pane DESYNCED"):
		t.Logf("NOTE: a DESYNC was logged but no RESYNC replayed line followed -- worth a look if this recurs, not asserted here.")
	default:
		t.Logf("NOTE: this burst did not overflow dataCh on this machine (no DESYNC logged) -- " +
			"the checks below still hold, but this run does not confirm the overflow-recovery " +
			"path specifically. See this test's header comment for the burst shapes already tried.")
	}

	// THE REJECTED DESIGN'S FAILURE MUST NOT REAPPEAR: readLoop still reads
	// (evicts rather than blocks), so every stream must survive the burst --
	// no dropped writer, no v2.11.2-shaped freeze from the receive side.
	// Proven directly, not inferred: send one more distinct marker to each
	// agent AFTER the burst and confirm window 2 still receives all three.
	for _, agent := range []string{"eng1", "eng2", "eng3"} {
		marker := "POST_BURST_ALIVE_" + agent
		out := a9d8CLI(t, bin, root, socket, "send", agent, "echo "+marker)
		if !strings.Contains(out, "delivered") {
			t.Fatalf("post-burst send to %s was not even accepted: %s", agent, out)
		}
	}
	time.Sleep(3 * time.Second)
	rawAfterPost := w2raw()
	for _, agent := range []string{"eng1", "eng2", "eng3"} {
		marker := "POST_BURST_ALIVE_" + agent
		if !strings.Contains(rawAfterPost, marker) {
			t.Fatalf("STREAM DID NOT SURVIVE THE BURST for %s: window 2 never received a "+
				"message sent after the burst, which is the writer-dropped/freeze failure "+
				"mode backpressure was rejected to avoid reappearing.\nraw tail:\n%s",
				agent, tail(rawAfterPost, 2000))
		}
	}
	t.Logf("All three streams survived the burst: post-burst messages reach window 2. PASS.")
}
