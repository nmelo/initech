package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSendAgyRenderedAppend(t *testing.T) {
	const body = "QA-ML-FIRST line\nQA-ML-LAST line"
	ten, eleven := agentCapture(t, "qa2-agy-ten"), agentCapture(t, "qa2-agy-eleven")
	for _, scenario := range []string{"new multiline item", "present with draft", "old row changed", "ambiguous short preview", "shared clipped prefix", "old unmarked row grew", "different new item", "two new items"} {
		t.Run(scenario, func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				before := ten
				if scenario == "shared clipped prefix" {
					before = strings.Replace(ten, "▸ QA-M1: also run: echo QA-M1 >> /tmp/q8a.log", "▸ QA-ML-FIRST line with different instructions", 1)
				}
				app, p, child := agentCaptureRig(t, "generic", before)
				child.shows = strings.Replace(before, "\n>\n", "\n> QA-ML-FIRST line\n  QA-ML-LAST line\n", 1)
				child.after = eleven
				want := DeliveryUnconfirmed
				switch scenario {
				case "new multiline item":
					want = DeliveryQueued
				case "present with draft":
					child.shows = strings.Replace(eleven, "\n>\n", "\n> QA-ML-FIRST line\n  QA-ML-LAST line\n", 1)
				case "shared clipped prefix":
					child.after = strings.Replace(eleven, "▸ QA-M1: also run: echo QA-M1 >> /tmp/q8a.log", "▸ QA-ML-FIRST line with different instructions", 1)
				case "old unmarked row grew":
					child.after = strings.Replace(eleven, "▸ QA-M1: also run: echo QA-M1 >> /tmp/q8a.log", "▸ QA-M1: also run: echo QA-M1 >> /tmp/q8a.log EXTRA", 1)
				case "old row changed":
					child.after = strings.Replace(eleven, "▸ QA-M10:", "▸ QA-M99:", 1)
				case "ambiguous short preview":
					child.after = strings.Replace(eleven, "QA-ML-FIRST line...", "QA-ML-FIRST lin...", 1)
				case "different new item":
					child.after = strings.Replace(eleven, "QA-ML-FIRST line...", "QA-ML-FIRST wrong...", 1)
				case "two new items":
					child.after = strings.Replace(eleven, "▸ QA-ML-FIRST line...", "▸ surprise\n▸ QA-ML-FIRST line...", 1)
				}
				reply := v69Send(t, app, p.name, body, true)
				if reply.Delivery != want || child.enters.Load() != 1 {
					t.Fatalf("got %+v from %d submits, want %s", reply, child.enters.Load(), want)
				}
				if want == DeliveryQueued && !strings.HasSuffix(reply.Data, "10 ahead") {
					t.Errorf("wrong queue position in %q", reply.Data)
				}
			})
		})
	}
}

func TestAgentSendRenderedCorpus(t *testing.T) {
	// Bodies taken from the qad sender; every item in both real queues is exercised.
	long := "this is a realistic longer steering message from a super that explains context at some length so that it is well over one screen row wide in the tile; please also run "
	kinds := []string{"plain", "tick", "long", "longtick", "plain", "tick", "longtick", "plain", "longtick", "tick", "multiline"}
	for _, tc := range []struct {
		file  string
		count int
	}{{"qa2-agy-ten", 10}, {"qa2-agy-eleven", 11}} {
		f := agyReader.read(strings.Split(agentCapture(t, tc.file), "\n"))
		if len(f.queued) != tc.count {
			t.Fatalf("%s showed %d items, want %d", tc.file, len(f.queued), tc.count)
		}
		for j, kind := range kinds[:tc.count] {
			mark := fmt.Sprintf("QA-M%d", j+1)
			cmd := "echo " + mark + " >> /tmp/q8a.log"
			body := mark + ": "
			switch kind {
			case "plain":
				body += "also run: " + cmd
			case "tick":
				body += "also run `" + cmd + "`"
			case "long":
				body += strings.TrimSuffix(long, " ") + ": " + cmd + " and then carry on with the current step as before."
			case "longtick":
				body += long + "`" + cmd + "` and then carry on with the current step as before."
			case "multiline":
				body = "QA-ML-FIRST line\nQA-ML-LAST line"
			}
			if got := newMessageProof(body).queueIndex(agyReader, f.queued); got != j {
				t.Errorf("%s item %d (%s) matched index %d", tc.file, j, kind, got)
			}
		}
	}
}

func TestAgentSendRenderedProofBothTools(t *testing.T) {
	for _, agent := range []struct {
		name, turn, queue string
		r                 agentReader
	}{{"codex", "›", "↳", codexReader}, {"agy", ">", "▸", agyReader}} {
		for _, tc := range []struct {
			name, body, shown string
			want              bool
		}{
			{"inline", "Please run `echo mark >> out`", "Please run echo mark >> out", true},
			{"emphasis", "Please review **the report** for _Tuesday_", "Please review the report for Tuesday", true},
			{"spaces", "Please\t review  the revised report", "Please review the revised report", true},
			{"multiline", "QA-ML-FIRST line\nQA-ML-LAST line", "QA-ML-FIRST line...", true},
			{"ellipsis", "0123456789abcdef plus remaining text", "0123456789abcdef…", true},
			{"wrapped", "Please review the revised handbook", "Please review the revised hand\n  book", true},
			{"short exact", "yes", "yes", true},
			{"too short", "0123456789abcde elsewhere", "0123456789abcde…", false},
			{"partial without ellipsis", "0123456789abcdef tail", "0123456789abcdef", true},
			{"normalized floor", "**abcdefghijklmn** remaining", "**abcdefghijklmn**...", false},
			{"literal ellipsis", "yes...", "yes...", true},
			{"short unicode", "東京東京東京東京 tail", "東京東京東京東京…", false},
			{"blank", "unused", "...", false},
			{"not a prefix", "a long message with tail", "long message with…", false},
			{"changed redirection", "Run `echo marker >> report`", "Run echo marker > report", false},
			{"surplus", "report on the messages", "report on the messages X", false},
		} {
			t.Run(agent.name+"/"+tc.name, func(t *testing.T) {
				for _, glyph := range []string{agent.turn, agent.queue} {
					rows := strings.Split(glyph+" "+tc.shown, "\n")
					got := false
					if glyph == agent.turn {
						got = newMessageProof(tc.body).userTurn(agent.r, rows)
					} else {
						got = newMessageProof(tc.body).queueIndex(agent.r, rows) >= 0
					}
					if got != tc.want {
						t.Errorf("glyph %s: match=%v, want %v", glyph, got, tc.want)
					}
				}
			})
		}
	}
}

func TestAgentSendAgyQueueWithTaskShelf(t *testing.T) {
	screen := agentCapture(t, "local-agy-queued-task-shelf")
	f := agyReader.read(strings.Split(screen, "\n"))
	if !f.busy || !f.box || f.draft != "" || f.native != 5 {
		t.Fatalf("native queue above task shelf: %+v", f)
	}
	for _, s := range []string{
		agentCapture(t, "local-agy-background-task"),
		strings.Replace(screen, "● [13:34:30]", "unexpected choice", 1),
		strings.Replace(screen, "sleep 150 running", "sleep 150 running\n  unexpected choice", 1),
	} {
		f = agyReader.read(strings.Split(s, "\n"))
		if f.box {
			t.Fatalf("read a composer through an unconfirmed shelf: %+v", f)
		}
	}
}

// The sender's original input is independent of the screen. Replay each real
// rendering at both programs' user-turn and native-queue boundaries.
func TestAgentSendRealProofRenderingsForBothTools(t *testing.T) {
	const approval = "Please request approval to create an empty file named receipt.txt in the current scratch directory with touch. The sandbox is read-only. Use the shell tool and ask me before creating the file. This is a UI test."
	const qca = "QA-M12: this is a realistic longer steering message from a super that explains context at some length so that it is well over one screen row wide in the tile; please also run `echo QA-M12 >> /tmp/q8c.log` and then carry on with the current step as before."
	for _, c := range []struct {
		file, start, body string
		height            int
	}{
		{"local-codex-0170-approval", "› Please request approval", approval, 3},
		{"local-codex-0170-queued-two", "› Please request approval", approval, 1},
		{"qa2-codex-qca-m12", "› QA-M12:", qca, 4},
		{"local-codex-after-tool", "› QA-M12:", "QA-M12: also run `echo QA-M12 >> marks.txt`", 1},
		{"local-codex-before-tool", "↳ QA-M12:", "QA-M12: also run `echo QA-M12 >> marks.txt`", 1},
		{"local-agy-1511-landed", "> Reply with exactly", "Reply with exactly Amber Receipt. Use no tools.", 1},
		{"qa2-agy-ten", "▸ QA-M10:", "QA-M10: also run `echo QA-M10 >> /tmp/q8a.log`", 1},
		{"qa2-agy-eleven", "▸ QA-ML-FIRST", "QA-ML-FIRST line\nQA-ML-LAST line", 1},
		{"local-r6-agy-queued", "▸ Please remember", "Please remember the first marker.\nPlease remember the second marker.", 1},
	} {
		t.Run(c.file, func(t *testing.T) {
			rows := strings.Split(agentCapture(t, c.file), "\n")
			var sample []string
			for i, row := range rows {
				if strings.HasPrefix(strings.TrimSpace(row), c.start) {
					sample = append([]string(nil), rows[i:i+c.height]...)
					_, sample[0], _ = strings.Cut(strings.TrimSpace(sample[0]), " ")
					sample[0] = strings.TrimSuffix(sample[0], " (alt+m to send immediately)")
					break
				}
			}
			if len(sample) == 0 {
				t.Fatal("missing captured message")
			}
			for _, tool := range []struct {
				r           agentReader
				turn, queue string
			}{{codexReader, "›", "↳"}, {agyReader, ">", "▸"}} {
				for _, body := range []string{c.body, "other " + c.body} {
					want := body == c.body
					proof := newMessageProof(body)
					turn := append([]string{tool.turn + " " + sample[0]}, sample[1:]...)
					queue := append([]string{tool.queue + " " + sample[0]}, sample[1:]...)
					if proof.userTurn(tool.r, turn) != want || (proof.queueIndex(tool.r, queue) == 0) != want {
						t.Fatalf("reader %v, body %q, sample %q, expected match %v", tool.r, body, sample, want)
					}
				}
			}
		})
	}
}

func TestAgentSendCodexQueueAlreadyVisibleWithDraft(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		const msg = "Reply Green after Blue, using no tools."
		busy := agentCapture(t, "local-codex-0170-busy")
		queue := agentCapture(t, "local-codex-0170-queued-two")
		app, p, child := agentCaptureRig(t, "codex", busy)
		child.shows = strings.Replace(queue, "› Ask Codex to do anything", "› "+msg, 1)
		child.after = queue
		reply := v69Send(t, app, p.name, msg, true)
		if reply.Delivery != DeliveryUnconfirmed || child.enters.Load() != 1 {
			t.Fatalf("pre-existing queue returned %+v, submits %d", reply, child.enters.Load())
		}
	})
}

func TestAgentSendAmbiguousQueuePrefix(t *testing.T) {
	proof := newMessageProof("0123456789abcdef the rest")
	for _, tc := range []struct {
		r     agentReader
		glyph string
	}{{codexReader, "↳ "}, {agyReader, "▸ "}} {
		index := proof.queueIndex(tc.r, []string{tc.glyph + "0123456789abcdef...", tc.glyph + "0123456789abcdef…"})
		if index != -2 {
			t.Fatalf("reader %v attributed an ambiguous queue to %d", tc.r, index)
		}
	}
}

// Check every captured proof surface, including repeated shapes at different
// widths. The separate sender-body tests above supply ground truth for the
// clipped suffixes, which cannot be recovered from a screen alone.
func TestAgentSendProofEveryCapturedFrame(t *testing.T) {
	files, err := filepath.Glob("testdata/agent_send/*.txt")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".txt")
		t.Run(name, func(t *testing.T) {
			origin, turn, queue := codexReader, "› ", "↳ "
			if strings.Contains(name, "-agy") {
				origin, turn, queue = agyReader, "> ", "▸ "
			}
			frame := origin.read(strings.Split(agentCapture(t, name), "\n"))
			for _, src := range []struct {
				rows  []string
				glyph string
			}{{frame.transcript, turn}, {frame.queued, queue}} {
				for i, row := range src.rows {
					if src.glyph == queue {
						row = strings.TrimSpace(row)
					}
					first, ok := strings.CutPrefix(row, src.glyph)
					if !ok {
						continue
					}
					lines := []string{first}
					for _, cont := range src.rows[i+1:] {
						v := strings.TrimSpace(cont)
						if v == "" || !strings.HasPrefix(cont, " ") || strings.HasPrefix(v, "↳ ") || strings.HasPrefix(v, "▸ ") || strings.HasPrefix(v, "⇧←") {
							break
						}
						lines = append(lines, v)
					}
					sent := strings.Join(lines, " \t ")
					// Supply a distinguishing suffix only to clipped examples.
					clipped := strings.HasSuffix(sent, "...") || strings.HasSuffix(sent, "…")
					if clipped {
						sent = strings.TrimSuffix(strings.TrimSuffix(sent, "..."), "…") + " remainder of captured preview"
					}
					for _, dest := range []struct {
						r           agentReader
						turn, queue string
					}{{codexReader, "› ", "↳ "}, {agyReader, "> ", "▸ "}} {
						shown := append([]string{dest.turn + lines[0]}, lines[1:]...)
						queued := append([]string{dest.queue + lines[0]}, lines[1:]...)
						positive := newMessageProof(sent)
						if !positive.userTurn(dest.r, shown) || positive.queueIndex(dest.r, queued) != 0 {
							t.Fatalf("reader %d failed captured %q", dest.r, lines)
						}
						negative := newMessageProof("DIFFERENT " + sent)
						if negative.userTurn(dest.r, shown) || negative.queueIndex(dest.r, queued) != -1 {
							t.Fatalf("reader %d accepted different text for %q", dest.r, lines)
						}
					}
					seen[src.glyph]++
				}
			}
		})
	}
	for _, glyph := range []string{"› ", "↳ ", "> ", "▸ "} {
		if seen[glyph] == 0 {
			t.Fatalf("no corpus examples for %q", glyph)
		}
	}
	t.Logf("%d captured frames; user/queue rows exercised on both agents: %v", len(files), seen)
}

func TestAgentSendCodexWorkingWithWeeklyWarning(t *testing.T) {
	const msg = "QA-M12: also run `echo QA-M12 >> marks.txt`"
	const displayed = "› QA-M12: also run echo QA-M12 >> marks.txt"
	screen := agentCapture(t, "local-codex-weekly-warning")
	before := strings.Replace(screen, displayed, "", 1)
	sendBubble(t, func(t *testing.T) {
		app, p, child := agentCaptureRig(t, "codex", before)
		child.shows = strings.Replace(before, "› Ask Codex to do anything", "› "+msg, 1)
		child.after = screen
		reply := v69Send(t, app, p.name, msg, true)
		if reply.Delivery != DeliverySteered || child.enters.Load() != 1 {
			t.Fatalf("working send returned %+v from %d submits", reply, child.enters.Load())
		}
	})
	for _, unrecognized := range []string{
		strings.Replace(before, "⚠ weekly limit: 20% left · /status", "unrelated transcript line", 1),
		strings.Replace(before, "• Working (17s • esc to interrupt)", "Completed", 1),
	} {
		f := codexReader.read(strings.Split(unrecognized, "\n"))
		if f.busy || !f.box {
			t.Fatalf("unrecognized or idle status classified incorrectly: %+v", f)
		}
	}
}
