package tui

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// ini-1etm: a narrow Codex or agy pane held every send forever. agy cuts its
// placeholder with "..." below 100 columns and the reader read the cut hint
// as a draft; Codex wraps its footer at 33 columns and narrower and the
// reader found no box at all. The captures are real: codex-cli 0.170.0 and
// agy 1.5.12 at each width, idle and with "fix tests" typed, and hover's.

// narrowRig is agentCaptureRig at the capture's own width, keeping the
// pane's events.
func narrowRig(t *testing.T, family, screen string, cols int) (*TUI, *Pane, *pkinFake, <-chan AgentEvent) {
	t.Helper()
	f := &pkinFake{takes: func(int) bool { return true }, screen: func(s string) string {
		return "\x1b[2J\x1b[H" + strings.ReplaceAll(s, "\n", "\r\n")
	}}
	p, events := pkinSendRigSized(t, f, cols, 40)
	p.agentType, p.noBracketedPaste = family, true
	if family == "generic" {
		p.cfg.Command = []string{"/opt/bin/agy"}
	}
	f.paint(screen)
	return &TUI{panes: []PaneView{p}}, p, f, events
}

func narrowSend(t *testing.T, app *TUI, p *Pane, action, text string) IPCResponse {
	t.Helper()
	return i35wIPC(t, func(c net.Conn) {
		app.handleIPCSend(c, IPCRequest{Action: action, Target: p.name, Text: text, Enter: true, From: "eng9"})
	})
}

func rowsOf(screen string) []string { return strings.Split(screen, "\n") }

// AC1, AC3: the placeholder reads empty at every width, whole or cut, with
// or without a spinner glyph; a real short draft still reads as text.
func TestNarrowPaneReadsItsPlaceholderAsEmpty(t *testing.T) {
	hover := agentCapture(t, "hover-codex1-w32-idle")
	qa2Codex := agentCapture(t, "qa2-codex-w32-clipped-footer-idle")
	spinner := strings.Replace(hover, "› Ask Codex to do anything", "› Ask Codex to do anything ⠈", 1)
	if spinner == hover {
		t.Fatal("the spinner variant did not apply")
	}
	for _, tc := range []struct {
		name   string
		r      agentReader
		screen string
		draft  string // "" means the box must read free
	}{
		{"codex-w40", codexReader, agentCapture(t, "local-codex-0170-w40-idle"), ""},
		{"codex-w33", codexReader, agentCapture(t, "local-codex-0170-w33-idle"), ""},
		{"codex-w32", codexReader, agentCapture(t, "local-codex-0170-w32-idle"), ""},
		{"codex-w26-cut-hint", codexReader, agentCapture(t, "local-codex-0170-w26-idle"), ""},
		{"hover-codex1-w32", codexReader, hover, ""},
		{"hover-codex1-w32-spinner", codexReader, spinner, ""},
		{"codex-w40-draft", codexReader, agentCapture(t, "local-codex-0170-w40-draft"), "fix tests"},
		{"codex-w33-draft", codexReader, agentCapture(t, "local-codex-0170-w33-draft"), "fix tests"},
		{"codex-w32-draft", codexReader, agentCapture(t, "local-codex-0170-w32-draft"), "fix tests"},
		{"codex-w33-hint-start-draft", codexReader, strings.Replace(agentCapture(t, "local-codex-0170-w33-idle"), "› Ask Codex to do anything", "› Ask Codex", 1), "Ask Codex"},
		// qa2's live Codex at 32 columns: the footer clipped to one row with
		// "…" ("main · GPT-6.2-Sol high · Con…"), not wrapped.
		{"qa2-codex-w32-clipped-footer", codexReader, qa2Codex, ""},
		{"qa2-codex-w32-clipped-footer-draft", codexReader, strings.Replace(qa2Codex, "› Ask Codex to do anything", "› fix tests", 1), "fix tests"},
		{"codex-hint-cut-with-ellipsis", codexReader, strings.Replace(qa2Codex, "› Ask Codex to do anything", "› Ask Codex to do any…", 1), ""},
		// Q5: text under the hint row is a draft, never the placeholder.
		{"codex-w33-text-under-hint", codexReader, strings.Replace(agentCapture(t, "local-codex-0170-w33-idle"), "› Ask Codex to do anything\n", "› Ask Codex to do anything\n  and fix tests\n", 1), "Ask Codex to do anything and fix tests"},
		{"agy-w40", agyReader, agentCapture(t, "local-agy-1512-w40-idle"), ""},
		{"agy-w33", agyReader, agentCapture(t, "local-agy-1512-w33-idle"), ""},
		{"agy-w32", agyReader, agentCapture(t, "local-agy-1512-w32-idle"), ""},
		{"agy-w26", agyReader, agentCapture(t, "local-agy-1512-w26-idle"), ""},
		{"agy-w40-draft", agyReader, agentCapture(t, "local-agy-1512-w40-draft"), "fix tests"},
		{"agy-w33-draft", agyReader, agentCapture(t, "local-agy-1512-w33-draft"), "fix tests"},
		{"agy-w32-draft", agyReader, agentCapture(t, "local-agy-1512-w32-draft"), "fix tests"},
		{"agy-w33-short-dots-draft", agyReader, strings.Replace(agentCapture(t, "local-agy-1512-w33-idle"), "> Auto mode: edits & safe com...", "> Auto...", 1), "Auto..."},
		{"agy-w33-other-dots-draft", agyReader, strings.Replace(agentCapture(t, "local-agy-1512-w33-idle"), "> Auto mode: edits & safe com...", "> fix the tests...", 1), "fix the tests..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.r.read(rowsOf(tc.screen))
			if tc.draft == "" && !f.free() {
				t.Fatalf("placeholder read as %+v, want free", f)
			}
			if tc.draft != "" && (!f.box || f.free() || f.draft != tc.draft) {
				t.Fatalf("draft read as %+v, want %q", f, tc.draft)
			}
		})
	}
}

// hover's agy1 capture was taken after initech interrupt --hard: its last
// row is agy's "press ctrl+c again to exit", a state pm ruled not built. It
// stays unread (never free), and the bound ends any hold behind it; its box
// row alone is the cut hint and reads as the hint.
func TestNarrowHoverAgyAfterCtrlCStaysUnread(t *testing.T) {
	screen := agentCapture(t, "hover-agy1-w33-ctrlc")
	if f := agyReader.read(rowsOf(screen)); f.free() || f.box {
		t.Fatalf("ctrl+c state read as %+v", f)
	}
	if !agyHintOnly("Auto mode: edits & safe com...") {
		t.Fatal("hover's cut hint is not the hint")
	}
}

// Feed-it-nothing: an empty screen and a one-column box never read free,
// so nothing is typed blind; the bound below ends what they hold.
func TestNarrowEmptyAndOneColumnScreensNeverReadFree(t *testing.T) {
	for _, r := range []agentReader{codexReader, agyReader} {
		for _, screen := range []string{"", "\n\n", "─\n>\n─\n?", "›\n \nm\na\ni\nn\n·\nC\no\nn"} {
			if f := r.read(rowsOf(screen)); f.free() {
				t.Fatalf("reader %d read %q as free: %+v", r, screen, f)
			}
		}
	}
}

// AC2: a send to hover's Codex tile at 32 columns, and to agy's cut hint at
// 33, is typed and delivered on its proof (at base: held forever).
func TestNarrowSendToHoverWidthsGoesThrough(t *testing.T) {
	for _, tc := range []struct {
		family, idle, draft string
		cols                int
		hint, typed         string
	}{
		{"codex", "hover-codex1-w32-idle", "", 32, "› Ask Codex to do anything", "› fix tests"},
		{"generic", "local-agy-1512-w33-idle", "local-agy-1512-w33-draft", 33, "", ""},
		{"codex", "qa2-codex-w32-clipped-footer-idle", "", 32, "› Ask Codex to do anything", "› fix tests"},
	} {
		t.Run(tc.idle, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				idle := agentCapture(t, tc.idle)
				app, p, child, _ := narrowRig(t, tc.family, idle, tc.cols)
				glyph := "› "
				if tc.family == "generic" {
					glyph = "> "
					child.shows = agentCapture(t, tc.draft)
				} else {
					child.shows = strings.Replace(idle, tc.hint, tc.typed, 1)
				}
				child.after = glyph + "fix tests\n\n" + idle
				v := narrowSend(t, app, p, "", "fix tests")
				writes, _, _ := child.stream()
				if v.Delivery != DeliveryDelivered || !strings.Contains(writes, "fix tests") || p.QueueLen() != 0 {
					t.Fatalf("reply %+v queue %d PTY %q", v, p.QueueLen(), writes)
				}
			})
		})
	}
}

// menuChild answers agy's keys from the captured screens: Esc shows
// afterEsc, the Backspaces show the cleaned box. It counts both.
type menuChild struct {
	esc, dels         int
	afterEsc, cleaned string
}

func (m *menuChild) attach(f *pkinFake) {
	f.key = func(b string) {
		for _, c := range b {
			switch c {
			case 0x1b:
				m.esc++
				if m.afterEsc != "" {
					f.paint(m.afterEsc)
				}
			case 0x7f:
				m.dels++
			}
		}
		if strings.ContainsRune(b, 0x7f) && m.cleaned != "" {
			f.paint(m.cleaned)
		}
	}
}

// AC5, R3: a send whose own text opened agy's slash menu, into a box that
// read idle, presses one Esc and, when the box holds exactly our text, one
// Backspace per character typed; read empty, it is not delivered. Never an
// Enter. Every guard has its row: a steered send, a filter that is not ours,
// extra text after Esc, a Backspace count by bytes.
func TestNarrowAgySlashMenuIsClosedAndOurTextRemoved(t *testing.T) {
	idle := agentCapture(t, "local-agy-1512-w33-idle")
	menu := agentCapture(t, "hover-agy1-w33-slash-menu")
	afterEsc := agentCapture(t, "local-agy-1512-w33-menu-after-esc")
	cleaned := agentCapture(t, "local-agy-1512-w33-menu-cleaned")
	for _, tc := range []struct {
		name           string
		text, before   string
		menu, afterEsc string
		outcome        string
		esc, dels      int
	}{
		{"ours", "/clear", idle, menu, afterEsc, DeliveryNotDelivered, 1, 6},
		{"ours-wide", "/clear", agentCapture(t, "local-agy-1512-w100-idle"), agentCapture(t, "local-agy-1512-w100-menu-clear"), agentCapture(t, "local-agy-1512-w100-menu-after-esc"), DeliveryNotDelivered, 1, 6},
		{"one-backspace-per-character", "/clé", idle, strings.ReplaceAll(menu, "> /clear", "> /clé"), strings.ReplaceAll(afterEsc, "> /clear", "> /clé"), DeliveryNotDelivered, 1, 4},
		{"extra-text-after-esc", "/clear", idle, menu, strings.ReplaceAll(afterEsc, "> /clear", "> /clear x"), DeliveryUnconfirmed, 1, 0},
		{"filter-not-ours", "/clear", idle, strings.ReplaceAll(menu, "> /clear", "> /cle"), afterEsc, DeliveryUnconfirmed, 0, 0},
		{"menu-stays-open", "/clear", idle, menu, menu, DeliveryUnconfirmed, 1, 0},
		{"box-not-empty-after-backspaces", "/clear", idle, menu, afterEsc, DeliveryUnconfirmed, 1, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				cols := 33
				if strings.HasSuffix(tc.name, "-wide") {
					cols = 100
				}
				app, p, child, _ := narrowRig(t, "generic", tc.before, cols)
				child.shows = tc.menu
				keys := &menuChild{afterEsc: tc.afterEsc, cleaned: cleaned}
				if tc.name == "box-not-empty-after-backspaces" {
					keys.cleaned = tc.afterEsc // agy ignored the Backspaces
				}
				keys.attach(child)
				v := narrowSend(t, app, p, "", tc.text)
				synctest.Wait()
				if v.Delivery != tc.outcome || keys.esc != tc.esc || keys.dels != tc.dels || child.enters.Load() != 0 {
					t.Fatalf("reply %+v esc %d dels %d enters %d", v, keys.esc, keys.dels, child.enters.Load())
				}
				if tc.outcome == DeliveryNotDelivered && !strings.Contains(v.Data+v.Error, agyMenuNotDelivered) {
					t.Fatalf("reply %+v does not name the menu", v)
				}
			})
		})
	}
}

// R3: a steered send (the box read busy before typing) never presses Esc,
// which would cancel agy's running turn.
func TestNarrowAgySlashMenuOnASteeredSendPressesNoKey(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		busy := agentCapture(t, "local-agy-1511-busy")
		if f := agyReader.read(rowsOf(busy)); !f.busy || !f.box || f.draft != "" {
			t.Fatalf("fixture is not a steerable busy box: %+v", f)
		}
		app, p, child, _ := narrowRig(t, "generic", busy, 220)
		child.shows = agentCapture(t, "local-agy-1512-w100-menu-clear")
		keys := &menuChild{afterEsc: agentCapture(t, "local-agy-1512-w100-menu-after-esc")}
		keys.attach(child)
		v := narrowSend(t, app, p, "", "/clear")
		synctest.Wait()
		if keys.esc != 0 || keys.dels != 0 || child.enters.Load() != 0 || v.Delivery != DeliveryUnconfirmed {
			t.Fatalf("reply %+v esc %d dels %d enters %d", v, keys.esc, keys.dels, child.enters.Load())
		}
	})
}

// boundRig holds one message from eng9 behind a box that never reads empty
// (someone's draft at 33 columns), with the given action.
func boundRig(t *testing.T, action string) (*Pane, *pkinFake, <-chan AgentEvent, []string) {
	t.Helper()
	screen := agentCapture(t, "local-agy-1512-w33-draft")
	app, p, child, events := narrowRig(t, "generic", screen, 33)
	v := narrowSend(t, app, p, action, "hello")
	if v.Delivery != DeliveryHeld || p.QueueLen() != 1 {
		t.Fatalf("not held: %+v", v)
	}
	return p, child, events, rowsOf(screen)
}

func senderLines(events <-chan AgentEvent) []AgentEvent {
	var out []AgentEvent
	for {
		select {
		case ev := <-events:
			if ev.ToSender != "" {
				out = append(out, ev)
			}
		default:
			return out
		}
	}
}

// AC4, R2: a hold for a box that never reads empty ends at the bound, with
// one line to its sender, and nothing is typed after it.
func TestNarrowHoldForANeverEmptyBoxEndsAtTheBound(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, child, events, rows := boundRig(t, "")
		held := p.messageQueue[0].Time
		start := time.Now()
		p.noteBoxWait(rows, start)
		p.resolveHeldMail(start.Add(emptyBoxHoldBound - time.Second))
		if p.QueueLen() != 1 || len(senderLines(events)) != 0 {
			t.Fatalf("ended before the bound: queue %d", p.QueueLen())
		}
		p.noteBoxWait(rows, start.Add(emptyBoxHoldBound))
		p.resolveHeldMail(start.Add(emptyBoxHoldBound))
		lines := senderLines(events)
		want := "the message to " + p.name + " held since " + held.Format("15:04") + " was not delivered: its input box never read empty; send it again"
		if p.QueueLen() != 0 || len(lines) != 1 || lines[0].ToSender != want || lines[0].From != "eng9" {
			t.Fatalf("queue %d lines %+v", p.QueueLen(), lines)
		}
		// Late type: the box now reads empty and the drain runs; nothing of
		// the ended message crosses the PTY.
		child.paint(agentCapture(t, "local-agy-1512-w33-idle"))
		p.drainAgentQueue()
		if writes, _, _ := child.stream(); strings.Contains(writes, "hello") {
			t.Fatalf("typed after the line: %q", writes)
		}
	})
}

// R2: --after and do-not-disturb mail keep their own ends; the bound never
// drops them.
func TestNarrowBoundLeavesAfterAndDNDMail(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, events, rows := boundRig(t, "send_after")
		p.mu.Lock()
		p.messageQueue = append(p.messageQueue, QueuedMessage{Text: "dnd", HeldForAgent: true, DND: true, Time: time.Now(), From: "eng9"})
		p.mu.Unlock()
		start := time.Now()
		p.noteBoxWait(rows, start)
		p.resolveHeldMail(start.Add(10 * emptyBoxHoldBound))
		if p.QueueLen() != 2 || len(senderLines(events)) != 0 {
			t.Fatalf("queue %d after the bound", p.QueueLen())
		}
	})
}

// R2: after a restart the bound counts from the restart, not from the hold.
func TestNarrowBoundCountsFromARestart(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		screen := agentCapture(t, "local-agy-1512-w33-draft")
		_, p, _, events := narrowRig(t, "generic", screen, 33)
		now := time.Now()
		lines := requeueHeld([]heldEntry{{Receipt: "r1", Pane: p.name, Text: "old", Enter: true, Time: now.Add(-time.Hour), Kind: heldKindAgent, From: "eng9"}},
			func(string) *Pane { return p }, now)
		if len(lines) != 0 || p.QueueLen() != 1 {
			t.Fatalf("restore: lines %v queue %d", lines, p.QueueLen())
		}
		p.noteBoxWait(rowsOf(screen), now)
		p.resolveHeldMail(now)
		if p.QueueLen() != 1 {
			t.Fatal("a restored message ended at once")
		}
		p.resolveHeldMail(now.Add(emptyBoxHoldBound))
		if p.QueueLen() != 0 || len(senderLines(events)) != 1 {
			t.Fatalf("queue %d after the bound from the restart", p.QueueLen())
		}
	})
}

// The clock stops whenever the box reads empty, busy or at a dialog, so the
// bound counts only an unbroken wait.
func TestNarrowBoxWaitClockRestartsOnAnEmptyRead(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, _, rows := boundRig(t, "")
		start := time.Now()
		p.noteBoxWait(rows, start)
		p.noteBoxWait(rowsOf(agentCapture(t, "local-agy-1512-w33-idle")), start.Add(time.Minute))
		p.noteBoxWait(rows, start.Add(2*time.Minute))
		p.resolveHeldMail(start.Add(emptyBoxHoldBound))
		if p.QueueLen() != 1 {
			t.Fatal("ended although the box read empty within the bound")
		}
		p.resolveHeldMail(start.Add(2*time.Minute + emptyBoxHoldBound))
		if p.QueueLen() != 0 {
			t.Fatal("not ended a bound after the wait restarted")
		}
	})
}

// A clipped "…" row is Codex's footer only directly below its composer: with
// no "›" composer above, or with text between them, it is not.
func TestNarrowCodexClippedFooterNeedsTheComposerAbove(t *testing.T) {
	for _, screen := range []string{
		"some output\n\n  main · GPT-6.2-Sol high · Con…\n  ← for agents",
		"› an old turn\nits answer\n\n  main · GPT-6.2-Sol high · Con…\n  ← for agents",
		"› Ask Codex to do anything\nnot indented · a · b…\n  ← for agents",
	} {
		if f := codexReader.read(rowsOf(screen)); f.box || f.free() {
			t.Fatalf("%q read as %+v", screen, f)
		}
	}
}

// qa2's live agy at 33 columns: agy cuts its queue footer to "Press up to
// edit queued message". The pane reads busy with its one queued item, and a
// cut preview of our message is the proof that it queued (at ed7a21f both
// sends read unknown).
func TestNarrowAgyCutQueueFooterProvesTheQueuedSend(t *testing.T) {
	for i, text := range []string{"QA-Neng2-3: please reply with the single word ok", "QA-Neng2-8: please reply with the single word ok"} {
		f := agyReader.read(rowsOf(agentCapture(t, fmt.Sprintf("qa2-agy-w33-queued-cut-footer-%d", i+1))))
		if !f.busy || !f.box || f.draft != "" || f.native != 1 {
			t.Fatalf("capture %d read as %+v", i+1, f)
		}
		if got := agyQueuedAppend(agentFrame{box: true, busy: true}, f, newMessageProof(text)); got != 0 {
			t.Fatalf("capture %d: queued append %d, want 0", i+1, got)
		}
		if got := agyQueuedAppend(agentFrame{box: true, busy: true}, f, newMessageProof("QA-Neng2-9: please reply with the single word ok")); got != -1 {
			t.Fatalf("capture %d: another message proved it: %d", i+1, got)
		}
	}
	if agyQueueFooter("Press up to edi") {
		t.Fatal("a 15-character cut was read as the queue footer")
	}
}
