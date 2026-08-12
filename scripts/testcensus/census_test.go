package main

// census_test.go covers the census comparison itself (ini-2x8.8).
//
// A guardrail whose own logic is untested is theater: it would report green
// against a broken comparison exactly as convincingly as against a healthy
// suite, which is the same failure -- a reassuring signal that witnesses
// nothing -- that this check was built to end.

import (
	"strings"
	"testing"
)

func census(entries map[string][]string) Census {
	c := Census{}
	for platform, keys := range entries {
		set := map[string]bool{}
		for _, k := range keys {
			set[k] = true
		}
		c[platform] = set
	}
	return c
}

func TestCompare_SameSuiteEverywherePasses(t *testing.T) {
	c := census(map[string][]string{
		"linux":   {"internal/tui/A_test.go", "internal/tui/B_test.go"},
		"darwin":  {"internal/tui/A_test.go", "internal/tui/B_test.go"},
		"windows": {"internal/tui/A_test.go", "internal/tui/B_test.go"},
	})
	problems, stale := Compare(c, nil)
	if len(problems) != 0 {
		t.Errorf("problems on an identical suite: %v", problems)
	}
	if len(stale) != 0 {
		t.Errorf("stale exemptions reported with none declared: %v", stale)
	}
}

// TestCompare_AbsentOnOnePlatformFails is the case the whole check exists for:
// the ini-47w shape, where a suite silently left one leg.
func TestCompare_AbsentOnOnePlatformFails(t *testing.T) {
	c := census(map[string][]string{
		"linux":   {"internal/tui/Detect_test.go", "internal/tui/Shared_test.go"},
		"darwin":  {"internal/tui/Detect_test.go", "internal/tui/Shared_test.go"},
		"windows": {"internal/tui/Shared_test.go"},
	})
	problems, _ := Compare(c, nil)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1: %v", len(problems), problems)
	}
	p := problems[0]
	if p.Key != "internal/tui/Detect_test.go" {
		t.Errorf("wrong test flagged: %q", p.Key)
	}
	if len(p.Absent) != 1 || p.Absent[0] != "windows" {
		t.Errorf("absent = %v, want [windows]", p.Absent)
	}
	if got := strings.Join(p.Present, ","); got != "darwin,linux" {
		t.Errorf("present = %q, want darwin,linux", got)
	}
}

func TestCompare_DeclaredExemptionPasses(t *testing.T) {
	c := census(map[string][]string{
		"linux":   {"internal/tui/LiveProbe_test.go"},
		"windows": {},
	})
	ex := []Exemption{{
		Key:       "internal/tui/LiveProbe_test.go",
		Platforms: map[string]bool{"windows": true},
		Reason:    "drives a PTY via creack/pty",
	}}
	problems, stale := Compare(c, ex)
	if len(problems) != 0 {
		t.Errorf("declared exemption did not suppress the problem: %v", problems)
	}
	if len(stale) != 0 {
		t.Errorf("an exemption in active use was reported stale: %v", stale)
	}
}

// TestCompare_ExemptionOnlyCoversThePlatformsItNames stops an exemption from
// becoming a blanket licence: excusing a test on windows must not also excuse it
// vanishing from darwin.
func TestCompare_ExemptionOnlyCoversThePlatformsItNames(t *testing.T) {
	c := census(map[string][]string{
		"linux":   {"internal/tui/LiveProbe_test.go"},
		"darwin":  {},
		"windows": {},
	})
	ex := []Exemption{{
		Key:       "internal/tui/LiveProbe_test.go",
		Platforms: map[string]bool{"windows": true},
		Reason:    "drives a PTY",
	}}
	problems, _ := Compare(c, ex)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1 (darwin absence is not covered)", len(problems))
	}
	if len(problems[0].Absent) != 1 || problems[0].Absent[0] != "darwin" {
		t.Errorf("absent = %v, want [darwin] only", problems[0].Absent)
	}
}

// TestCompare_StaleExemptionIsReported guards the hole this check takes AFTER
// someone fixes an underlying constraint: an exemption that has outlived its
// cause is a standing licence for that suite to vanish again unnoticed.
func TestCompare_StaleExemptionIsReported(t *testing.T) {
	// The test now compiles everywhere, so the exemption no longer applies.
	c := census(map[string][]string{
		"linux":   {"internal/tui/OnceConstrained_test.go"},
		"windows": {"internal/tui/OnceConstrained_test.go"},
	})
	ex := []Exemption{{
		Key:       "internal/tui/OnceConstrained_test.go",
		Platforms: map[string]bool{"windows": true},
		Reason:    "was pty-bound before the split",
	}}
	problems, stale := Compare(c, ex)
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
	if len(stale) != 1 {
		t.Fatalf("got %d stale exemptions, want 1", len(stale))
	}
}

func TestCompare_ExemptionForADeletedTestIsStale(t *testing.T) {
	c := census(map[string][]string{
		"linux":   {"internal/tui/StillHere_test.go"},
		"windows": {"internal/tui/StillHere_test.go"},
	})
	ex := []Exemption{{
		Key:       "internal/tui/DeletedLastMonth_test.go",
		Platforms: map[string]bool{"windows": true},
		Reason:    "gone",
	}}
	_, stale := Compare(c, ex)
	if len(stale) != 1 {
		t.Fatalf("an exemption for a test that no longer exists must be reported, got %d", len(stale))
	}
}

func TestCompare_SinglePlatformComparesNothing(t *testing.T) {
	// Guarded at the caller too, but pinned here: one leg has nothing to
	// disagree with, and reporting green would be a vacuous pass.
	c := census(map[string][]string{"linux": {"internal/tui/A_test.go"}})
	problems, stale := Compare(c, nil)
	if len(problems) != 0 || len(stale) != 0 {
		t.Errorf("single-platform compare produced findings: %v %v", problems, stale)
	}
}

// ── Exemption file ──────────────────────────────────────────────────

func TestParseExemptions_ReadsAWellFormedEntry(t *testing.T) {
	src := "# header comment\n\n" +
		"internal/tui/attention_emitter_probe_test.go windows # drives a PTY via creack/pty\n"
	ex, errs := ParseExemptions(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(ex) != 1 {
		t.Fatalf("got %d exemptions, want 1", len(ex))
	}
	if ex[0].Key != "internal/tui/attention_emitter_probe_test.go" {
		t.Errorf("key = %q", ex[0].Key)
	}
	if !ex[0].Platforms["windows"] || len(ex[0].Platforms) != 1 {
		t.Errorf("platforms = %v", ex[0].Platforms)
	}
	if ex[0].Reason != "drives a PTY via creack/pty" {
		t.Errorf("reason = %q", ex[0].Reason)
	}
}

// TestParseExemptions_RejectsAnEntryWithNoReason is the rule that keeps this
// file from becoming the bug it prevents. The failure being guarded against is
// a constraint nobody consciously chose; an undocumented exemption is that
// same thing, written down.
func TestParseExemptions_RejectsAnEntryWithNoReason(t *testing.T) {
	for _, src := range []string{
		"internal/tui/foo_test.go windows\n",
		"internal/tui/foo_test.go windows #\n",
		"internal/tui/foo_test.go windows #   \n",
	} {
		ex, errs := ParseExemptions(src)
		if len(errs) == 0 {
			t.Errorf("accepted a reasonless exemption: %q", src)
		}
		if len(ex) != 0 {
			t.Errorf("kept a reasonless exemption: %q -> %v", src, ex)
		}
	}
}

func TestParseExemptions_RejectsMalformedLines(t *testing.T) {
	src := "internal/tui/foo_test.go extra_field windows # too many fields\n"
	_, errs := ParseExemptions(src)
	if len(errs) != 1 {
		t.Errorf("got %d errors, want 1", len(errs))
	}
}

func TestParseExemptions_ReadsMultiplePlatforms(t *testing.T) {
	ex, errs := ParseExemptions("internal/tui/foo_test.go windows,darwin # needs /proc\n")
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !ex[0].Platforms["windows"] || !ex[0].Platforms["darwin"] || len(ex[0].Platforms) != 2 {
		t.Errorf("platforms = %v", ex[0].Platforms)
	}
}
