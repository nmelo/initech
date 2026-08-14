package tui

// The handshake preamble is matched by ACTION, never by position (ini-x5ob).
//
// This is the regression guard for the defect that made a secondary window
// render an empty monitor while holding a correct ownership map: an unrelated
// control frame overtook stream_map, the client unmarshalled it as one anyway
// (JSON ignores unknown fields, so it "succeeded" with a nil Streams map), and
// the client then built zero panes without an error. Nothing retried, so the
// window stayed blank for the life of the connection.

import (
	"encoding/json"
	"strings"
	"testing"
)

// framesReader turns control frames into the newline-delimited stream the
// client reads.
func framesReader(t *testing.T, frames ...any) *strings.Reader {
	t.Helper()
	var b strings.Builder
	for _, f := range frames {
		data, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return strings.NewReader(b.String())
}

// TestReadStreamMap_OwnershipFrameDoesNotStealTheStreamMap is the bug, exactly.
//
// Window 1 broadcasts ownership when a window attaches -- which is the gap
// between hello_ok and stream_map -- so this interleaving is routine, not
// exotic. Measured at 4 failures in 6 composed runs before the fix.
func TestReadStreamMap_OwnershipFrameDoesNotStealTheStreamMap(t *testing.T) {
	r := framesReader(t,
		ControlResp{Action: paneOwnershipAction, Owner: map[string]string{
			"eng1": "window-2", "eng2": "window-2", "super": WindowOne,
		}},
		StreamMapMsg{Action: "stream_map", Streams: map[uint32]string{
			1: "eng1", 3: "eng2", 5: "super",
		}},
	)
	hello := HelloOKMsg{Owner: map[string]string{"eng1": WindowOne}}

	got, err := readStreamMap(NewIPCScanner(r), "window1", &hello)
	if err != nil {
		t.Fatalf("readStreamMap: %v", err)
	}
	if len(got.Streams) != 3 {
		t.Fatalf("stream map has %d entries, want 3.\n\n"+
			"An empty or short stream map is not an error the client notices: it simply "+
			"creates that many panes. Zero streams means a viewer that renders nothing, "+
			"holds a correct ownership map, and never retries.", len(got.Streams))
	}
	for id, name := range map[uint32]string{1: "eng1", 3: "eng2", 5: "super"} {
		if got.Streams[id] != name {
			t.Errorf("stream %d maps to %q, want %q", id, got.Streams[id], name)
		}
	}

	// The overtaking frame is NEWER than the one hello_ok carried, so it must
	// replace it rather than be discarded.
	if hello.Owner["eng1"] != "window-2" {
		t.Errorf("the ownership frame read before stream_map was dropped: eng1 owner is %q, "+
			"want window-2. Discarding it would leave the viewer acting on the map from "+
			"the handshake, which is older.", hello.Owner["eng1"])
	}
}

// TestReadStreamMap_PlainOrderStillWorks is the positive control: the ordinary
// case must not have been broken by making the read tolerant.
func TestReadStreamMap_PlainOrderStillWorks(t *testing.T) {
	r := framesReader(t, StreamMapMsg{Action: "stream_map", Streams: map[uint32]string{1: "eng1"}})
	hello := HelloOKMsg{}
	got, err := readStreamMap(NewIPCScanner(r), "window1", &hello)
	if err != nil {
		t.Fatalf("readStreamMap: %v", err)
	}
	if len(got.Streams) != 1 || got.Streams[1] != "eng1" {
		t.Fatalf("stream map = %+v, want one entry mapping 1 -> eng1", got.Streams)
	}
}

// TestReadStreamMap_UnrelatedFramesAreSkipped covers the general case: any
// broadcast can land here, not only ownership.
func TestReadStreamMap_UnrelatedFramesAreSkipped(t *testing.T) {
	r := framesReader(t,
		ControlResp{Action: sessionNoticeAction, Text: "window window-2 reattached"},
		ControlResp{Action: "agent_status"},
		StreamMapMsg{Action: "stream_map", Streams: map[uint32]string{7: "qa1"}},
	)
	hello := HelloOKMsg{}
	got, err := readStreamMap(NewIPCScanner(r), "window1", &hello)
	if err != nil {
		t.Fatalf("readStreamMap: %v", err)
	}
	if got.Streams[7] != "qa1" {
		t.Fatalf("stream map = %+v, want 7 -> qa1 after skipping two unrelated frames", got.Streams)
	}
}

// TestReadStreamMap_GivesUpRatherThanHanging pins the bound. A peer that never
// sends a stream map must produce an error, not an infinite read.
func TestReadStreamMap_GivesUpRatherThanHanging(t *testing.T) {
	var frames []any
	for i := 0; i < 100; i++ {
		frames = append(frames, ControlResp{Action: sessionNoticeAction, Text: "noise"})
	}
	hello := HelloOKMsg{}
	_, err := readStreamMap(NewIPCScanner(framesReader(t, frames...)), "window1", &hello)
	if err == nil {
		t.Fatal("a peer that never sends stream_map produced no error; the client would " +
			"consume frames forever instead of reporting a broken handshake")
	}
	if !strings.Contains(err.Error(), "stream_map") {
		t.Errorf("error %q does not name what was missing", err)
	}
}

// TestReadStreamMap_EmptyStreamIsAnError covers the disconnect case, so a
// closed connection is reported rather than silently yielding no panes.
func TestReadStreamMap_EmptyStreamIsAnError(t *testing.T) {
	hello := HelloOKMsg{}
	_, err := readStreamMap(NewIPCScanner(strings.NewReader("")), "window1", &hello)
	if err == nil {
		t.Fatal("a connection that closed before stream_map produced no error")
	}
}
