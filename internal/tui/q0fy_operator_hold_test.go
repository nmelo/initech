package tui

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
)

// ini-q0fy: a key the operator types while a message is being typed into the
// same pane (between its first byte and its Enter) is held, and reaches the
// child after the Enter, in arrival order. Driven through the whole send
// (sendPaneTextLocked) against the fake Claude, in a synctest bubble.

const q0fyBody = "status: the build is green, about 8."

// q0fyRig is a send rig whose fake Claude runs during, once the send's paste
// ends and before it paints the body, standing for the operator typing in
// the 0.68 s the CTO's log measured between the hold's release and the submit.
func q0fyRig(t *testing.T, during func(p *Pane)) (*Pane, *pkinFake) {
	t.Helper()
	f := &pkinFake{body: q0fyBody, after: "❯ ", takes: func(int) bool { return true }}
	var once sync.Once
	f.pasted = func() { once.Do(func() { during(f.p) }) }
	p, _ := pkinSendRig(t, f)
	return p, f
}

// q0fyOutside is everything the child read outside a bracketed paste.
func q0fyOutside(f *pkinFake) string {
	all, _, _ := f.stream()
	return outsidePaste(all)
}

func q0fyKey(r rune) *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }

// AC1, AC2: a key typed between the paste and the Enter is not submitted
// with the body; it reaches the box after the Enter.
func TestOperatorKeyBetweenPasteAndEnter_LandsAfterTheEnter(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, f := q0fyRig(t, func(p *Pane) { p.SendKey(q0fyKey('y')) })
		if v := pkinSend(p, q0fyBody); v.Outcome != DeliveryDelivered {
			t.Fatalf("verdict %+v", v)
		}
		synctest.Wait()
		if got := q0fyOutside(f); got != "\x13\ry" {
			t.Fatalf("outside the paste the child read %q, want the stash key, the Enter, then the operator's y", got)
		}
	})
}

// q0fyAfterEnter is what the child read after the send's first Enter (the
// whole stream, pastes included), and whether there was an Enter.
func q0fyAfterEnter(f *pkinFake) (string, bool) {
	all, _, _ := f.stream()
	end := strings.Index(all, "\x1b[201~")
	if end < 0 {
		return "", false
	}
	rest := all[end+len("\x1b[201~"):]
	i := strings.Index(rest, "\r")
	if i < 0 {
		return "", false
	}
	return rest[i+1:], true
}

// AC3: each operator input path is held while the send types and reaches
// the child after its Enter. Window 1's key and paste, Shift+Enter, a mouse
// click, another window's stream.
func TestOperatorInputEachPath_IsHeldUntilTheEnter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(p *Pane)
		input func(p *Pane)
		want  string
	}{
		{"window 1 key", nil, func(p *Pane) { p.SendKey(q0fyKey('y')) }, "y"},
		{"shift+enter", nil, func(p *Pane) { p.SendKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift)) }, "\x1b[13;2u"},
		{"window 1 paste", nil, func(p *Pane) { p.FlushPaste([]byte("pasted")) }, "\x1b[200~pasted\x1b[201~"},
		{"mouse click", func(p *Pane) { p.emu.Write([]byte("\x1b[?1000h")) },
			func(p *Pane) { p.ForwardMouse(uv.MouseClickEvent(uv.Mouse{X: 2, Y: 1, Button: uv.MouseLeft})) }, "\x1b[M"},
		{"another window's stream", nil, func(p *Pane) { p.streamInput([]byte("zz")) }, "zz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				p, f := q0fyRig(t, tc.input)
				if tc.setup != nil {
					tc.setup(p)
				}
				pkinSend(p, q0fyBody)
				synctest.Wait()
				after, ok := q0fyAfterEnter(f)
				if !ok || !strings.HasPrefix(after, tc.want) {
					all, _, _ := f.stream()
					t.Fatalf("after the Enter the child read %q (Enter seen %v), want it to start %q; whole stream %q", after, ok, tc.want, all)
				}
				if n := p.heldOperatorInput(); n != 0 {
					t.Errorf("%d pieces still held after the send", n)
				}
			})
		})
	}
}

// AC3, the app's and companion's keystroke send: it is itself a send, so it
// waits on sendMu for the send in flight and lands after its Enter. Outside
// a synctest bubble: a goroutine waiting on a sync.Mutex is not durably
// blocked, so a bubble's clock would never pass the first send's own sleeps.
// The test itself waits on an event (the WaitGroup); its text is the body's,
// so the fake's echo confirms it at once.
func TestOperatorTextFromTheApp_LandsAfterTheSendInFlight(t *testing.T) {
	var wg sync.WaitGroup
	p, f := q0fyRig(t, func(p *Pane) {
		wg.Add(1)
		go func() { defer wg.Done(); p.SendOperatorText(q0fyBody, true) }()
	})
	pkinSend(p, q0fyBody)
	wg.Wait()
	after, ok := q0fyAfterEnter(f)
	if !ok || !strings.HasPrefix(after, "\x13\x1b[200~"+q0fyBody+"\x1b[201~") {
		t.Fatalf("after the Enter the child read %q, want the app's send (its stash key and paste) there", after)
	}
}

// Condition 2: nothing dropped, nothing reordered. Keys, Esc, a paste,
// another window's bytes and an operator Enter, held together, reach the
// child in the order they came.
func TestHeldOperatorInput_IsReleasedInArrivalOrder(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, f := q0fyRig(t, func(p *Pane) {
			p.SendKey(q0fyKey('a'))
			p.SendKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
			p.FlushPaste([]byte("P"))
			p.streamInput([]byte("s"))
			p.SendKey(q0fyKey('b'))
			p.SendKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		})
		pkinSend(p, q0fyBody)
		synctest.Wait()
		after, _ := q0fyAfterEnter(f)
		if want := "a\x1b\x1b[200~P\x1b[201~sb\r"; after != want {
			t.Fatalf("after the Enter the child read %q, want %q", after, want)
		}
	})
}

// AC6: a send that fails after its first byte (its body cannot be written)
// releases every held key, in order.
func TestASendThatFailsMidWay_ReleasesTheHeldKeysInOrder(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		f := &pkinFake{body: q0fyBody, after: "❯ ", takes: func(int) bool { return true }}
		var once sync.Once
		f.key = func(b string) {
			if !strings.Contains(b, "\x13") {
				return
			}
			once.Do(func() {
				f.p.SendKey(q0fyKey('a'))
				f.p.SendKey(q0fyKey('b'))
				f.p.input.mu.Lock()
				f.p.input.closed = true // the body's write is refused
				f.p.input.mu.Unlock()
			})
		}
		p, _ := pkinSendRig(t, f)
		if v := pkinSend(p, q0fyBody); v.Outcome == DeliveryDelivered {
			t.Fatalf("the send did not fail: %+v", v)
		}
		synctest.Wait()
		if got := q0fyOutside(f); got != "\x13ab" {
			t.Fatalf("the child read %q, want the stash key, then a and b", got)
		}
		if n := p.heldOperatorInput(); n != 0 {
			t.Errorf("%d keys still held", n)
		}
	})
}

// Condition 4: a key held during a send still counts as typing, so the next
// message waits for the operator's pause.
func TestAKeyHeldDuringASend_StillHoldsTheNextMessage(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _ := q0fyRig(t, func(p *Pane) { p.SendKey(q0fyKey('y')) })
		pkinSend(p, q0fyBody)
		var returned atomic.Bool
		done := make(chan struct{})
		go func() {
			release := p.waitTypingHold()
			returned.Store(true)
			release()
			close(done)
		}()
		synctest.Wait()
		if returned.Load() {
			t.Fatal("the next message went at once, though the operator typed during the send")
		}
		<-done // the pause comes on the bubble's clock
	})
}

// The agent send (Codex) holds the operator's input the same way.
func TestAgentSend_HoldsTheOperatorKeyUntilTheEnter(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		const msg = "Reply with exactly Amber Receipt. Use no tools."
		idle := agentCapture(t, "local-codex-0170-idle")
		app, p, f := agentCaptureRig(t, "codex", idle)
		f.body = msg
		f.shows = strings.Replace(idle, "› Ask Codex to do anything", "› "+msg, 1)
		f.after = "› " + msg + "\n\n" + idle
		var once sync.Once
		f.pasted = func() { once.Do(func() { f.p.SendKey(q0fyKey('y')) }) }
		if v := v69Send(t, app, p.Name(), msg, true); v.Delivery != DeliveryDelivered {
			t.Fatalf("verdict %+v", v)
		}
		synctest.Wait()
		if got := q0fyOutside(f); got != "\ry" {
			t.Fatalf("outside the paste the child read %q, want the Enter, then y", got)
		}
	})
}

// Feed it nothing: with no send in flight, a key reaches the child at once,
// and nothing is held; after a send ends, the next key goes straight in.
func TestWithNoSendInFlight_AKeyGoesInAtOnce(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, f := q0fyRig(t, func(p *Pane) {})
		p.SendKey(q0fyKey('k'))
		synctest.Wait()
		if got := q0fyOutside(f); got != "k" || p.heldOperatorInput() != 0 {
			t.Fatalf("the child read %q, %d held; want k at once", got, p.heldOperatorInput())
		}
		pkinSend(p, q0fyBody)
		p.SendKey(q0fyKey('m'))
		synctest.Wait()
		if got := q0fyOutside(f); got != "k\x13\rm" || p.heldOperatorInput() != 0 {
			t.Fatalf("after a send the child read %q, %d held; want m straight in", got, p.heldOperatorInput())
		}
	})
}
