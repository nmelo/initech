package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestFN77Viewer_ManagementNoticeSurvivesAuthorityFooter(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(130, 44)
	u := newTestTUI()
	u.screen = s
	u.windowID = "window-2"
	u.viewerConnected = true
	u.viewerAuthority = &AuthorityIdentity{PID: 123, StartedAt: time.Now().UTC()}
	u.identityLineShown = true // hidden by default since ini-evdn; Option+w shows it
	u.agentEvents = make(chan AgentEvent, 4)
	u.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	select {
	case ev := <-u.agentEvents:
		if !strings.Contains(ev.Detail, "available in the main window") {
			t.Fatal(ev.Detail)
		}
		u.handleAgentEvent(ev)
	default:
		t.Fatal("Option+A never emitted its notice")
	}
	u.render()
	text := readScreenRect(s, 0, 0, 130, 44)
	if !strings.Contains(text, "main PID 123") {
		t.Fatal("authority footer missing")
	}
	if !strings.Contains(text, "available in the main") {
		t.Fatal("notice was emitted, but the full render erased it with the authority footer")
	}
}

func TestFN77Notices_StackAboveViewerFooterAndKeepMainPosition(t *testing.T) {
	for _, window := range []string{WindowOne, "window-2"} {
		t.Run(window, func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			if err := s.Init(); err != nil {
				t.Fatal(err)
			}
			defer s.Fini()
			s.SetSize(130, 44)
			u := newTestTUI()
			u.screen = s
			u.windowID = window
			u.viewerConnected = true
			u.viewerAuthority = &AuthorityIdentity{PID: 123, StartedAt: time.Now().UTC()}
			u.identityLineShown = true // hidden by default since ini-evdn; Option+w shows it
			u.notifications = []notification{
				{event: AgentEvent{Detail: "older notice"}},
				{event: AgentEvent{Detail: "newer notice"}},
			}
			u.render()
			base := 42
			if window != WindowOne {
				base = 41
			}
			if !strings.Contains(readScreenRect(s, 0, base, 130, 1), "newer notice") {
				t.Fatal("newest notice missing or misplaced")
			}
			if !strings.Contains(readScreenRect(s, 0, base-1, 130, 1), "older notice") {
				t.Fatal("stacked notice missing or misplaced")
			}
			if window != WindowOne && !strings.Contains(readScreenRect(s, 0, 42, 130, 1), "main PID 123") {
				t.Fatal("notices covered the authority identity")
			}
		})
	}
}
