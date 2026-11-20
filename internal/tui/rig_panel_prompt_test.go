package tui

import (
	"testing"
	"time"
)

func TestAgentsBox_ClearedPromptStaysClearedWhileMonitorActivityFlips(t *testing.T) {
	var st panelSettledState
	st.settle("prompt", panelNow, panelNow)
	resumed := panelNow.Add(9 * time.Second)
	// North's monitor kept the pane flipping for eleven minutes. Only
	// the first change leaves a prompt; later flicker keeps the run's age.
	for tick := 0; tick <= 22; tick++ {
		since := resumed.Add(time.Duration(tick) * 30 * time.Second)
		raw := "running"
		if tick%2 == 1 {
			raw = "background"
		}
		word, began := st.settle(raw, since, since.Add(time.Second))
		if word != "running" || !began.Equal(resumed) {
			t.Fatalf("draw %d: got %s since %v, want running since %v", tick, word, began, resumed)
		}
	}
}

// The panel and the list reply used by status --json sample the same pane.
// The panel calls waiting "prompt" and groups pending input with idle.
func panelAndStatusWords(t *testing.T, tui *TUI, now time.Time) (string, string) {
	t.Helper()
	agents, _ := tui.panelSnapshot(now)
	working, holding, _ := tui.panelSections(agents, now)
	rows := append(working, holding...)
	panes, ok := tui.AllPanes()
	if !ok || len(panes) != 1 || len(rows) != 1 {
		t.Fatalf("expected one agent in panel and status: %d panel rows, %d status entries, ok=%v", len(rows), len(panes), ok)
	}
	return rows[0].word, panes[0].Activity
}

func TestAgentsBox_AfterPromptMatchesStatusForEveryFooter(t *testing.T) {
	quietWords := map[string]string{"background": "background", "idle": "idle", "pending": "idle"}
	for _, name := range gavCorpusNames(t) {
		t.Run(name, func(t *testing.T) {
			p := testPane("video")
			p.SetBead("ini-test", "")
			tui := newTestTUI(p)
			p.activity = StateWaitingInput
			if panel, status := panelAndStatusWords(t, tui, panelNow); panel != "prompt" || status != "waiting" {
				t.Fatalf("before dismissal: panel %q, status %q", panel, status)
			}

			rows, width := corpusFooter(t, name)
			if screenShowsDialogText(rows) {
				t.Fatal("footer contains a dialog")
			}
			p.activity = StateIdle
			p.observeBackground(panelNow, true, parseFooter(rows, width))
			p.observePendingInput(rows, width)
			panel, status := panelAndStatusWords(t, tui, panelNow.Add(9*time.Second))
			if want, ok := quietWords[status]; !ok || panel != want {
				t.Errorf("on dismissal: panel %q, status %q", panel, status)
			}
		})
	}
}

func TestAgentsBox_PermissionPromptAppearsAndClearsInBothModes(t *testing.T) {
	for _, tc := range []struct {
		footer, wantRest string
	}{
		{"north_video_auto_monitor.txt", "background"},
		{"eastrig_eng9_bypass_agent.txt", "idle"},
	} {
		t.Run(tc.footer, func(t *testing.T) {
			rows, width := corpusFooter(t, tc.footer)
			p := testPane("video")
			p.emu.Resize(width, 48)
			p.agentType = "claude-code"
			p.attn = &attentionSignal{}
			p.SetBead("ini-test", "")
			tui := newTestTUI(p)
			check := func(at time.Duration, wantPanel, wantStatus string) {
				t.Helper()
				now := panelNow.Add(at)
				maintainPaneFrame(p)
				panel, status := panelAndStatusWords(t, tui, now)
				if panel != wantPanel || status != wantStatus {
					t.Fatalf("at %v: panel %q, status %q, want %q and %q", at, panel, status, wantPanel, wantStatus)
				}
			}
			lvxwPaint(p, rows)
			p.observeBackground(panelNow, true, parseFooter(rows, width))
			check(0, tc.wantRest, tc.wantRest)
			// A permission dialog replaces the footer. Replay the captured
			// question and the permission notification seen in north's log.
			lvxwPaint(p, rmgPrompt(t))
			p.attn.noteNotify("Claude needs your permission")
			check(time.Second, "prompt", "waiting")
			check(8*time.Second, "prompt", "waiting")
			lvxwPaint(p, rows)
			check(10*time.Second, tc.wantRest, tc.wantRest)
		})
	}
}
