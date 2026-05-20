package lifecycle

import (
	"errors"
	"reflect"
	"testing"
)

// stubConfigGet replaces ConfigGetFn for the duration of t. The returned
// teardown restores the original; t.Cleanup wires it up automatically.
func stubConfigGet(t *testing.T, value string, err error) {
	t.Helper()
	orig := ConfigGetFn
	ConfigGetFn = func(key string) (string, error) {
		if key != "status.custom" {
			t.Errorf("ConfigGetFn called with unexpected key %q, want %q", key, "status.custom")
		}
		return value, err
	}
	t.Cleanup(func() { ConfigGetFn = orig })
}

func TestLoadChain_DefaultProject(t *testing.T) {
	// Initech's standard configuration: four custom states between
	// in_progress and closed.
	stubConfigGet(t, "ready_for_qa,in_qa,qa_passed,ready_to_ship", nil)

	got, err := LoadChain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"open", "in_progress", "ready_for_qa", "in_qa", "qa_passed", "ready_to_ship", "closed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chain = %v\n want %v", got, want)
	}
}

func TestLoadChain_NoCustom(t *testing.T) {
	// Project with no custom states: chain collapses to the wrapper only.
	stubConfigGet(t, "", nil)

	got, err := LoadChain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"open", "in_progress", "closed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chain = %v\n want %v", got, want)
	}
}

func TestLoadChain_CustomMiddle(t *testing.T) {
	// A different project with a code-review-heavy flow.
	stubConfigGet(t, "design_review,code_review,ready_to_ship", nil)

	got, err := LoadChain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"open", "in_progress", "design_review", "code_review", "ready_to_ship", "closed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chain = %v\n want %v", got, want)
	}
}

func TestLoadChain_TrimsWhitespaceAndEmpties(t *testing.T) {
	// Operator-edited config with stray spaces / blank entries shouldn't
	// produce empty positions in the chain.
	stubConfigGet(t, " a, b , , c ", nil)

	got, err := LoadChain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"open", "in_progress", "a", "b", "c", "closed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chain = %v\n want %v", got, want)
	}
}

func TestLoadChain_BdUnavailable_HardFails(t *testing.T) {
	// Per ini-6e54 Q4: when bd is unreachable or errors out, deliver must
	// refuse to write status with a clear error. The walker has no
	// implicit fallback chain.
	stubConfigGet(t, "", errors.New("bd: command not found"))

	got, err := LoadChain()
	if err == nil {
		t.Fatalf("expected error when bd is unavailable; got chain %v", got)
	}
	if got != nil {
		t.Errorf("chain should be nil on error, got %v", got)
	}
	// Error message should give the operator a fix direction.
	if msg := err.Error(); msg == "" {
		t.Error("error message is empty; operator gets no fix direction")
	}
}

func TestNextState(t *testing.T) {
	chain := []string{"open", "in_progress", "ready_for_qa", "in_qa", "qa_passed", "ready_to_ship", "closed"}

	tests := []struct {
		current string
		want    string
		wantOk  bool
	}{
		{"open", "in_progress", true},
		{"in_progress", "ready_for_qa", true},
		{"ready_for_qa", "in_qa", true},
		{"in_qa", "qa_passed", true},
		{"qa_passed", "ready_to_ship", true},
		{"ready_to_ship", "closed", true},
		{"closed", "", false}, // terminal: no advance
		{"unknown", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			got, ok := NextState(chain, tt.current)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("NextState(%q) = (%q, %v), want (%q, %v)", tt.current, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

func TestPrevState(t *testing.T) {
	chain := []string{"open", "in_progress", "ready_for_qa", "in_qa", "qa_passed", "ready_to_ship", "closed"}

	tests := []struct {
		current string
		want    string
		wantOk  bool
	}{
		{"open", "", false}, // initial: no walk-back
		{"in_progress", "open", true},
		{"ready_for_qa", "in_progress", true},
		{"in_qa", "ready_for_qa", true},
		{"qa_passed", "in_qa", true},
		{"ready_to_ship", "qa_passed", true},
		{"closed", "ready_to_ship", true},
		{"unknown", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			got, ok := PrevState(chain, tt.current)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("PrevState(%q) = (%q, %v), want (%q, %v)", tt.current, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

// TestNextState_BareChain_NoCustom verifies the walker behaves correctly
// when the chain is just the wrapper (project has no custom states). This
// is the minimal valid chain — the walker still has to advance through it.
func TestNextState_BareChain_NoCustom(t *testing.T) {
	chain := []string{"open", "in_progress", "closed"}

	got, ok := NextState(chain, "in_progress")
	if got != "closed" || !ok {
		t.Errorf("NextState(in_progress) on bare chain = (%q, %v), want (closed, true)", got, ok)
	}

	// Terminal still terminates.
	got, ok = NextState(chain, "closed")
	if got != "" || ok {
		t.Errorf("NextState(closed) = (%q, %v), want (\"\", false)", got, ok)
	}
}
