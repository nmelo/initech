//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// Tests for ini-z8o: a wedged (alive-but-unresponsive) window client must be
// dropped fast enough that its monitor stops showing a frozen frame that
// looks live, WITHOUT the tuning leaking onto cross-machine peers and without
// becoming so eager that a transient stall triggers fold-back.

// ── scoping: the non-negotiable AC, both directions ─────────────────

// TestWindowYamux_TunedForWindowClients asserts the window server's sessions
// carry the tightened timeouts.
func TestWindowYamux_TunedForWindowClients(t *testing.T) {
	cfg := windowYamuxConfig()
	if cfg.KeepAliveInterval != windowKeepAliveInterval {
		t.Errorf("KeepAliveInterval = %v, want %v", cfg.KeepAliveInterval, windowKeepAliveInterval)
	}
	if cfg.ConnectionWriteTimeout != windowConnectionWriteTimeout {
		t.Errorf("ConnectionWriteTimeout = %v, want %v", cfg.ConnectionWriteTimeout, windowConnectionWriteTimeout)
	}

	// The bounds pm specified, asserted as bounds rather than as the exact
	// values, so a future retune inside the envelope does not have to edit
	// this test to stay meaningful.
	if worst := cfg.KeepAliveInterval + cfg.ConnectionWriteTimeout; worst > 15*time.Second {
		t.Errorf("worst-case detection = %v, want <= 15s", worst)
	}
	if cfg.ConnectionWriteTimeout <= 5*time.Second {
		t.Errorf("minimum time-to-drop = %v, want > 5s so a 5s transient stall cannot be dropped", cfg.ConnectionWriteTimeout)
	}
}

// TestWindowYamux_RemotePathKeepsDefaults is the other direction, and the one
// that actually guards the shared transport: a daemon that is NOT the window
// server must hand yamux the untouched defaults.
//
// Compared against yamux.DefaultConfig() read live rather than against
// literal 30s/10s, so this tracks an upstream default change instead of
// pinning a stale copy of it -- the assertion is "unchanged from the
// library's default", which is what the AC actually says.
func TestWindowYamux_RemotePathKeepsDefaults(t *testing.T) {
	remote := &Daemon{} // what RunDaemon builds: yamuxCfg never set
	got := remote.yamuxConfig()
	def := yamux.DefaultConfig()

	if got.KeepAliveInterval != def.KeepAliveInterval {
		t.Errorf("remote KeepAliveInterval = %v, want yamux default %v -- the window tuning leaked onto the cross-machine path",
			got.KeepAliveInterval, def.KeepAliveInterval)
	}
	if got.ConnectionWriteTimeout != def.ConnectionWriteTimeout {
		t.Errorf("remote ConnectionWriteTimeout = %v, want yamux default %v -- the window tuning leaked onto the cross-machine path",
			got.ConnectionWriteTimeout, def.ConnectionWriteTimeout)
	}

	// And the tuned values must actually differ from the defaults, or the
	// two assertions above would pass vacuously if someone set the window
	// config equal to the defaults.
	win := windowYamuxConfig()
	if win.KeepAliveInterval == def.KeepAliveInterval && win.ConnectionWriteTimeout == def.ConnectionWriteTimeout {
		t.Error("window config equals the defaults, making the scoping assertions vacuous")
	}
}

// TestWindowServer_UsesTunedConfigForItsSessions closes the gap between "the
// helper returns tuned values" and "the server actually installs them": it
// checks the daemon instance startWindowServer built, not the helper.
func TestWindowServer_UsesTunedConfigForItsSessions(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, _ := startTestWindowServer(t, panes)

	got := ws.daemon.yamuxConfig()
	if got.KeepAliveInterval != windowKeepAliveInterval || got.ConnectionWriteTimeout != windowConnectionWriteTimeout {
		t.Errorf("window server daemon config = %v/%v, want %v/%v",
			got.KeepAliveInterval, got.ConnectionWriteTimeout,
			windowKeepAliveInterval, windowConnectionWriteTimeout)
	}
}

// ── wedge detection, with a REAL external process ───────────────────

// TestWindowWedge_SigstoppedWindowFoldsBackWithinBound measures the actual
// improvement: a real attached client is SIGSTOPped, so its kernel keeps
// ACKing TCP while the application answers nothing. Only the yamux pong stops,
// which is precisely the no-signal case ini-9ka.7's close/crash detection
// correctly excluded.
//
// Uses a real helper process (eng1's foldback pattern): an in-process fake
// cannot be SIGSTOPped and would exercise the clean-close path in disguise.
func TestWindowWedge_SigstoppedWindowFoldsBackWithinBound(t *testing.T) {
	skipWedgeInShort(t)
	ws, addr, a, groupOf := foldbackServerFixture(t)

	helper := startWedgeHelper(t, addr)
	waitForClients(t, ws, 1)
	if ownerOfAgent("eng1", a, groupOf, ws.connectedWindows()) == WindowOne {
		t.Fatal("window 1 rendered window2's agent while the helper was healthy")
	}

	// SIGSTOP: process alive, socket open, application frozen.
	if err := helper.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("SIGSTOP helper: %v", err)
	}

	start := time.Now()
	deadline := 15 * time.Second
	folded := false
	for time.Since(start) < deadline+3*time.Second {
		if ownerOfAgent("eng1", a, groupOf, ws.connectedWindows()) == WindowOne {
			folded = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	took := time.Since(start)

	if !folded {
		t.Fatalf("wedged window never folded back within %v (was 40.03s before tuning)", took)
	}
	if took > deadline {
		t.Errorf("wedged window folded back in %v, want <= %v", took, deadline)
	}
	t.Logf("SIGSTOP -> fold-back in %v (baseline was 40.03s; bound is %v)", took, deadline)
}

// TestWindowWedge_TransientStallDoesNotFoldBack is the false-positive guard.
// A tuned keepalive's new failure mode is eagerness: a window busy under heavy
// render load must not be mistaken for a wedged one. SIGSTOP followed by
// SIGCONT inside the 5s tolerance must leave the window connected.
func TestWindowWedge_TransientStallDoesNotFoldBack(t *testing.T) {
	skipWedgeInShort(t)
	ws, addr, a, groupOf := foldbackServerFixture(t)

	helper := startWedgeHelper(t, addr)
	waitForClients(t, ws, 1)

	if err := helper.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("SIGSTOP helper: %v", err)
	}
	time.Sleep(5 * time.Second) // The full tolerance the AC specifies.
	if err := helper.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("SIGCONT helper: %v", err)
	}

	// Give the session a moment to prove it survived rather than to recover.
	time.Sleep(1 * time.Second)

	if ownerOfAgent("eng1", a, groupOf, ws.connectedWindows()) == WindowOne {
		t.Errorf("a 5s transient stall folded back -- the tuning is too eager (min time-to-drop is %v)", windowConnectionWriteTimeout)
	}
	if got := len(ws.connectedWindows()); got != 1 {
		t.Errorf("connected windows = %d after a recovered stall, want 1", got)
	}
}

// TestWindowWedge_RecoveredWedgeReattachesCleanly covers the last AC: a window
// dropped as wedged that later recovers is an ordinary reattach candidate,
// handled by the existing ini-9ka.7 path with no wedge-specific state.
func TestWindowWedge_RecoveredWedgeReattachesCleanly(t *testing.T) {
	skipWedgeInShort(t)
	ws, addr, a, groupOf := foldbackServerFixture(t)

	helper := startWedgeHelper(t, addr)
	waitForClients(t, ws, 1)
	if err := helper.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("SIGSTOP helper: %v", err)
	}

	// Wait out the drop.
	start := time.Now()
	for time.Since(start) < 18*time.Second {
		if ownerOfAgent("eng1", a, groupOf, ws.connectedWindows()) == WindowOne {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !(ownerOfAgent("eng1", a, groupOf, ws.connectedWindows()) == WindowOne) {
		t.Fatal("precondition: wedged window was never dropped")
	}
	_ = helper.Process.Signal(syscall.SIGCONT)
	_ = helper.Process.Kill()

	// Reattach through the ORDINARY path -- same dial any window uses.
	session, ctrl, _ := dialWindow(t, addr, "window2")
	defer ctrl.Close()
	defer session.Close()
	waitForClients(t, ws, 1)

	if ownerOfAgent("eng1", a, groupOf, ws.connectedWindows()) == WindowOne {
		t.Error("after reattach, window 1 still renders window2's agent -- the split did not restore")
	}
	// Assignment is untouched by the whole wedge/recover cycle.
	if got := a.WindowOfGroup("eng"); got != "window2" {
		t.Errorf("assignment for eng = %q after wedge+reattach, want window2 (unchanged)", got)
	}
}

// startWedgeHelper launches a real attached window client and returns it.
func startWedgeHelper(t *testing.T, addr string) *exec.Cmd {
	t.Helper()
	helper := exec.Command(os.Args[0], "-test.run=TestFoldbackHelperAttachAndIdle")
	helper.Env = append(os.Environ(), "INITECH_FOLDBACK_HELPER=1", "INITECH_FOLDBACK_ADDR="+addr)
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper client: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Signal(syscall.SIGCONT) // never leave one stopped
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	return helper
}

// skipWedgeInShort skips the timing-bound wedge tests under -short and in CI.
// They cost real seconds by construction: the thing under test IS a timeout,
// so it cannot be made fast without testing something else.
func skipWedgeInShort(t *testing.T) {
	t.Helper()
	if testing.Short() || os.Getenv("CI") != "" {
		t.Skip("wedge detection test: measures real keepalive timeouts, run locally")
	}
}
