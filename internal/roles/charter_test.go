package roles

import (
	"strings"
	"testing"
)

func TestParseCharter_AFreshCharterIsUnedited(t *testing.T) {
	body := "# CLAUDE.md\n\nbody\n"
	body2, hasHeader, unedited := ParseCharter(CharterHeader("eng", "eng1", body) + body)
	if !hasHeader || !unedited || body2 != body {
		t.Fatalf("hasHeader=%v unedited=%v body=%q, want a fresh charter to parse as unedited", hasHeader, unedited, body2)
	}
}

func TestParseCharter_AnEditBelowTheHeaderIsDetected(t *testing.T) {
	body := "# CLAUDE.md\n\nbody\n"
	text := CharterHeader("eng", "eng1", body) + body + "my own rule\n"
	if _, hasHeader, unedited := ParseCharter(text); !hasHeader || unedited {
		t.Fatalf("hasHeader=%v unedited=%v, want an edited body reported as edited", hasHeader, unedited)
	}
}

func TestParseCharter_AHeaderlessFileHasNoHeader(t *testing.T) {
	for _, text := range []string{"", "# CLAUDE.md\n\nhand written\n", "<!-- generated from the eng template. -->\n# no hash line\n"} {
		if _, hasHeader, unedited := ParseCharter(text); hasHeader || unedited {
			t.Errorf("ParseCharter(%q): hasHeader=%v unedited=%v, want neither", text, hasHeader, unedited)
		}
	}
}

func TestCharterHeader_NamesTheTemplateAndTheRegenCommand(t *testing.T) {
	h := CharterHeader("qa", "qa2", "x")
	lines := strings.Split(strings.TrimSuffix(h, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("header has %d lines, want 2:\n%s", len(lines), h)
	}
	for _, want := range []string{"the qa template", "initech add-agent qa2 --regen", "--force"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header line 1 = %q, want it to contain %q", lines[0], want)
		}
	}
	if !strings.HasPrefix(lines[1], "<!-- initech-hash: sha256:") {
		t.Errorf("header line 2 = %q, want the hash line", lines[1])
	}
}
