package tui

import (
	"errors"
	"go/ast"
	"net"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Read the child's side of the PTY with the real emulator response loop
// running. Both operator keys and the asynchronous DND drain end up here.
func dndKeyPane(t *testing.T, name string) (*Pane, *pipeRecord) {
	t.Helper()
	p := testPane(name)
	r, w := memPipe(t)
	p.ptmx = w
	p.startupSettled = true
	rec := recordPipe(t, r)
	go p.responseLoop()
	t.Cleanup(func() { p.emu.Close() })
	return p, rec
}

func dndKeyBar(t *testing.T, tu *TUI, screen tcell.SimulationScreen) string {
	t.Helper()
	tu.renderStatusBar()
	screen.Show()
	rows := screenRows(t, screen)
	return strings.TrimSpace(rows[len(rows)-1])
}

func TestDNDKey_TerminalBytesTargetTheDrawnPane(t *testing.T) {
	for _, tc := range []struct{ name, hold, deleteWord string }{
		{"legacy", "\x1bh", "\x1bd"},
		{"kitty", "\x1b[104;3u", "\x1b[100;3u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				selected, child := dndKeyPane(t, "selected")
				other := testPane("other")
				tu := newTestTUI(other, selected)
				tu.layoutState.Mode = Layout2Col
				tu.layoutState.Focused = selected.Name()
				tu.applyLayout()
				if len(tu.plan.Panes) != 2 || tu.inputPane() != selected {
					t.Fatal("fixture did not draw the split with selected focused")
				}
				// Input belongs to the last drawn frame, even if focus intent has changed.
				tu.layoutState.Focused = other.Name()
				tu.handleKey(decodeTerminalBytes(t, tc.hold))
				synctest.Wait()
				if got := child.take(); got != "" {
					t.Errorf("hold chord typed %q", got)
				}
				dndSetActivity(selected, StateIdle)
				if !selected.InDND() || selected.endDNDIfDue(time.Now()) || other.InDND() {
					t.Error("want only the selected pane held across its idle turn end")
				}

				// This operator chord must still reach Claude, on the same pipe.
				tu.handleKey(decodeTerminalBytes(t, tc.deleteWord))
				synctest.Wait()
				if got := child.take(); got != "\x1bd" {
					t.Fatalf("delete-word chord wrote %q", got)
				}
			})
		})
	}
}

func TestDNDKey_OnlyTheExactChordToggles(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, child := dndKeyPane(t, "eng")
		tu := newTestTUI(p)
		tu.applyLayout()
		for _, raw := range []string{"h", "˙", "\x1b[104;4u"} { // text, macOS text, Shift+Alt+h
			tu.handleKey(decodeTerminalBytes(t, raw))
			synctest.Wait()
			if got := child.take(); got == "" || p.InDND() {
				t.Fatalf("input %q: child got %q, pane held %v", raw, got, p.InDND())
			}
		}
	})
}

func TestDNDKey_OffReleasesHeldMailInOrder(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, child := dndKeyPane(t, "eng")
		tu, screen := simTUI(t, 90, 15, p)
		tu.applyLayout()
		chord := tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModAlt)
		tu.handleKey(chord)
		if got := dndKeyBar(t, tu, screen); got != "dnd on, until off" {
			t.Fatalf("on confirmation = %q", got)
		}
		time.Sleep(time.Second - time.Nanosecond) // synctest's virtual clock
		if got := dndKeyBar(t, tu, screen); got != "dnd on, until off" {
			t.Fatalf("confirmation ended early: %q", got)
		}
		time.Sleep(time.Nanosecond)
		if got := dndKeyBar(t, tu, screen); strings.Contains(got, "dnd on, until off") {
			t.Fatal("confirmation still showing at one second")
		}
		for _, msg := range []string{"[from peer] first", "[from peer] second"} {
			if v := p.SendTextVerdictFrom(msg, false, sendOrigin{From: "peer"}); v.Outcome != DeliveryHeld {
				t.Fatalf("send %q: %+v", msg, v)
			}
		}
		synctest.Wait()
		if got := child.take(); got != "" {
			t.Fatalf("held messages reached the child: %q", got)
		}
		// Stop at the real drain's launch, then run it to completion.
		var drain func()
		p.safeGo = func(run func()) { drain = run }
		tu.handleKey(chord)
		if got := dndKeyBar(t, tu, screen); got != "dnd off, 2 held released" {
			t.Errorf("off confirmation = %q", got)
		}
		if p.InDND() || drain == nil {
			t.Fatal("off failed to release held mail to the normal drain")
		}
		drain()
		synctest.Wait()
		got := child.take()
		if first, second := strings.Index(got, "[from peer] first"), strings.Index(got, "[from peer] second"); first < 0 || second <= first ||
			strings.Count(got, "[from peer]") != 2 || strings.Contains(got, "\x1bh") || p.QueueLen() != 0 {
			t.Fatalf("off typed %q, queue length %d", got, p.QueueLen())
		}
	})
}

func TestDNDKey_OffWithNoHeldMail(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _ := dndKeyPane(t, "eng")
		tu, screen := simTUI(t, 90, 15, p)
		tu.applyLayout()
		// An existing timed hold from the CLI must be turned off too.
		if resp := dndCall(t, func(c net.Conn) {
			dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: p.Name(), Text: "on", ForSec: 60}, nil)
		}); !resp.OK {
			t.Fatal(resp.Error)
		}
		tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModAlt))
		if got := dndKeyBar(t, tu, screen); got != "dnd off" || p.InDND() {
			t.Fatalf("empty release: bar %q, held %v", got, p.InDND())
		}
	})
}

func TestDNDKey_EmptySelectionPreservesCommandBar(t *testing.T) {
	p := testPane("undrawn")
	tu, screen := simTUI(t, 90, 15, p)
	tu.cmd.error = "previous command failed"
	before := dndKeyBar(t, tu, screen)
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModAlt))
	if got := dndKeyBar(t, tu, screen); got != before || p.InDND() {
		t.Fatalf("empty selection: bar %q -> %q, undrawn pane held: %v", before, got, p.InDND())
	}
}

func TestDNDKey_SuspendedPaneStaysParked(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p := testPane("parked")
		tu := newTestTUI(p)
		tu.applyLayout()
		setSuspendedForTest(tu, p.Name(), true)
		spawns := 0
		tu.paneSpawner = func(PaneConfig, int, int) (*Pane, error) {
			spawns++
			return nil, errors.New("unexpected wake")
		}
		tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModAlt))
		synctest.Wait()
		if spawns != 0 || p.IsWaking() || !p.IsSuspended() || p.dndHold() != "off" {
			t.Fatalf("spawns=%d waking=%v suspended=%v hold=%q", spawns, p.IsWaking(), p.IsSuspended(), p.dndHold())
		}
	})
}

func TestDNDKey_RemotePaneShowsCommandRefusal(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		local, peer := net.Pipe()
		t.Cleanup(func() { local.Close(); peer.Close() })
		child := recordPipe(t, peer)
		p := &RemotePane{name: "other", stream: local}
		tu, screen := simTUI(t, 110, 15)
		tu.plan.Panes = []PaneRender{{Pane: p, Focused: true}}
		tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModAlt))
		synctest.Wait()
		if got := child.take(); got != "" {
			t.Errorf("key reached remote stream: %q", got)
		}
		if got := dndKeyBar(t, tu, screen); got != "other runs in another window or on another machine; run initech dnd there" {
			t.Errorf("remote refusal = %q", got)
		}
		tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt))
		synctest.Wait()
		if got := child.take(); got != "\x1bd" {
			t.Errorf("remote delete-word chord wrote %q", got)
		}
	})
}

func TestDNDKey_NarrowRibbonAndHelp(t *testing.T) {
	p := dndRibbonPane()
	p.name = "long-agent-name"
	p.region.W = 12
	p.startDND()
	if got := dndRibbon(t, p); !strings.Contains(got[:12], "dnd") {
		t.Errorf("12-column ribbon = %q", got[:12])
	}
	if !slices.ContainsFunc(getHelpLines(), func(line string) bool {
		return strings.Contains(line, modKey+"+h ") && strings.Contains(line, "do-not-disturb")
	}) {
		t.Error("missing do-not-disturb shortcut in help")
	}
}

func TestDNDKey_CommandAndKeyShareAction(t *testing.T) {
	var callers []string
	inventoryCalls(t, func(fn, _ string, _ bool, call *ast.CallExpr) {
		if selectorPath(call.Fun) == "applyDND" {
			callers = append(callers, fn)
		}
	})
	slices.Sort(callers)
	if !slices.Equal(callers, []string{"TUI.toggleFocusedDND", "handleIPCDND"}) {
		t.Fatalf("shared DND action called from %v", callers)
	}
}
