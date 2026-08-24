package tui

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// withholdTestPane is a pane with a real emulator but no child: enough for the
// belt's bookkeeping, which reads the composer.
func withholdTestPane(name string) *Pane {
	return &Pane{name: name, emu: vt.NewSafeEmulator(80, 24)}
}

// safeLogBuffer is a concurrency-safe sink for captured logs.
//
// THE HELPER IS THE SYNCHRONISATION POINT, not each test that reads it.
// Production logs from goroutines it owns -- a fire-and-forget paste logs its
// failure on its own goroutine -- so the slog handler writes this buffer
// concurrently with any test that reads it. bytes.Buffer is not safe for that,
// and the defect is in the FIXTURE: the product is doing exactly what it
// should.
//
// POLLING DOES NOT FIX IT, and that is the shape worth remembering. The author
// of the racing cell SAW the hazard and wrote it down -- "the log lands on a
// goroutine and a bare read races it" -- then answered it by polling until the
// text appeared. Polling fixes the TIMING (reading too early) and leaves the
// MEMORY MODEL untouched; it converts one unsynchronised read into many.
// Timing mitigations answer "will I see it yet"; synchronisation answers "is
// this defined at all". A noticed hazard answered with the wrong KIND of fix
// is more dangerous than an unnoticed one, because the comment makes the cell
// look considered.
//
// Guarding here fixes the class: no future test that captures logs from a
// goroutine can race on this buffer, whether or not its author thinks about it.
type safeLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// String reads under the SAME lock the writer takes. Verified load-bearing:
// with this lock removed, -race reports 4 warnings on
// TestPaste_ARejectedPasteIsNotSilent -- the exact count shipper measured at
// the v2.11.5 cut.
func (s *safeLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureLogs installs a logger at DEBUG level (so nothing is filtered out by
// the capture itself) writing to a concurrency-safe buffer, and restores the
// previous logger.
func captureLogs(t *testing.T) *safeLogBuffer {
	t.Helper()
	buf := &safeLogBuffer{}
	appLogger.mu.Lock()
	prev := appLogger.logger
	appLogger.logger = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	appLogger.mu.Unlock()
	t.Cleanup(func() {
		appLogger.mu.Lock()
		appLogger.logger = prev
		appLogger.mu.Unlock()
	})
	return buf
}

// TestWithholdSubmit_LogsAtInfoSoTheHeldMessageSurvivesTheDefaultLevel pins the
// rule ini-9gvn bought with a live incident: a message the fleet is holding is
// not debug detail. The 9gvn incident left zero trace in a 7.8MB fleet log
// because its only record was below the default level, and this path holds a
// message in a worse way -- the body is already in the composer and nothing
// re-delivers it.
func TestWithholdSubmit_LogsAtInfoSoTheHeldMessageSurvivesTheDefaultLevel(t *testing.T) {
	buf := captureLogs(t)
	pane := withholdTestPane("eng1")

	withholdSubmit(pane, "a message the fleet is holding", "bracketed")

	// Scope the assertion to the WITHHELD record. The buffer also holds the
	// event-emit line, which is legitimately DEBUG -- asserting over the whole
	// buffer tests the wrong record and fails for the wrong reason.
	line := recordContaining(t, buf.String(), "submit WITHHELD")
	if strings.Contains(line, "level=DEBUG") {
		t.Errorf("withheld submit logged at DEBUG: it disappears at the default level, which is "+
			"exactly how the 9gvn incident left no trace in a 7.8MB log. Got: %q", line)
	}
	if !strings.Contains(line, "level=INFO") {
		t.Errorf("withheld submit is not at INFO. Got: %q", line)
	}
}

// recordContaining returns the single log line holding marker, failing if there
// is not exactly one -- so the assertion cannot silently read a neighbour.
func recordContaining(t *testing.T, out, marker string) string {
	t.Helper()
	var hits []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, marker) {
			hits = append(hits, l)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly 1 record containing %q, got %d. Full log:\n%s", marker, len(hits), out)
	}
	return hits[0]
}

// The preview must survive too -- a record naming no message is a record you
// cannot act on.
func TestWithholdSubmit_NamesTheMessageItIsHolding(t *testing.T) {
	buf := captureLogs(t)
	withholdSubmit(withholdTestPane("eng1"), "deploy the thing", "bracketed")
	if out := buf.String(); !strings.Contains(out, "eng1") {
		t.Errorf("withheld-submit record does not name the pane: %q", out)
	}
}
