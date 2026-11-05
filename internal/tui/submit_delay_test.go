package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The production submit waits are what real agents get: a test shortening
// them must never leak into what ships (ini-02h).
func TestSubmitDelays_ProductionValuesAre500And200ms(t *testing.T) {
	if prodBracketedPasteSubmitDelay != 500*time.Millisecond {
		t.Errorf("prodBracketedPasteSubmitDelay = %s, want 500ms", prodBracketedPasteSubmitDelay)
	}
	if prodRawSubmitDelay != 200*time.Millisecond {
		t.Errorf("prodRawSubmitDelay = %s, want 200ms", prodRawSubmitDelay)
	}
	if got := bracketedPasteSubmitDelay.Get(); got != 500*time.Millisecond {
		t.Errorf("bracketedPasteSubmitDelay outside any helper = %s, want 500ms", got)
	}
	if got := rawSubmitDelay.Get(); got != 200*time.Millisecond {
		t.Errorf("rawSubmitDelay outside any helper = %s, want 200ms", got)
	}
}

// A helper's shrink lasts exactly as long as the test that asked for it.
func TestSubmitDelays_HelperShrinkIsGoneAfterCleanup(t *testing.T) {
	t.Run("inside", func(t *testing.T) {
		shortSubmitDelays(t)
		if got := bracketedPasteSubmitDelay.Get(); got != testSubmitDelay {
			t.Fatalf("bracketedPasteSubmitDelay inside the helper = %s, want %s", got, testSubmitDelay)
		}
		if got := rawSubmitDelay.Get(); got != testSubmitDelay {
			t.Fatalf("rawSubmitDelay inside the helper = %s, want %s", got, testSubmitDelay)
		}
	})
	if got := bracketedPasteSubmitDelay.Get(); got != prodBracketedPasteSubmitDelay {
		t.Errorf("bracketedPasteSubmitDelay after Cleanup = %s, want %s", got, prodBracketedPasteSubmitDelay)
	}
	if got := rawSubmitDelay.Get(); got != prodRawSubmitDelay {
		t.Errorf("rawSubmitDelay after Cleanup = %s, want %s", got, prodRawSubmitDelay)
	}
}

// The delivering helpers the -short suite leans on run with the short waits,
// which is what keeps the suite near its charter time (ini-02h).
func TestSubmitDelays_DeliveringHelperRunsShort(t *testing.T) {
	dndPane(t, "a")
	if got := bracketedPasteSubmitDelay.Get(); got != testSubmitDelay {
		t.Fatalf("bracketedPasteSubmitDelay under dndPane = %s, want %s", got, testSubmitDelay)
	}
}

// directSet matches any write to a submit wait.
var directSet = regexp.MustCompile(`(rawSubmitDelay|bracketedPasteSubmitDelay)\.(set|ns)\b`)

// Only shortSubmitDelays writes the waits; a test swapping them by hand is
// the bare-swap shape the atomic setting exists to end (ini-tio).
func TestSubmitDelays_NoTestSetsThemDirectly(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob test files: %v (%d found)", err, len(files))
	}
	sawHelper := false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !directSet.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "var directSet") {
				continue
			}
			if f == "test_helpers_test.go" {
				sawHelper = true
				continue
			}
			t.Errorf("%s:%d sets a submit wait directly; use shortSubmitDelays(t): %s", f, i+1, strings.TrimSpace(line))
		}
	}
	if !sawHelper {
		t.Fatal("census found no write in test_helpers_test.go: the pattern no longer sees the helper, so it would miss a direct set too")
	}
}
