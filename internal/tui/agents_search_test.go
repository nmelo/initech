// Tests for agent modal search/filter (/ keystroke).
package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestAgentsRefilter_EmptyQueryShowsAll(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"))
	tui.agents.searching = true
	tui.agents.searchBuf = nil
	tui.agentsRefilter()

	if len(tui.agents.filtered) != 3 {
		t.Errorf("expected 3 matches, got %d", len(tui.agents.filtered))
	}
}

func TestAgentsRefilter_SubstringMatch(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"), testPane("super"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agentsRefilter()

	if len(tui.agents.filtered) != 2 {
		t.Errorf("expected 2 matches for 'eng', got %d", len(tui.agents.filtered))
	}
	if tui.agents.filtered[0] != 0 || tui.agents.filtered[1] != 1 {
		t.Errorf("filtered indices = %v, want [0, 1]", tui.agents.filtered)
	}
}

func TestAgentsRefilter_CaseInsensitive(t *testing.T) {
	tui := newTestTUI(testPane("Eng1"), testPane("eng2"), testPane("QA1"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("ENG")
	tui.agentsRefilter()

	if len(tui.agents.filtered) != 2 {
		t.Errorf("expected 2 matches for 'ENG', got %d", len(tui.agents.filtered))
	}
}

func TestAgentsRefilter_NoMatches(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("xyz")
	tui.agentsRefilter()

	if len(tui.agents.filtered) != 0 {
		t.Errorf("expected 0 matches for 'xyz', got %d", len(tui.agents.filtered))
	}
}

func TestAgentsRefilter_SelectionClamped(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"), testPane("super"))
	tui.agents.searching = true
	tui.agents.selected = 3 // pointing at "super"
	tui.agents.searchBuf = []rune("eng")
	tui.agentsRefilter()

	// Only 2 matches, so selected should clamp to 1 (last index).
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (clamped)", tui.agents.selected)
	}
}

func TestAgentsRefilter_SingleChar(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("q")
	tui.agentsRefilter()

	if len(tui.agents.filtered) != 1 {
		t.Errorf("expected 1 match for 'q', got %d", len(tui.agents.filtered))
	}
	if tui.agents.filtered[0] != 2 {
		t.Errorf("filtered[0] = %d, want 2 (qa1)", tui.agents.filtered[0])
	}
}

func TestAgentsSearch_EnterSelectsFromFiltered(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"), testPane("super"))
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agentsRefilter()

	// Move to second filtered item (eng2, which is pane index 1).
	tui.agents.selected = 1

	// Simulate Enter.
	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	// After Enter, searching should be off and selected should be pane index 1.
	if tui.agents.searching {
		t.Error("searching should be false after Enter")
	}
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (eng2 pane index)", tui.agents.selected)
	}
}

func TestAgentsSearch_EscClearsSearch(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agentsRefilter()

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

	if tui.agents.searching {
		t.Error("searching should be false after Esc")
	}
	if tui.agents.searchBuf != nil {
		t.Error("searchBuf should be nil after Esc")
	}
	if tui.agents.filtered != nil {
		t.Error("filtered should be nil after Esc")
	}
	if tui.agents.selected != 0 {
		t.Errorf("selected should reset to 0, got %d", tui.agents.selected)
	}
}

func TestAgentsSearch_BackspaceRemovesRune(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("en")
	tui.agentsRefilter()

	if len(tui.agents.filtered) != 1 {
		t.Fatalf("pre-check: expected 1 match for 'en', got %d", len(tui.agents.filtered))
	}

	// Backspace to "e".
	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))

	if string(tui.agents.searchBuf) != "e" {
		t.Errorf("searchBuf = %q, want %q", string(tui.agents.searchBuf), "e")
	}
	// "e" matches eng1 still.
	if len(tui.agents.filtered) != 1 {
		t.Errorf("expected 1 match for 'e', got %d", len(tui.agents.filtered))
	}
}

func TestAgentsSearch_ResetOnModalClose(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agentsRefilter()

	// Reopen modal.
	tui.openAgentsModal()

	if tui.agents.searching {
		t.Error("searching should be false on reopen")
	}
	if tui.agents.searchBuf != nil {
		t.Error("searchBuf should be nil on reopen")
	}
	if tui.agents.filtered != nil {
		t.Error("filtered should be nil on reopen")
	}
}

func TestAgentsSearch_SlashOnlyWhenNotSearching(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.agents.active = true

	// / from normal mode enters search.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if !tui.agents.searching {
		t.Fatal("expected searching to be true after /")
	}

	// / while searching should be treated as printable (appended to searchBuf).
	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	// '/' is printable, so it gets appended.
	if string(tui.agents.searchBuf) != "/" {
		t.Errorf("expected '/' in searchBuf, got %q", string(tui.agents.searchBuf))
	}
}

func TestAgentsFilteredCount(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"))

	// No filter.
	if tui.agentsFilteredCount() != 3 {
		t.Errorf("no filter: got %d, want 3", tui.agentsFilteredCount())
	}

	// With filter.
	tui.agents.filtered = []int{0, 1}
	if tui.agentsFilteredCount() != 2 {
		t.Errorf("filtered: got %d, want 2", tui.agentsFilteredCount())
	}
}
