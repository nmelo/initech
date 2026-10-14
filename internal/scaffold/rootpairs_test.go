package scaffold

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// TestRenderRootCLAUDE_QAPairsAndDirectRouting covers AC1 of the direct QA
// hand-off: the root carries Speed, Messaging between agents on a bead and QA
// pairs exactly once, and the pairs table matches the roster.
func TestRenderRootCLAUDE_QAPairsAndDirectRouting(t *testing.T) {
	cases := []struct {
		name     string
		roles    []string
		rows     []string
		noQALine bool
	}{
		{"2 eng + 1 qa", []string{"super", "eng1", "eng2", "qa1"},
			[]string{"| eng1, eng2 | qa1 |"}, false},
		{"6 eng + 3 qa", []string{"super", "pm", "eng1", "eng2", "eng3", "eng4", "eng5", "eng6", "qa1", "qa2", "qa3"},
			[]string{"| eng1, eng2 | qa1 |", "| eng3, eng4 | qa2 |", "| eng5, eng6 | qa3 |"}, false},
		{"5 eng + 2 qa", []string{"super", "eng1", "eng2", "eng3", "eng4", "eng5", "qa1", "qa2"},
			[]string{"| eng1, eng2, eng3 | qa1 |", "| eng4, eng5 | qa2 |"}, false},
		{"3 eng + 0 qa", []string{"super", "eng1", "eng2", "eng3"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderRootCLAUDE(&config.Project{Name: "p", Roles: tc.roles})
			for _, h := range []string{"\n## QA pairs\n", "\n### Speed\n", "\n### Between agents on a bead\n"} {
				if n := strings.Count(out, h); n != 1 {
					t.Errorf("heading %q appears %d times, want 1", h, n)
				}
			}
			for _, row := range tc.rows {
				if !strings.Contains(out, row) {
					t.Errorf("missing pairs row %q", row)
				}
			}
			if got := strings.Count(out, " | qa"); got != len(tc.rows) {
				t.Errorf("pairs rows = %d, want %d:\n%s", got, len(tc.rows), out)
			}
			if strings.Contains(out, "{{qa_pairs_table}}") {
				t.Errorf("pairs placeholder left unrendered")
			}
			if has := strings.Contains(out, "no QA role: the engineer's DONE evidence is the gate"); has != tc.noQALine {
				t.Errorf("no-QA line present = %v, want %v", has, tc.noQALine)
			}
		})
	}
}
