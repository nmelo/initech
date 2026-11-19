package watch

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The kinds and states are the stored format's words: renaming one strands
// every watch already in a rig's file. Pinned by value.
func TestKindsAndStates_AreTheStoredWords(t *testing.T) {
	for got, want := range map[Kind]string{KindBeadStatus: "bead_status", KindBeadComment: "bead_comment",
		KindDeadline: "deadline", KindSendDelivered: "send_delivered", KindSendHeld: "send_held"} {
		if string(got) != want {
			t.Errorf("kind %q, want %q", got, want)
		}
	}
	for got, want := range map[State]string{Waiting: "waiting", Fired: "fired", Missed: "missed", Cancelled: "cancelled"} {
		if string(got) != want {
			t.Errorf("state %q, want %q", got, want)
		}
	}
}

// The bounds the guide names, pinned by value.
func TestBounds_AreTheNamedValues(t *testing.T) {
	if MaxWaitingPerOwner != 20 || MaxWhy != 200 || History != time.Hour ||
		HeldTellSuper != 30*time.Minute || WriteTroubleTellSuper != 5*time.Minute {
		t.Errorf("bounds %d %d %v %v %v", MaxWaitingPerOwner, MaxWhy, History, HeldTellSuper, WriteTroubleTellSuper)
	}
}

// A waiting watch writes no ended, delivery or late fields; an ended one
// round-trips every field.
func TestWatch_RoundTripsThroughYAML(t *testing.T) {
	at := time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC)
	waiting := Watch{ID: "w1", Owner: "eng8", Kind: KindBeadStatus, Params: map[string]string{"bead": "ini-x", "status": "ready_for_qa"},
		Why: "read it", Deadline: at.Add(time.Hour), Set: at, State: Waiting}
	b, err := yaml.Marshal(waiting)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"ended", "delivery", "late", "detail", "hold_if_asleep"} {
		if yamlHasKey(b, absent) {
			t.Errorf("a waiting watch wrote %q:\n%s", absent, b)
		}
	}
	ended := waiting
	ended.State, ended.Ended, ended.Detail, ended.Late = Fired, at.Add(time.Minute), "ini-x reached ready_for_qa", true
	ended.Delivery = Delivery{Handed: at.Add(2 * time.Minute), Receipt: "r9", Delivered: at.Add(3 * time.Minute)}
	b, err = yaml.Marshal(ended)
	if err != nil {
		t.Fatal(err)
	}
	var back Watch
	if err := yaml.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.State != Fired || !back.Ended.Equal(ended.Ended) || !back.Late || back.Delivery.Receipt != "r9" ||
		!back.Delivery.Delivered.Equal(ended.Delivery.Delivered) || back.Params["status"] != "ready_for_qa" {
		t.Errorf("round trip lost a field: %+v", back)
	}
}

func yamlHasKey(b []byte, key string) bool {
	var m map[string]any
	_ = yaml.Unmarshal(b, &m)
	_, ok := m[key]
	return ok
}
