package tui

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func agentCapture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/agent_send/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

func agentCaptureRig(t *testing.T, family, screen string) (*TUI, *Pane, *pkinFake) {
	t.Helper()
	f := &pkinFake{takes: func(int) bool { return true }, screen: func(s string) string {
		return "\x1b[2J\x1b[H" + strings.ReplaceAll(s, "\n", "\r\n")
	}}
	p, _ := pkinSendRigSized(t, f, 220, 48)
	p.agentType, p.noBracketedPaste = family, true
	if family == "generic" {
		p.cfg.Command = []string{"/opt/bin/agy", "--model", "fixture"}
	}
	f.paint(screen)
	return &TUI{panes: []PaneView{p}}, p, f
}

func sendAfterFixture(t *testing.T, app *TUI, p *Pane, text string) IPCResponse {
	t.Helper()
	return i35wIPC(t, func(c net.Conn) {
		app.handleIPCSend(c, IPCRequest{Action: "send_after", Target: p.name, Text: text, Enter: true, From: "owner"})
	})
}

func TestAgentSendCapturedHolds(t *testing.T) {
	for _, tc := range []struct {
		family, file, reason string
		ahead                int
	}{
		{"codex", "local-codex-0170-busy", "running task", 0},
		// ini-bvjy: a send into a working pane with the low-quota notice is
		// steered, never delivered.
		{"codex", "local-codex-0170-bgterm-working-low-quota", "running task", 0},
		{"codex", "local-codex-0170-queued", "running task", 1},
		{"codex", "local-codex-0170-queued-two", "running task", 2},
		{"generic", "local-agy-1511-busy", "running task", 0},
		{"generic", "qa3-agy-working", "running task", 0},
		{"generic", "qa3-agy-native-queued", "running task", 1},
		{"generic", "qa3-agy-native-with-draft", "running task", 1},
		{"generic", "hover-agy2-idle-quota-1016", "running task", 0},
		{"codex", "local-codex-update-menu", "dialog", 0},
		{"codex", "qa3-codex-shortcuts", "dialog", 0},
		{"codex", "local-codex-0170-approval", "dialog", 0},
		{"generic", "local-agy-1511-trust", "dialog", 0},
		{"generic", "local-agy-1511-model-menu", "dialog", 0},
		{"generic", "hover-agy2-0946", "", 0},
		{"codex", "", "", 0},
		{"codex", "hover-agy2-0946", "", 0},
	} {
		t.Run(tc.family+tc.file, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				screen := ""
				if tc.file != "" {
					screen = agentCapture(t, tc.file)
				}
				app, p, f := agentCaptureRig(t, tc.family, screen)
				var v IPCResponse
				if tc.reason == "running task" {
					v = sendAfterFixture(t, app, p, "/clear")
				} else {
					v = v69Send(t, app, p.Name(), "/clear", true)
				}
				synctest.Wait()
				writes, _, _ := f.stream()
				if writes != "" || p.QueueLen() != 1 || !v.OK || (v.Delivery != DeliveryHeld && v.Delivery != DeliveryDeferred) {
					t.Fatalf("PTY=%q held=%d reply=%+v", writes, p.QueueLen(), v)
				}
				if tc.reason == "dialog" && (v.Delivery != DeliveryHeld || !strings.Contains(v.Data, " is at a dialog (") || !strings.Contains(v.Data, "receipt ")) {
					t.Fatalf("dialog: %+v", v)
				}
				if tc.reason == "running task" {
					want := fmt.Sprintf("held for %s: queued behind a running task, %d ahead (receipt ", p.Name(), tc.ahead)
					if !strings.HasPrefix(v.Data, want) {
						t.Fatalf("reply=%q want prefix %q", v.Data, want)
					}
				}
			})
		})
	}
}

func TestAgentSendProof(t *testing.T) {
	for _, tc := range []struct{ family, file string }{
		{"codex", "local-codex-0170-idle"},
		{"generic", "local-agy-1511-idle"},
		{"codex", "local-codex-0170-branch-idle"},
		{"codex", "local-codex-idle"},
	} {
		t.Run(tc.family, func(t *testing.T) {
			for _, result := range []string{"confirmed", "missing", "still-in-box", "already-above", "no-echo"} {
				t.Run(result, func(t *testing.T) {
					sendBubble(t, func(t *testing.T) {
						const msg = "Reply with exactly Amber Receipt. Use no tools."
						idle := agentCapture(t, tc.file)
						prompt := "> "
						placeholder := "Auto mode: edits & safe commands auto-approved (shift+tab to cycle)"
						if tc.family == "codex" {
							prompt = "› "
							placeholder = "Ask Codex to do anything"
						}
						box := strings.Replace(idle, prompt+placeholder, prompt+msg, 1)
						shown := prompt + msg + "\n\n" + idle
						before := idle
						if result == "already-above" {
							before = shown
							box = prompt + msg + "\n\n" + box
						}
						app, p, f := agentCaptureRig(t, tc.family, before)
						f.body, f.shows, f.after = msg, box, shown
						outcome := DeliveryDelivered
						switch result {
						case "missing", "already-above":
							f.after = before
							outcome = DeliveryUnconfirmed
						case "still-in-box":
							f.takes = func(int) bool { return false }
							outcome = DeliveryNotDelivered
						case "no-echo":
							f.shows = idle
							outcome = DeliveryUnconfirmed
						}
						v := v69Send(t, app, p.Name(), msg, true)
						if v.Delivery != outcome {
							t.Fatalf("verdict=%+v want %s", v, outcome)
						}
						wantEnter := int32(1)
						if tc.family == "codex" && result == "still-in-box" {
							wantEnter = 2
						}
						if result == "no-echo" {
							wantEnter = 0
						}
						if f.enters.Load() != wantEnter {
							t.Fatalf("enters=%d want %d", f.enters.Load(), wantEnter)
						}
					})
				})
			}
		})
	}
}

func TestAgentSendWakeNeedsProof(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		parked := NewParkedPane(PaneConfig{Name: "agy-seat", AgentType: "generic", Command: []string{"agy"}}, 10, 40)
		app := newTestTUI(parked)
		var spawned atomic.Int32
		app.paneSpawner = fakePaneSpawner(t, &spawned)
		resp := v69Send(t, app, parked.Name(), "A message for a freshly woken agent", true)
		p := app.panes[0].(*Pane)
		if p != parked {
			t.Cleanup(p.Close)
		}
		if spawned.Load() != 1 || !resp.OK || resp.Delivery != DeliveryHeld || strings.Contains(resp.Data, "delivered") || !strings.Contains(resp.Data, "receipt ") {
			t.Fatalf("wakes=%d reply=%+v", spawned.Load(), resp)
		}
		if p.QueueLen() != 1 {
			t.Fatalf("new pane with no composer holds %d messages", p.QueueLen())
		}
	})
}

func TestAgentSendCapturedStates(t *testing.T) {
	expected := map[string][]string{
		"idle": {"hover-codex1-0946", "hover-codex2-0946", "hover-codex3-0946", "hover-codex4-0946", "local-codex-idle", "local-codex-0170-idle", "local-codex-0170-landed", "local-codex-0170-branch-idle", "local-codex-0170-branch-landed", "local-agy-1511-idle", "local-agy-1511-landed", "hover-agy3-idle-quota-1016", "hover-agy4-idle-quota-1016",
			// ini-bvjy: idle with a background terminal still running, and the
			// negative twin, an old Working row in the conversation above an
			// ordinary row (built from that capture).
			"local-codex-0170-bgterm-idle", "local-codex-0170-working-in-transcript"},
		"busy": {"local-codex-next-tool-short-working", "local-codex-next-tool-short-draft", "local-codex-next-tool-short-shelf", "local-codex-next-tool-long-working", "local-codex-next-tool-long-draft", "local-codex-next-tool-long-shelf", "local-codex-weekly-warning", "local-agy-queued-task-shelf", "local-agy-background-task", "qa2-agy-ten", "qa2-agy-eleven", "qa2-codex-qca-m12", "local-codex-before-tool", "local-codex-after-tool", "local-r6-agy-busy", "local-r6-agy-draft", "local-r6-agy-queued", "qa3-agy-working", "qa3-agy-native-queued", "qa3-agy-native-with-draft", "qa3-codex-e7-solid", "qa3-codex-e8-hollow", "qa3-codex-working-plain", "qa3-codex-native-two", "qa3-codex-working-backticks", "hover-agy1-0946", "hover-agy2-busy-0952", "hover-agy3-busy-0952", "hover-agy4-busy-0952", "hover-agy2-idle-quota-1016", "local-agy-1511-busy", "local-codex-0170-busy", "local-codex-0170-queued", "local-codex-0170-queued-two",
			// ini-bvjy: working with a background terminal, with Codex's "⚠
			// weekly limit: only 5% left" notice between the Working row and
			// the composer.
			"local-codex-0170-bgterm-working-low-quota", "local-codex-0170-bgterm-steered"},
		"draft":      {"local-codex-0170-draft", "local-agy-1511-draft"},
		"dialog":     {"qa3-codex-shortcuts", "local-codex-update-menu", "local-codex-0170-folder-dialog", "local-codex-0170-approval", "local-agy-1511-model-menu", "local-agy-1511-trust"},
		"unreadable": {"hover-agy2-0946", "hover-agy3-0946", "hover-agy4-0946"},
	}
	files, err := filepath.Glob("testdata/agent_send/*.txt")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for want, names := range expected {
		for _, name := range names {
			n++
			t.Run(name, func(t *testing.T) {
				r := codexReader
				if strings.Contains(name, "-agy") {
					r = agyReader
				}
				state := r.read(strings.Split(agentCapture(t, name), "\n"))
				got := "unreadable"
				switch {
				case state.dialog:
					got = "dialog"
				case state.busy:
					got = "busy"
				case state.free():
					got = "idle"
				case state.box:
					got = "draft"
				}
				if got != want {
					t.Errorf("got %s, want %s (draft %q)", got, want, state.draft)
				}
			})
		}
	}
	if n != len(files) {
		t.Fatalf("read %d captures, directory has %d", n, len(files))
	}
}

func TestAgentSendHeldReceiptAndRelease(t *testing.T) {
	for _, family := range []string{"codex", "generic"} {
		t.Run(family, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				idle, busy, glyph, placeholder := "local-codex-0170-idle", "local-codex-0170-queued", "› ", "Ask Codex to do anything"
				native := 1
				if family == "generic" {
					idle, busy, glyph, placeholder = "local-agy-1511-idle", "local-agy-1511-busy", "> ", "Auto mode: edits & safe commands auto-approved (shift+tab to cycle)"
					native = 0
				}
				screen := agentCapture(t, idle)
				tu, p, f := agentCaptureRig(t, family, agentCapture(t, busy))
				events := make(chan AgentEvent, 12)
				p.eventCh = events
				w := newHeldWriter(t.TempDir(), heldFileWindowOne)
				p.setHeldHook(w)
				w.start()
				call := func(action, body, owner string) IPCResponse {
					return i35wIPC(t, func(c net.Conn) {
						tu.handleIPCSend(c, IPCRequest{Action: action, Target: p.name, Text: body, From: owner, Enter: action == "send_after"})
					})
				}
				keep := []string{"First retained message", "Second retained message"}
				ids := []string{}
				for i, msg := range []string{keep[0], "withdraw this message", keep[1]} {
					resp := call("send_after", msg, "eng1")
					want := fmt.Sprintf("%d ahead (receipt ", i+native)
					_, id, found := strings.Cut(resp.Data, want)
					if !found || resp.Delivery != DeliveryHeld {
						t.Fatalf("send %d: %+v; want %s", i, resp, want)
					}
					ids = append(ids, strings.TrimSuffix(id, ")"))
				}
				entries, err := readHeldFile(w.path)
				if err != nil || len(entries) != 3 || entries[1].Receipt != ids[1] {
					t.Fatalf("persisted receipts: %+v err=%v", entries, err)
				}
				if v := call("withdraw", ids[1], "eng9"); v.OK || !strings.Contains(v.Error, "another sender") {
					t.Fatalf("foreign withdrawal: %+v", v)
				}
				if v := call("withdraw", ids[1], "eng1"); !v.OK || v.Data != "withdrawn: "+ids[1] {
					t.Fatalf("withdrawal: %+v", v)
				}
				if v := call("withdraw", ids[1], "eng1"); v.OK || !strings.Contains(v.Error, "unknown") {
					t.Fatalf("duplicate withdrawal: %+v", v)
				}
				if writes, _, _ := f.stream(); writes != "" {
					t.Fatalf("busy pane received %q", writes)
				}
				// Drive the child's real send path. Each paste paints that message into
				// its box; each Enter paints a new user turn above the empty composer.
				f.body = keep[0]
				var transcript string
				f.screen = func(s string) string {
					if strings.HasPrefix(s, "❯ ") {
						s = strings.Replace(screen, glyph+placeholder, glyph+strings.TrimPrefix(s, "❯ "), 1)
					}
					return "\x1b[2J\x1b[H" + strings.ReplaceAll(transcript+s, "\n", "\r\n")
				}
				f.after = screen
				f.takes = func(n int) bool {
					transcript += glyph + keep[n-1] + "\n\n"
					if n < len(keep) {
						f.body = keep[n]
					}
					return true
				}
				f.paint(screen)
				p.safeGo = func(drain func()) { drain() }
				p.maybeDrainModalQueue()
				if len(events) != len(keep) {
					t.Fatalf("release emitted %d events, want %d", len(events), len(keep))
				}
				// Wait for release by taking its verdict events, not by sampling a clock.
				for range keep {
					ev := <-events
					if ev.ToSender != "delivered to "+p.name {
						t.Fatalf("release: %+v", ev)
					}
				}
				writes, _, _ := f.stream()
				want := string(bracketedBody(keep[0])) + "\r" + string(bracketedBody(keep[1])) + "\r"
				if writes != want || p.QueueLen() != 0 {
					t.Fatalf("queue %d, PTY %q; want %q", p.QueueLen(), writes, want)
				}
				if v := call("withdraw", ids[0], "eng1"); v.OK || !strings.Contains(v.Error, "already typed") {
					t.Fatalf("late withdrawal: %+v", v)
				}
			})
		})
	}
}

func TestAgentSendRestoredReceiptAndMissingFile(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		idle := agentCapture(t, "local-codex-0170-idle")
		_, p, f := agentCaptureRig(t, "codex", idle)
		p.suspended = true
		w := newHeldWriter(t.TempDir(), heldFileWindowOne)
		entries, err := readHeldFile(w.path)
		if err != nil || len(entries) != 0 {
			t.Fatalf("absent file: %v %v", entries, err)
		}
		if _, err = p.withdrawReceipt("absent", ""); err == nil {
			t.Fatal("unknown receipt from missing file succeeded")
		}
		if _, err = p.withdrawReceipt("", ""); err == nil {
			t.Fatal("empty receipt succeeded")
		}
		const legacy = "Message from the old version"
		const canRemove = "no-owner-receipt"
		writeHeld(t, filepath.Dir(filepath.Dir(w.path)),
			heldEntry{Pane: p.name, Text: legacy, Enter: true, Time: heldT0, Kind: heldKindSuspended, State: heldStateHeld},
			heldEntry{Pane: p.name, Text: "withdraw me after reload", Receipt: canRemove, Enter: true, Time: heldT0, Kind: heldKindSuspended, State: heldStateHeld})
		entries, err = readHeldFile(w.path)
		if err != nil {
			t.Fatal(err)
		}
		lines := requeueHeld(entries, func(string) *Pane { return p }, heldT0)
		if len(lines) != 0 {
			t.Fatal(lines)
		}
		if _, err = p.withdrawReceipt(canRemove, "new caller"); err != nil {
			t.Fatal(err)
		}
		p.suspended = false
		f.shows = strings.Replace(idle, "› Ask Codex to do anything", "› "+legacy, 1)
		f.after = "› " + legacy + "\n\n" + idle
		p.drainAgentQueue()
		got, _, _ := f.stream()
		if got != string(bracketedBody(legacy))+"\r" {
			t.Fatalf("old held file released %q", got)
		}
	})
}

// Pause the writer at the first byte after the drain has claimed the receipt.
// A withdrawal during this interval must not report success.
func TestAgentSendWithdrawDuringRelease(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		idle := agentCapture(t, "local-agy-1511-idle")
		_, p, f := agentCaptureRig(t, "generic", agentCapture(t, "local-agy-1511-busy"))
		const msg = "Receipt release interleave"
		v := p.SendTextVerdictFrom(msg, true, sendOrigin{From: "owner", After: true})
		_, id, ok := strings.Cut(v.Reason, "(receipt ")
		id = strings.TrimSuffix(id, ")")
		if !ok {
			t.Fatal(v)
		}
		f.paint(idle)
		seen, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		draw := f.screen
		f.shows = strings.Replace(idle, "> Auto mode: edits & safe commands auto-approved (shift+tab to cycle)", "> "+msg, 1)
		f.after = "> " + msg + "\n\n" + idle
		f.screen = func(frame string) string {
			if frame == f.shows {
				close(seen)
				<-resume
			}
			return draw(frame)
		}
		go func() { defer close(done); p.drainAgentQueue() }()
		<-seen
		answer := make(chan error, 1)
		go func() { _, err := p.withdrawReceipt(id, "owner"); answer <- err }()
		synctest.Wait()
		select {
		case err := <-answer:
			close(resume)
			<-done
			t.Fatalf("withdrawal returned before the write completed: %v", err)
		default:
		}
		close(resume)
		<-done
		err := <-answer
		if err == nil || !strings.Contains(err.Error(), "already typed") {
			t.Fatalf("withdraw in flight: %v", err)
		}
		got, _, _ := f.stream()
		if got != string(bracketedBody(msg))+"\r" {
			t.Fatalf("release typed %q", got)
		}
	})
}

func TestAgentSendNativeQueueIsNotDelivery(t *testing.T) {
	for _, tc := range []struct{ file, text string }{
		{"local-codex-0170-queued-two", "Reply Green after Blue, using no tools."},
		{"qa3-codex-native-two", "STEER-E: after the last step, append STEER-E-RECEIVED to /tmp/iq-steer/cdx/log.txt with echo"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				idle := agentCapture(t, "local-codex-0170-branch-idle")
				queue := agentCapture(t, tc.file)
				msg := tc.text
				tu, p, f := agentCaptureRig(t, "codex", idle)
				f.shows = strings.Replace(idle, "› Ask Codex to do anything", "› "+msg, 1)
				f.after = queue
				v := v69Send(t, tu, p.name, msg, true)
				if v.Delivery != DeliveryQueued || !strings.HasPrefix(v.Data, "queued by "+p.name+" behind its running task, 1 ahead") || strings.Contains(v.Data, "receipt") || p.QueueLen() != 0 {
					t.Fatalf("native queue: %+v, local holds %d", v, p.QueueLen())
				}
				p.updateActivity()
				seats, ok := tu.AllPanes()
				if !ok || len(seats) != 1 || seats[0].QueuedCount != 2 || p.Activity() != StateRunning {
					t.Fatalf("native queue in status: %+v", seats)
				}
			})
		})
	}
}

func TestAgentSendUnsupportedCommandsKeepTheirVerdict(t *testing.T) {
	for _, family := range []string{"opencode", "generic"} {
		t.Run(family, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				tu, p, f := agentCaptureRig(t, family, agentCapture(t, "local-agy-1511-idle"))
				p.cfg.Command = []string{"other", "agy"}
				v := v69Send(t, tu, p.name, "message text", true)
				got, _, _ := f.stream()
				if v.Delivery != DeliveryUnchecked || !strings.Contains(got, "message text") {
					t.Fatalf("unsupported: reply %+v, PTY %q", v, got)
				}
			})
		})
	}
}

func TestAgentSendReleaseRechecksAfterSaving(t *testing.T) {
	for _, change := range []string{"busy", "dnd"} {
		t.Run(change, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				busy := agentCapture(t, "local-codex-0170-busy")
				_, p, f := agentCaptureRig(t, "codex", busy)
				v := p.SendTextVerdictFrom("Held while busy", true, sendOrigin{From: "owner", After: true})
				if v.Outcome != DeliveryHeld {
					t.Fatal(v)
				}
				f.paint(agentCapture(t, "local-codex-0170-idle"))
				p.heldHook = func(p *Pane) {
					p.mu.Lock()
					claimed := p.heldTyping != nil
					p.mu.Unlock()
					if claimed {
						if change == "busy" {
							f.paint(busy)
						} else {
							p.startDNDFor(time.Now(), 0)
						}
					}
				}
				p.drainAgentQueue()
				writes, _, _ := f.stream()
				if writes != "" || p.QueueLen() != 1 {
					t.Fatalf("PTY %q, held %d", writes, p.QueueLen())
				}
			})
		})
	}
}

func TestAgentSendWrappedRows(t *testing.T) {
	idle := agentCapture(t, "local-codex-0170-idle")
	busy := strings.Replace(idle, "› Ask Codex", "• Working (17s • esc to interrupt)\n  1 background terminal running\n\n› Ask Codex", 1)
	if f := codexReader.read(strings.Split(busy, "\n")); !f.busy || f.free() {
		t.Fatalf("wrapped progress: %+v", f)
	}
	for _, r := range []agentReader{codexReader, agyReader} {
		glyph := "›"
		if r == agyReader {
			glyph = ">"
		}
		rows := []string{glyph + " First line", "", "  second line with soft", "  wrapping", "", "• response"}
		if !newMessageProof("First line\n\nsecond line with soft wrapping").userTurn(r, rows) || newMessageProof("First line\n\nsecond line with other words").userTurn(r, rows) {
			t.Fatalf("reader %d failed multiline proof", r)
		}
	}
}

func TestAgentSendForwardedWithdrawalReachesOwner(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		app, p, child := agentCaptureRig(t, "generic", agentCapture(t, "local-agy-1511-busy"))
		pm := peerManager{onForwardSend: app.deliverForwardedSend}
		send := pm.answerForwardSend("peer", ControlResp{Action: "forward_send_after", Target: p.name, Text: "wait for me", Enter: true, From: "owner"})
		_, receipt, ok := strings.Cut(send.Data, "(receipt ")
		if !ok || !send.OK || send.Delivery != DeliveryHeld {
			t.Fatal(send)
		}
		receipt = strings.TrimSuffix(receipt, ")")
		withdraw := ControlResp{Action: "forward_withdraw", Target: p.name, Text: receipt, From: "other"}
		if v := pm.answerForwardSend("peer", withdraw); v.OK || !strings.Contains(v.Error, "another sender") {
			t.Fatal(v)
		}
		withdraw.From = "owner"
		v := pm.answerForwardSend("peer", withdraw)
		writes, _, _ := child.stream()
		if !v.OK || v.Data != "withdrawn: "+receipt || p.QueueLen() != 0 || writes != "" {
			t.Fatalf("reply %+v held %d PTY %q", v, p.QueueLen(), writes)
		}
	})
}

func TestAgentSendRealLandedScreens(t *testing.T) {
	for _, tc := range []struct{ family, idle, landed, text string }{
		{"generic", "local-agy-1511-idle", "local-agy-1511-landed", "Reply with exactly Amber Receipt. Use no tools."},
		{"codex", "local-codex-0170-branch-idle", "local-codex-0170-branch-landed", "Reply only with Quartz. Use no tools."},
	} {
		t.Run(tc.family, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				idle := agentCapture(t, tc.idle)
				app, p, child := agentCaptureRig(t, tc.family, idle)
				if tc.family == "generic" {
					child.shows = agentCapture(t, "local-agy-1511-draft")
				} else {
					child.shows = strings.Replace(idle, "› Ask Codex to do anything", "› "+tc.text, 1)
				}
				child.after = agentCapture(t, tc.landed)
				v := v69Send(t, app, p.name, tc.text, true)
				if v.Delivery != DeliveryDelivered || child.enters.Load() != 1 {
					t.Fatalf("reply %+v, enters %d", v, child.enters.Load())
				}
			})
		})
	}
}

func TestAgentSendStatusMatchesBothReaders(t *testing.T) {
	for _, tc := range []struct{ family, idle, busy, dialog string }{
		{"codex", "local-codex-0170-branch-idle", "local-codex-0170-busy", "local-codex-update-menu"},
		{"generic", "local-agy-1511-idle", "local-agy-1511-busy", "local-agy-1511-trust"},
	} {
		t.Run(tc.family, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				app, p, child := agentCaptureRig(t, tc.family, "")
				for _, check := range []struct {
					file  string
					state ActivityState
				}{{tc.busy, StateRunning}, {tc.dialog, StateWaitingInput}, {tc.idle, StateIdle}} {
					child.paint(agentCapture(t, check.file))
					p.updateActivity()
					rows, ok := app.AllPanes()
					if !ok || len(rows) != 1 || rows[0].Activity != check.state.String() {
						t.Fatalf("%s reported %+v", check.file, rows)
					}
				}
			})
		})
	}
}

func TestAgentSendAfterHoldsRegardlessOfContent(t *testing.T) {
	for _, file := range []string{"qa3-codex-working-plain", "qa3-codex-working-backticks"} {
		t.Run(file, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				app, p, child := agentCaptureRig(t, "codex", agentCapture(t, file))
				native := 0
				if file == "qa3-codex-working-backticks" {
					native = 3
				}
				for i, body := range []string{"After your step, record this message.", "Please record `echo MARKER >> log.txt` after your step."} {
					reply := sendAfterFixture(t, app, p, body)
					want := fmt.Sprintf("held for %s: queued behind a running task, %d ahead (receipt ", p.name, native+i)
					writes, _, _ := child.stream()
					if writes != "" || !reply.OK || reply.Delivery != DeliveryHeld || !strings.HasPrefix(reply.Data, want) {
						t.Errorf("body %q: PTY %q, reply %+v, want %q", body, writes, reply, want)
					}
				}
			})
		})
	}
}

func TestAgentSendDefaultSteersEveryWorkingCodex(t *testing.T) {
	for _, file := range []string{"local-codex-0170-busy", "local-codex-0170-queued", "local-codex-0170-queued-two", "qa3-codex-working-plain", "qa3-codex-working-backticks", "qa3-codex-native-two", "qa3-codex-e7-solid", "qa3-codex-e8-hollow", "qa2-codex-qca-m12", "local-codex-before-tool", "local-codex-after-tool", "0.154.0"} {
		t.Run(file, func(t *testing.T) {
			for _, body := range []struct{ typed, rendered string }{
				{"After your step, record this message.", "After your step, record this message."},
				{"Please record `echo MARKER >> log.txt` after your step.", "Please record echo MARKER >> log.txt after your step."},
			} {
				sendBubble(t, func(t *testing.T) {
					rendered := body.rendered
					var before string
					if file == "0.154.0" {
						b, err := os.ReadFile("testdata/codex/0.154.0/running-frame.txt")
						if err != nil {
							t.Fatal(err)
						}
						before = string(b)
						rendered = body.typed
					} else {
						before = agentCapture(t, file)
					}
					app, p, child := agentCaptureRig(t, "codex", before)
					busy := agentCapture(t, "local-codex-0170-busy")
					child.shows = strings.Replace(busy, "› Ask Codex to do anything", "› "+body.typed, 1)
					child.after = "› " + rendered + "\n\n" + busy
					child.takes = func(n int) bool { return n == 2 }
					reply := v69Send(t, app, p.name, body.typed, true)
					writes, _, _ := child.stream()
					if !reply.OK || reply.Delivery != "steered" || writes != string(bracketedBody(body.typed))+"\r\r" {
						t.Errorf("%s: reply %+v PTY %q", body.typed, reply, writes)
					}
				})
			}
		})
	}
}

func TestAgentSendSpinnerNeverReleasesHeldMail(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		app, p, f := agentCaptureRig(t, "codex", agentCapture(t, "qa3-codex-e7-solid"))
		if reply := sendAfterFixture(t, app, p, "After this task"); reply.Delivery != DeliveryHeld {
			t.Fatal(reply)
		}
		for _, file := range []string{"qa3-codex-e7-solid", "qa3-codex-e8-hollow", "qa3-codex-e7-solid"} {
			screen := agentCapture(t, file)
			if state, ok := codexScreenSignature(strings.Split(screen, "\n")); !ok || state != StateRunning {
				t.Errorf("%s signature %v %v", file, state, ok)
			}
			f.paint(screen)
			p.updateActivity()
			p.maybeDrainModalQueue()
			synctest.Wait()
			writes, _, _ := f.stream()
			if p.Activity() != StateRunning || p.QueueLen() != 1 || writes != "" {
				t.Fatalf("%s activity %v, held %d, PTY %q", file, p.Activity(), p.QueueLen(), writes)
			}
		}
	})
}

func TestAgentSendSteerNeedsNewProof(t *testing.T) {
	for _, outcome := range []string{"missing", "already above", "native queued", "turn ended before proof", "confirmed first Enter"} {
		t.Run(outcome, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				busy := agentCapture(t, "local-codex-0170-busy")
				const msg = "Reply Green after Blue, using no tools."
				before := busy
				if outcome == "already above" {
					before = "› " + msg + "\n\n" + before
				}
				app, p, f := agentCaptureRig(t, "codex", before)
				f.shows = strings.Replace(before, "› Ask Codex to do anything", "› "+msg, 1)
				f.after = before
				want := DeliveryUnconfirmed
				switch outcome {
				case "native queued":
					f.after = agentCapture(t, "local-codex-0170-queued-two")
					want = DeliveryQueued
				case "turn ended before proof":
					f.after = "› " + msg + "\n\n" + agentCapture(t, "local-codex-0170-idle")
				case "confirmed first Enter":
					f.after = "› " + msg + "\n\n" + busy
					want = DeliverySteered
				}
				v := v69Send(t, app, p.name, msg, true)
				if v.Delivery != want || f.enters.Load() != 1 {
					t.Fatalf("verdict %+v, submits %d", v, f.enters.Load())
				}
			})
		})
	}
}

func TestAgentSendDNDStillHoldsWorkingCodex(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		app, p, f := agentCaptureRig(t, "codex", agentCapture(t, "qa3-codex-native-two"))
		p.startDNDFor(time.Now(), 0)
		v := v69Send(t, app, p.name, "Wait for me", true)
		writes, _, _ := f.stream()
		if writes != "" || v.Delivery != DeliveryHeld || !strings.Contains(v.Data, "2 messages ahead") || !strings.Contains(v.Data, "receipt ") || p.QueueLen() != 1 {
			t.Fatalf("PTY %q, reply %+v", writes, v)
		}
	})
}

func TestAgentSendDefaultBypassesMailExplicitlyHeldForIdle(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		busy := agentCapture(t, "qa3-codex-e7-solid")
		app, p, f := agentCaptureRig(t, "codex", busy)
		if v := sendAfterFixture(t, app, p, "A later task"); v.Delivery != DeliveryHeld {
			t.Fatal(v)
		}
		const msg = "An update to the current task"
		f.shows = strings.Replace(busy, "› Ask Codex to do anything", "› "+msg, 1)
		f.after = "› " + msg + "\n\n" + busy
		v := v69Send(t, app, p.name, msg, true)
		writes, _, _ := f.stream()
		if v.Delivery != DeliverySteered || p.QueueLen() != 1 || writes != string(bracketedBody(msg))+"\r" {
			t.Fatalf("verdict %+v held %d PTY %q", v, p.QueueLen(), writes)
		}
	})
}

func TestAgentSendRemoteAfterAndWithdrawKeepTheirActions(t *testing.T) {
	r := newCrossRig(t)
	for _, action := range []string{"send_after", "withdraw"} {
		// An older peer refuses a command it does not implement.
		r.mu.Lock()
		r.reply = func(c ControlCmd) *ControlResp { return &ControlResp{Error: "unknown action " + c.Action} }
		r.mu.Unlock()
		v := r.call(t, sendH, IPCRequest{Action: action, Host: "workbench", Target: "eng1", Text: "receipt or message", From: "owner"})
		got := r.received()
		if v.OK || !strings.Contains(v.Error, "unknown action "+action) || len(got) == 0 || got[len(got)-1].Action != action || got[len(got)-1].From != "owner" {
			t.Fatalf("action %s response %+v controls %+v", action, v, got)
		}
	}
}

func TestAgentSendWorkingAgyQueuesAtNextStep(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		before := agentCapture(t, "qa3-agy-working")
		const msg = "MARK-A1: also append MARK-A1 to /tmp/iq-agy/ag/log.txt with echo"
		app, p, f := agentCaptureRig(t, "generic", before)
		f.shows = strings.Replace(before, "\n>\n", "\n> "+msg+"\n", 1)
		f.after = agentCapture(t, "qa3-agy-native-queued")
		v := v69Send(t, app, p.name, msg, true)
		writes, _, _ := f.stream()
		want := "queued by " + p.name + ": runs at the next step of its running task, 0 ahead"
		if !v.OK || v.Delivery != DeliveryQueued || v.Data != want || p.QueueLen() != 0 || writes != string(bracketedBody(msg))+"\r" {
			t.Fatalf("reply %+v; local holds %d PTY %q", v, p.QueueLen(), writes)
		}
	})
}

func TestAgentSendMultilineAgyIsOneNativeItem(t *testing.T) {
	for _, ending := range []string{"recorded queue", "different preview", "split queue", "no queue"} {
		t.Run(ending, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				const body = "Please remember the first marker.\nPlease remember the second marker."
				busy := agentCapture(t, "local-r6-agy-busy")
				app, p, f := agentCaptureRig(t, "generic", busy)
				f.shows = agentCapture(t, "local-r6-agy-draft")
				f.after = agentCapture(t, "local-r6-agy-queued")
				want := DeliveryQueued
				switch ending {
				case "different preview":
					f.after = strings.Replace(f.after, "▸ Please remember the first marker....", "▸ A different message...", 1)
					want = DeliveryUnconfirmed
				case "split queue":
					f.after = strings.Replace(f.after, "▸ Please remember the first marker....", "▸ Please remember the first marker....\n▸ Please remember the second marker.", 1)
					want = DeliveryUnconfirmed
				case "no queue":
					f.after = "> " + strings.ReplaceAll(body, "\n", "\n  ") + "\n\n" + busy
					want = DeliveryUnconfirmed
				}
				v := v69Send(t, app, p.name, body, true)
				writes, _, _ := f.stream()
				if v.Delivery != want || p.QueueLen() != 0 || writes != string(bracketedBody(body))+"\r" || f.enters.Load() != 1 {
					t.Fatalf("verdict %+v, PTY %q, local holds %d", v, writes, p.QueueLen())
				}
				if ending == "recorded queue" && (v.Data != "queued by "+p.name+": runs at the next step of its running task, 0 ahead" || p.readAgentFrame().native != 1) {
					t.Fatal(v)
				}
			})
		})
	}
}

func TestAgentSendAgyReportsNativeItemsAhead(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		const msg = "The second native item"
		before := agentCapture(t, "qa3-agy-native-queued")
		app, p, f := agentCaptureRig(t, "generic", before)
		f.shows = strings.Replace(before, "\n>\n", "\n> "+msg+"\n", 1)
		f.after = strings.Replace(before, " (alt+m to send immediately)\n", "\n▸ "+msg+" (alt+m to send immediately)\n", 1)
		v := v69Send(t, app, p.name, msg, true)
		if v.Delivery != DeliveryQueued || v.Data != "queued by "+p.name+": runs at the next step of its running task, 1 ahead" || p.QueueLen() != 0 || p.readAgentFrame().native != 2 {
			t.Fatalf("reply %+v; local %d native %d", v, p.QueueLen(), p.readAgentFrame().native)
		}
	})
}

func TestAgentSendCodexRenderedUserTurn(t *testing.T) {
	const msg = "QA-M12: this is a realistic longer steering message from a super that explains context " +
		"at some length so that it is well over one screen row wide in the tile; please also run " +
		"`echo QA-M12 >> /tmp/q8c.log` and then carry on with the current step as before."
	for _, scenario := range []string{"fresh", "already visible", "appeared before Enter", "different command", "draft lost backticks"} {
		t.Run(scenario, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				landed := agentCapture(t, "qa2-codex-qca-m12")
				working := agentCapture(t, "qa3-codex-e8-hollow")
				if scenario == "already visible" {
					working = landed
				}
				app, p, child := agentCaptureRig(t, "codex", working)
				child.shows = strings.Replace(working, "› Ask Codex to do anything", "› "+msg, 1)
				child.after = landed
				want := DeliveryUnconfirmed
				enters := int32(1)
				switch scenario {
				case "fresh":
					want = DeliverySteered
				case "already visible":
					// The long draft scrolls the old copy out of view before Enter.
					child.shows = strings.Replace(agentCapture(t, "qa3-codex-e8-hollow"), "› Ask Codex to do anything", "› "+msg, 1)
				case "appeared before Enter":
					child.shows = strings.Replace(landed, "› Ask Codex to do anything", "› "+msg, 1)
				case "different command":
					child.after = strings.Replace(landed, ">> /tmp", "> /tmp", 1)
				case "draft lost backticks":
					child.shows = strings.ReplaceAll(child.shows, "`", "")
					enters = 0
				}
				reply := v69Send(t, app, p.name, msg, true)
				if reply.Delivery != want || child.enters.Load() != enters {
					t.Fatalf("send returned %+v after %d submits, want %s after %d", reply, child.enters.Load(), want, enters)
				}
			})
		})
	}
}

func TestAgentSendCodexTransientUserTurn(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		const msg = "Read the new marker"
		working := agentCapture(t, "qa3-codex-e8-hollow")
		app, p, child := agentCaptureRig(t, "codex", working)
		child.shows = strings.Replace(working, "› Ask Codex to do anything", "› "+msg, 1)
		child.after = "› " + msg + "\n\n" + working
		child.takes = func(int) bool {
			// On the bubble clock the child paints after Enter, then removes
			// the turn between the sender's first and second polls.
			time.AfterFunc(composerEchoPoll+composerEchoPoll/2, func() { child.paint(working) })
			return true
		}
		reply := v69Send(t, app, p.name, msg, true)
		if reply.Delivery != DeliveryUnconfirmed || child.enters.Load() != 1 {
			t.Fatalf("one transient user turn returned %+v after %d submits", reply, child.enters.Load())
		}
	})
}

func TestAgentSendCodexInlineCode(t *testing.T) {
	for _, tc := range []struct{ body, shown string }{
		{"", ""},
		{"`", "`"},
		{"example:\n\n    shell `date`", "example:\n\n    shell `date`"},
		{"example:\n\n\tshell `date`", "example:\n\n\tshell `date`"},
		{"run `one` then `two`", "run one then two"},
		{"use ``a`b``", "use a`b"},
		{"run ` echo hi `", "run echo hi"},
		{"run `a  b`", "run a  b"},
		{"run `  `", "run   "},
		{"unmatched ` then ``ok``", "unmatched ` then ok"},
		{"first `one`\nsecond `two`", "first one\nsecond two"},
		{"open `", "open `"},
		{"escaped \\`literal`", "escaped \\`literal`"},
		{"```sh\necho `date`\n```", "```sh\necho `date`\n```"},
	} {
		p := newMessageProof(tc.body)
		got := p.match(tc.shown, false)
		if (tc.shown == "" && got != proofNone) || (tc.shown != "" && got != proofFull) {
			t.Errorf("body %q, display %q: got %v", tc.body, tc.shown, got)
		}
	}
	const msg = "run `echo hi`"
	proof := newMessageProof(msg)
	if !proof.userTurn(agyReader, []string{"> run echo hi"}) ||
		proof.queueIndex(codexReader, []string{"↳ run echo hi"}) != 0 ||
		proof.userTurn(codexReader, []string{"• run echo hi"}) {
		t.Fatal("rendered proof did not respect the user-turn and queue boundaries")
	}
}

func TestAgentSendCodexBeforeAndAfterToolCall(t *testing.T) {
	const msg = "QA-M12: also run `echo QA-M12 >> marks.txt`"
	for _, tc := range []struct{ screen, verdict string }{
		{"local-codex-before-tool", DeliveryQueued},
		{"local-codex-after-tool", DeliverySteered},
	} {
		t.Run(tc.screen, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				busy := agentCapture(t, "qa3-codex-e7-solid")
				app, p, child := agentCaptureRig(t, "codex", busy)
				child.shows = strings.Replace(busy, "› Ask Codex to do anything", "› "+msg, 1)
				child.after = agentCapture(t, tc.screen)
				reply := v69Send(t, app, p.name, msg, true)
				if tc.verdict == DeliveryQueued && reply.Data != "queued by "+p.name+": runs at the next step of its running task, 0 ahead" {
					t.Fatalf("next-tool receipt: %+v", reply)
				}
				if reply.Delivery != tc.verdict || child.enters.Load() != 1 {
					t.Fatalf("send returned %+v after %d submits, want %s after one", reply, child.enters.Load(), tc.verdict)
				}
			})
		})
	}
}

func TestAgentSendCodexNextToolShelf(t *testing.T) {
	const short = "QA-M2: also run `echo QA-M2 >> ./marks0.log`"
	const long = "QA-M3: this is a realistic longer steering message from a super that " +
		"explains context at some length so that it is well over one screen row wide in the tile; " +
		"please also run: echo QA-M3 >> ./marks0.log and then carry on with the current step as before."
	for _, tc := range []struct {
		name   string
		length string
		queued bool
		ahead  int
	}{
		{"exact item", "short", true, 0},
		{"clipped wrapped item", "long", true, 0},
		{"native and held ahead", "short", true, 2},
		{"look-alike header", "short", false, 0},
		{"wrong header ending", "short", false, 0},
		{"indented transcript heading", "short", false, 0},
		{"interrupted samples", "short", false, 0},
		{"older unrelated follow-up heading", "short", false, 0},
		{"clipped shelf before Enter", "short", false, 0},
		{"foreign indented row", "short", false, 0},
		{"gap in header", "short", false, 0},
		{"unrelated text below shelf", "short", false, 0},
		{"nonempty composer", "short", false, 0},
		{"already on shelf", "short", false, 0},
		{"stale before draft", "short", false, 0},
		{"earlier slot changed", "short", false, 0},
		{"two added slots", "short", false, 0},
		{"replaced existing slot", "short", false, 0},
		{"matching item is not last", "short", false, 0},
		{"ambiguous clipped prefix", "long", false, 0},
		{"different body", "short", false, 0},
		{"one sample", "short", false, 0},
		{"header only", "short", false, 0},
		{"empty screen", "short", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				msg := short
				if tc.length == "long" {
					msg = long
				}
				base := "local-codex-next-tool-" + tc.length + "-"
				before := agentCapture(t, base+"working")
				draft := agentCapture(t, base+"draft")
				after := agentCapture(t, base+"shelf")
				const box = "› Ask Codex to do anything"
				const row = "  ↳ "
				withOlder := func(s string) string { return strings.Replace(s, row, row+"First queued message\n"+row, 1) }
				beforeOlder := strings.Replace(after, row+msg, row+"First queued message", 1)
				switch tc.name {
				case "native and held ahead", "earlier slot changed":
					before = beforeOlder
					draft = strings.Replace(before, box, "› "+msg, 1)
					after = withOlder(after)
					if tc.name == "earlier slot changed" {
						after = strings.Replace(after, "First queued message", "Another queued message", 1)
					}
				case "look-alike header":
					after = strings.Replace(after, "• Messages to be submitted", "• Drafts to be submitted", 1)
				case "indented transcript heading":
					after = strings.Replace(after, "• Messages to be submitted", " • Messages to be submitted", 1)
				case "wrong header ending":
					after = strings.Replace(after, "send immediately)", "send later)", 1)
				case "older unrelated follow-up heading":
					after = strings.Replace(after, "• Messages to be submitted", "• Queued follow-up inputs\n  ↳ Old item\n\n• Other messages to be submitted", 1)
					// No quota decoration between the queue and composer on this variant.
					start := strings.Index(after, "                                        ⚠ weekly limit:")
					_, tail, _ := strings.Cut(after[start:], "\n")
					after = after[:start] + tail
				case "clipped shelf before Enter":
					before = strings.Replace(beforeOlder, "• Messages to be submitted", "clipped header", 1)
					draft = strings.Replace(before, box, "› "+msg, 1)
				case "foreign indented row":
					after = strings.Replace(after, row+msg, row+msg+"\n  foreign", 1)
				case "gap in header":
					after = strings.Replace(after, "  send immediately)", "\n  send immediately)", 1)
				case "unrelated text below shelf":
					after = strings.Replace(after, box, "• unrelated transcript\n\n"+box, 1)
				case "nonempty composer":
					after = strings.Replace(after, box, "› Different draft", 1)
				case "already on shelf":
					before = after
					draft = strings.Replace(before, box, "› "+msg, 1)
				case "stale before draft":
					before = after
				case "replaced existing slot":
					before = beforeOlder
					draft = strings.Replace(before, box, "› "+msg, 1)
				case "matching item is not last":
					after = strings.Replace(after, row+msg, row+msg+"\n"+row+"Another queued message", 1)
				case "two added slots":
					after = withOlder(after)
				case "ambiguous clipped prefix":
					header, preview, _ := strings.Cut(after, row)
					_, footer, _ := strings.Cut(preview, "    …\n")
					older := row + msg[:55] + "…\n"
					before = header + older + footer
					draft = strings.Replace(before, box, "› "+msg, 1)
					after = header + older + row + preview
				case "different body":
					after = strings.Replace(after, row+msg, row+"Someone else's message", 1)
				case "header only":
					after = strings.Replace(after, row+msg+"\n", "", 1)
				case "empty screen":
					after = ""
				}
				app, p, child := agentCaptureRig(t, "codex", before)
				if tc.name == "native and held ahead" {
					if held := sendAfterFixture(t, app, p, "earlier Initech hold"); held.Delivery != DeliveryHeld || !strings.Contains(held.Data, "1 ahead (receipt ") {
						t.Fatal(held)
					}
				}
				child.shows, child.after = draft, after
				if tc.name == "one sample" || tc.name == "interrupted samples" {
					child.takes = func(int) bool {
						// Paint either one sighting or two separated by a missing shelf.
						// sendBubble advances only the synthetic clock.
						time.AfterFunc(composerEchoPoll+composerEchoPoll/2, func() { child.paint(before) })
						if tc.name == "interrupted samples" {
							time.AfterFunc(2*composerEchoPoll+composerEchoPoll/2, func() { child.paint(after) })
							time.AfterFunc(3*composerEchoPoll+composerEchoPoll/2, func() { child.paint(before) })
						}
						return true
					}
				}
				reply := v69Send(t, app, p.name, msg, true)
				want := DeliveryUnconfirmed
				if tc.queued {
					want = DeliveryQueued
					if words := fmt.Sprintf("queued by %s: runs at the next step of its running task, %d ahead", p.name, tc.ahead); reply.Data != words {
						t.Errorf("got %q, want %q", reply.Data, words)
					}
				}
				writes, _, _ := child.stream()
				if reply.Delivery != want || writes != string(bracketedBody(msg))+"\r" {
					t.Fatalf("reply %+v, want %s after one submission; PTY %q", reply, want, writes)
				}
			})
		})
	}
}

// ini-bvjy, AC2: a send into a Codex pane working with a background terminal,
// Codex's "⚠ weekly limit: only 5% left" notice between its Working row and
// the composer, reports steered: the frame before the send reads busy, and
// the frame after shows our message in the conversation with the pane still
// working (both live captures, codex-cli 0.170.0).
func TestAgentSendCodexSteeredPastTheQuotaNotice(t *testing.T) {
	const msg = "Reply with the single word ok."
	sendBubble(t, func(t *testing.T) {
		busy := agentCapture(t, "local-codex-0170-bgterm-working-low-quota")
		app, p, child := agentCaptureRig(t, "codex", busy)
		child.shows = strings.Replace(busy, "› Ask Codex to do anything", "› "+msg, 1)
		child.after = agentCapture(t, "local-codex-0170-bgterm-steered")
		reply := v69Send(t, app, p.name, msg, true)
		if reply.Delivery != DeliverySteered || child.enters.Load() != 1 {
			t.Fatalf("send returned %+v after %d submits, want steered after one", reply, child.enters.Load())
		}
	})
}

// ini-bvjy, AC3: status --json and the agents list (codexScreenSignature)
// read the same captures as the send path: working with a background
// terminal and the quota notice reads running; idle with the terminal still
// going reads idle.
func TestCodexSignatureReadsTheBackgroundTerminalCaptures(t *testing.T) {
	for name, want := range map[string]ActivityState{
		"local-codex-0170-bgterm-working-low-quota": StateRunning,
		"local-codex-0170-bgterm-steered":           StateRunning,
		"local-codex-0170-bgterm-idle":              StateIdle,
	} {
		got, ok := codexScreenSignature(strings.Split(agentCapture(t, name), "\n"))
		if !ok || got != want {
			t.Errorf("%s: %v (ok %v), want %v", name, got, ok, want)
		}
	}
}
