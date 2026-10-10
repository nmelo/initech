package tui

import (
	"strings"
	"testing"
)

func TestPerfMarkerToken(t *testing.T) {
	for text, want := range map[string]string{
		"echo IQPERF-42":         "IQPERF-42",
		"a IQPERF-7 b IQPERF-8":  "IQPERF-7",
		"no marker here":         "",
		"IQPERF- without digits": "",
	} {
		if got := perfMarkerToken(text); got != want {
			t.Errorf("perfMarkerToken(%q) = %q, want %q", text, got, want)
		}
	}
}

func echoedLines(logs *safeLogBuffer) int {
	return strings.Count(logs.String(), "marker echoed")
}

func TestPerfMarker_EchoInOneReadLogsAndDisarms(t *testing.T) {
	logs := captureLogs(t)
	p := &Pane{name: "shell"}
	p.armPerfMarker("IQPERF-3")

	p.checkPerfMarker([]byte("$ echo IQPERF-3\r\n"))

	if echoedLines(logs) != 1 {
		t.Fatalf("echo not logged once:\n%s", logs.String())
	}
	if p.perfMarker.Load() != nil {
		t.Error("still armed after the echo; the next unrelated read would be scanned for nothing")
	}
	p.checkPerfMarker([]byte("IQPERF-3 again"))
	if echoedLines(logs) != 1 {
		t.Error("a disarmed pane logged a second echo")
	}
}

// A token split across two PTY reads is still seen -- echo of pasted text
// arrives in pieces.
func TestPerfMarker_TokenSplitAcrossReads(t *testing.T) {
	logs := captureLogs(t)
	p := &Pane{name: "shell"}
	p.armPerfMarker("IQPERF-123")

	p.checkPerfMarker([]byte("$ echo IQPE"))
	if echoedLines(logs) != 0 {
		t.Fatal("logged an echo from half a token")
	}
	p.checkPerfMarker([]byte("RF-123\r\n"))

	if echoedLines(logs) != 1 {
		t.Errorf("a token split across two reads was missed:\n%s", logs.String())
	}
}

func TestPerfMarker_UnrelatedOutputStaysArmedAndUnarmedIsInert(t *testing.T) {
	logs := captureLogs(t)
	p := &Pane{name: "shell"}
	p.checkPerfMarker([]byte("IQPERF-1")) // not armed
	if echoedLines(logs) != 0 {
		t.Fatal("an unarmed pane logged an echo")
	}
	p.armPerfMarker("IQPERF-9")
	for i := 0; i < 5; i++ {
		p.checkPerfMarker([]byte(strings.Repeat("noise ", 50)))
	}
	if p.perfMarker.Load() == nil || echoedLines(logs) != 0 {
		t.Error("unrelated output disarmed the pane or logged an echo")
	}
}

// The "written" stamp fires for the armed token only, so an unrelated send to
// the same pane cannot claim the marker's write.
func TestPerfMarker_WrittenStampOnlyForTheArmedToken(t *testing.T) {
	logs := captureLogs(t)
	p := &Pane{name: "shell"}
	notePerfMarkerWritten(p, "echo IQPERF-5") // not armed
	p.armPerfMarker("IQPERF-5")
	notePerfMarkerWritten(p, "echo something else")
	if strings.Contains(logs.String(), "marker written") {
		t.Fatalf("a written stamp without the armed token:\n%s", logs.String())
	}
	notePerfMarkerWritten(p, "echo IQPERF-5")
	if strings.Count(logs.String(), "marker written") != 1 {
		t.Errorf("the armed token's write was not stamped once:\n%s", logs.String())
	}
}
