package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/tui"
)

// ini-pt3j: --until-off, no cap on --for, the older-initech line, and the
// status and patrol wording of how a hold ends.

func TestDNDHoldCmd_UntilOffTravelsOnlyWhenSet(t *testing.T) {
	_, _, err, got := x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until off: messages wait"}, "dnd", "on", "--until-off")
	if err != nil || len(got) != 1 || !got[0].UntilOff || got[0].ForSec != 0 {
		t.Fatalf("err %v, requests %+v, want one with until_off and no timer", err, got)
	}
	_, _, _, got = x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2: x"}, "dnd", "on")
	if len(got) != 1 || got[0].UntilOff {
		t.Fatalf("a plain on sent %+v, want no until_off", got)
	}
	b, _ := json.Marshal(tui.IPCRequest{Action: "dnd", Text: "on"})
	if strings.Contains(string(b), "until_off") {
		t.Fatalf("until_off false is on the wire: %s", b)
	}
}

func TestDNDHoldCmd_ForAndUntilOffTogetherAreAnError(t *testing.T) {
	_, _, err, got := x432cRun(t, tui.IPCResponse{OK: true}, "dnd", "on", "--for", "2h", "--until-off")
	if err == nil || !strings.Contains(err.Error(), "--for and --until-off") || len(got) != 0 {
		t.Fatalf("err %v, requests %+v, want an error naming both and nothing sent", err, got)
	}
	_, _, err, _ = x432cRun(t, tui.IPCResponse{OK: true}, "dnd", "off", "--until-off")
	if err == nil {
		t.Fatal("--until-off with off was accepted")
	}
}

// --for of any length goes as asked, in seconds; no cap on the way.
func TestDNDHoldCmd_ALongForGoesAsAsked(t *testing.T) {
	_, stderr, err, got := x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until 18:30:00 (3h): x"}, "dnd", "on", "--for", "3h")
	if err != nil || len(got) != 1 || got[0].ForSec != 3*3600 || stderr != "" {
		t.Fatalf("err %v, stderr %q, requests %+v, want 10800s sent and nothing more said", err, stderr, got)
	}
}

// AC4: an older running initech, said in one line with when it really ends.
func TestDNDHoldCmd_AnOlderInitechIsSaidWithItsRealEnd(t *testing.T) {
	cases := []struct {
		name string
		resp tui.IPCResponse
		args []string
		want string
	}{
		{"until-off unknown, turn end", tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2: messages wait until your turn ends or initech dnd off"},
			[]string{"dnd", "on", "--until-off"}, "the running initech does not know --until-off; do-not-disturb ends at turn end"},
		{"until-off unknown, a timer runs", tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until 15:00:00 (20m): x"},
			[]string{"dnd", "on", "--until-off"}, "the running initech does not know --until-off; do-not-disturb ends at 15:00:00"},
		{"--for capped", tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until 15:00:00 (1h): x", Notices: []string{"do-not-disturb capped at 1h"}},
			[]string{"dnd", "on", "--for", "3h"}, "the running initech caps --for; do-not-disturb ends at 15:00:00"},
		{"--for unknown", tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2: x"},
			[]string{"dnd", "on", "--for", "3h"}, "the running initech does not know --for; do-not-disturb ends at turn end"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, stderr, err, _ := x432cRun(t, c.resp, c.args...)
			if err != nil || strings.Count(stderr, c.want) != 1 {
				t.Fatalf("err %v, stderr %q, want exactly one %q", err, stderr, c.want)
			}
		})
	}
	// The positive twin: a current initech's reply prints no such line.
	_, stderr, _, _ := x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until off: x"}, "dnd", "on", "--until-off")
	if strings.Contains(stderr, "running initech") {
		t.Fatalf("a current reply printed %q", stderr)
	}
}

// 6a: status and patrol read how the hold ends; anything else prints today's line.
func TestDNDHoldCmd_StatusNamesHowTheHoldEnds(t *testing.T) {
	at := time.Date(2026, 11, 5, 15, 30, 0, 0, time.Local)
	for until, want := range map[string]string{
		"":                      "dnd 5m",
		"off":                   "dnd 5m until off",
		at.Format(time.RFC3339): "dnd 5m until 15:30",
		"garbage":               "dnd 5m",
	} {
		if got := dndText(300, until); got != want {
			t.Errorf("dndText(300, %q) = %q, want %q", until, got, want)
		}
	}
}

// 6a: dnd_until never reaches status --json.
func TestDNDHoldCmd_DndUntilIsNotInStatusJSON(t *testing.T) {
	b, _ := json.Marshal(agentJSON{})
	if strings.Contains(string(b), "dnd_until") {
		t.Fatalf("status --json carries dnd_until: %s", b)
	}
}
