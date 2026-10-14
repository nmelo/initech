package roles

import (
	"reflect"
	"strings"
	"testing"
)

func TestQAPairs_SplitsEngineersIntoContiguousBlocks(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  []QAPair
	}{
		{"2 eng + 1 qa", []string{"super", "eng1", "eng2", "qa1"},
			[]QAPair{{QA: "qa1", Engineers: []string{"eng1", "eng2"}}}},
		{"6 eng + 3 qa", []string{"super", "pm", "eng1", "eng2", "eng3", "eng4", "eng5", "eng6", "qa1", "qa2", "qa3"},
			[]QAPair{
				{QA: "qa1", Engineers: []string{"eng1", "eng2"}},
				{QA: "qa2", Engineers: []string{"eng3", "eng4"}},
				{QA: "qa3", Engineers: []string{"eng5", "eng6"}},
			}},
		{"5 eng + 2 qa: the extra goes to the first QA", []string{"eng1", "eng2", "eng3", "eng4", "eng5", "qa1", "qa2"},
			[]QAPair{
				{QA: "qa1", Engineers: []string{"eng1", "eng2", "eng3"}},
				{QA: "qa2", Engineers: []string{"eng4", "eng5"}},
			}},
		{"one QA takes every engineer", []string{"eng1", "eng2", "eng3", "qa1"},
			[]QAPair{{QA: "qa1", Engineers: []string{"eng1", "eng2", "eng3"}}}},
		{"more QA than engineers: spare QA get no pair", []string{"eng1", "qa1", "qa2"},
			[]QAPair{{QA: "qa1", Engineers: []string{"eng1"}}}},
		{"3 eng + 0 qa", []string{"super", "eng1", "eng2", "eng3"}, nil},
		{"0 eng + 1 qa", []string{"super", "qa1"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := QAPairs(tc.roles)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("QAPairs(%v)\n got %+v\nwant %+v", tc.roles, got, tc.want)
			}
		})
	}
}

func TestRenderQAPairsBlock_TableMatchesRoster(t *testing.T) {
	out := RenderQAPairsBlock([]string{"super", "eng1", "eng2", "eng3", "eng4", "eng5", "qa1", "qa2"})
	for _, want := range []string{
		QAPairsBegin,
		QAPairsEnd,
		"| eng1, eng2, eng3 | qa1 |",
		"| eng4, eng5 | qa2 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("section missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no QA role") {
		t.Errorf("a roster with QA must not say there is none:\n%s", out)
	}
}

func TestRenderQAPairsBlock_NoQARoleNamesWhoGrades(t *testing.T) {
	out := RenderQAPairsBlock([]string{"super", "eng1", "eng2", "eng3"})
	if !strings.Contains(out, "no QA role: super grades or the operator adds one") {
		t.Errorf("no-QA roster must say who grades:\n%s", out)
	}
	if strings.Contains(out, "| eng1") {
		t.Errorf("no-QA roster must not render a pairs row:\n%s", out)
	}
	if !strings.Contains(out, QAPairsBegin) || !strings.Contains(out, QAPairsEnd) {
		t.Errorf("markers must be present so add-agent can fill the table later:\n%s", out)
	}
}

func TestReplaceQAPairsBlock(t *testing.T) {
	oldRoles := []string{"super", "eng1", "eng2", "qa1"}
	newRoles := append(append([]string{}, oldRoles...), "eng3")
	doc := "# p\n\nintro\n\n" + RenderQAPairsBlock(oldRoles) + "\n## Beads\n\nrest\n"

	t.Run("unedited block is re-rendered and the rest is untouched", func(t *testing.T) {
		got, res := ReplaceQAPairsBlock(doc, oldRoles, newRoles)
		if res != PairsUpdated {
			t.Fatalf("result = %v, want PairsUpdated", res)
		}
		if !strings.Contains(got, "| eng1, eng2, eng3 | qa1 |") {
			t.Errorf("new engineer not in table:\n%s", got)
		}
		if !strings.HasPrefix(got, "# p\n\nintro\n\n") || !strings.HasSuffix(got, "\n## Beads\n\nrest\n") {
			t.Errorf("text outside the block changed:\n%s", got)
		}
		if strings.Count(got, QAPairsBegin) != 1 {
			t.Errorf("begin marker count = %d, want 1", strings.Count(got, QAPairsBegin))
		}
	})

	t.Run("a hand-edited block is left alone", func(t *testing.T) {
		edited := strings.Replace(doc, "| eng1, eng2 | qa1 |", "| eng1 | qa1 |\n| eng2 | qa1 |", 1)
		if edited == doc {
			t.Fatal("fixture edit did not apply")
		}
		got, res := ReplaceQAPairsBlock(edited, oldRoles, newRoles)
		if res != PairsEdited {
			t.Fatalf("result = %v, want PairsEdited", res)
		}
		if got != edited {
			t.Errorf("edited document was changed")
		}
	})

	t.Run("block already rendered for the new roster", func(t *testing.T) {
		fresh := "# p\n\n" + RenderQAPairsBlock(newRoles)
		got, res := ReplaceQAPairsBlock(fresh, oldRoles, newRoles)
		if res != PairsCurrent || got != fresh {
			t.Errorf("result = %v, changed = %v; want PairsCurrent, unchanged", res, got != fresh)
		}
	})

	t.Run("no markers: left alone", func(t *testing.T) {
		plain := "# p\n\nno pairs here\n"
		got, res := ReplaceQAPairsBlock(plain, oldRoles, newRoles)
		if res != PairsMissing || got != plain {
			t.Errorf("result = %v, changed = %v; want PairsMissing, unchanged", res, got != plain)
		}
	})

	t.Run("empty document", func(t *testing.T) {
		got, res := ReplaceQAPairsBlock("", oldRoles, newRoles)
		if res != PairsMissing || got != "" {
			t.Errorf("result = %v, got %q; want PairsMissing, empty", res, got)
		}
	})
}
