// QA tests for ini-0e5: fuzzy command suggestions in the command modal.
package tui

import "testing"

// ── levenshtein ─────────────────────────────────────────────────────

func TestLevenshtein_Identical(t *testing.T) {
	if d := levenshtein("patrol", "patrol"); d != 0 {
		t.Errorf("identical strings: got %d, want 0", d)
	}
}

func TestLevenshtein_Empty(t *testing.T) {
	if d := levenshtein("", "abc"); d != 3 {
		t.Errorf("empty vs abc: got %d, want 3", d)
	}
	if d := levenshtein("abc", ""); d != 3 {
		t.Errorf("abc vs empty: got %d, want 3", d)
	}
}

func TestLevenshtein_OneEdit(t *testing.T) {
	tests := []struct{ a, b string }{
		{"patrol", "pxtrol"},  // substitution
		{"patrol", "patro"},   // deletion
		{"patrol", "patrool"}, // insertion
	}
	for _, tc := range tests {
		if d := levenshtein(tc.a, tc.b); d != 1 {
			t.Errorf("levenshtein(%q, %q) = %d, want 1", tc.a, tc.b, d)
		}
	}
}

func TestLevenshtein_TwoEdits(t *testing.T) {
	if d := levenshtein("focus", "focu"); d != 1 {
		t.Errorf("focus vs focu: got %d, want 1", d)
	}
	if d := levenshtein("rem", "remove"); d != 3 {
		t.Errorf("rem vs remove: got %d, want 3", d)
	}
}

// ── updateSuggestions ───────────────────────────────────────────────

func TestSuggestions_PrefixMatch(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("pat")}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) == 0 {
		t.Fatal("expected suggestions for 'pat'")
	}
	if tui.cmd.suggestions[0] != "patrol" {
		t.Errorf("first suggestion = %q, want 'patrol'", tui.cmd.suggestions[0])
	}
}

func TestSuggestions_FuzzyMatch(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("focsu")}}
	tui.updateSuggestions()
	found := false
	for _, s := range tui.cmd.suggestions {
		if s == "focus" {
			found = true
		}
	}
	if !found {
		t.Errorf("'focus' should appear for 'focsu' (distance 2), got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_ExactMatchClears(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("patrol")}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) != 0 {
		t.Errorf("exact match should produce no suggestions, got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_SpaceAfterCommandClears(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("patrol ")}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) != 0 {
		t.Errorf("space after command should clear suggestions, got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_EmptyBuffer(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: nil}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) != 0 {
		t.Errorf("empty buffer should have no suggestions, got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_NoMatch(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("xyzzy")}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) != 0 {
		t.Errorf("'xyzzy' should have no suggestions, got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_MaxThree(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("r")}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) > 3 {
		t.Errorf("max 3 suggestions, got %d: %v", len(tui.cmd.suggestions), tui.cmd.suggestions)
	}
}

func TestSuggestions_AliasMatch(t *testing.T) {
	// "rm" is an exact alias, so no suggestions. But "rn" (distance 1 from "rm")
	// should suggest "remove (rm)".
	tui := &TUI{cmd: cmdModal{buf: []rune("rn")}}
	tui.updateSuggestions()
	found := false
	for _, s := range tui.cmd.suggestions {
		if s == "remove (rm)" {
			found = true
		}
	}
	if !found {
		t.Errorf("'rn' should suggest 'remove (rm)' (distance 1 from alias 'rm'), got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_AliasExactClears(t *testing.T) {
	// "q" is an alias for quit. Exact alias match should not suggest.
	tui := &TUI{cmd: cmdModal{buf: []rune("q")}}
	tui.updateSuggestions()
	// "q" is also a prefix of "quit", so it should suggest "quit" still.
	// But "q" as an alias should not produce a suggestion. Since "q" is
	// in commandAliases, the exact alias check fires and returns early.
	// Actually "q" IS an exact alias match, so no suggestions.
	if len(tui.cmd.suggestions) != 0 {
		t.Errorf("exact alias 'q' should produce no suggestions, got %v", tui.cmd.suggestions)
	}
}

func TestSuggestions_PrefixBeforeFuzzy(t *testing.T) {
	tui := &TUI{cmd: cmdModal{buf: []rune("he")}}
	tui.updateSuggestions()
	if len(tui.cmd.suggestions) == 0 {
		t.Fatal("expected suggestions for 'he'")
	}
	// "help" is a prefix match and should come first.
	if tui.cmd.suggestions[0] != "help" {
		t.Errorf("prefix match 'help' should be first, got %v", tui.cmd.suggestions)
	}
}
