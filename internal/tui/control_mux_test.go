package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// TestControlMux_ConcurrentRequestsGetCorrectResponses verifies that two
// concurrent Request calls each receive their own response (no interleaving).
func TestControlMux_ConcurrentRequestsGetCorrectResponses(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mux := NewControlMux(client)

	// Fake server: read two commands via scanner, respond in reverse order.
	go func() {
		scanner := bufio.NewScanner(server)
		var cmds []ControlCmd
		for scanner.Scan() {
			var cmd ControlCmd
			if json.Unmarshal(scanner.Bytes(), &cmd) == nil && cmd.ID != "" {
				cmds = append(cmds, cmd)
			}
			if len(cmds) >= 2 {
				break
			}
		}
		// Respond in reverse order to prove ID routing works.
		for i := len(cmds) - 1; i >= 0; i-- {
			resp, _ := json.Marshal(ControlResp{ID: cmds[i].ID, OK: true, Data: cmds[i].Target})
			server.Write(resp)
			server.Write([]byte("\n"))
		}
	}()

	var wg sync.WaitGroup
	results := make([]ControlResp, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], _ = mux.Request(ControlCmd{Action: "send", Target: "eng1"})
	}()
	go func() {
		defer wg.Done()
		results[1], _ = mux.Request(ControlCmd{Action: "send", Target: "eng2"})
	}()
	wg.Wait()

	// Each result should have the correct target in Data.
	targets := map[string]bool{results[0].Data: true, results[1].Data: true}
	if !targets["eng1"] || !targets["eng2"] {
		t.Errorf("responses mixed up: got Data=%q and %q, want eng1 and eng2", results[0].Data, results[1].Data)
	}
}

// TestControlMux_EventNotSwallowedBySendText verifies that an unsolicited
// server event pushed during a Request call is routed to the Events channel,
// not consumed by the Request caller. This is the core fix for ini-mwza.
func TestControlMux_EventNotSwallowedBySendText(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mux := NewControlMux(client)

	// Fake server: read command, push an unsolicited event, then respond.
	go func() {
		scanner := bufio.NewScanner(server)
		if !scanner.Scan() {
			return
		}
		var cmd ControlCmd
		json.Unmarshal(scanner.Bytes(), &cmd)

		// Push unsolicited event (no ID) BEFORE responding to the request.
		event, _ := json.Marshal(ControlResp{OK: true, Data: "agent_died:eng3"})
		server.Write(event)
		server.Write([]byte("\n"))

		// Now respond with the request ID.
		resp, _ := json.Marshal(ControlResp{ID: cmd.ID, OK: true, Data: "sent"})
		server.Write(resp)
		server.Write([]byte("\n"))
	}()

	// Fire SendText-equivalent request.
	resp, err := mux.Request(ControlCmd{Action: "send", Target: "eng1", Text: "hello"})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if resp.Data != "sent" {
		t.Errorf("Request got Data=%q, want 'sent'", resp.Data)
	}

	// The unsolicited event should be on the Events channel.
	select {
	case ev := <-mux.Events():
		if ev.Data != "agent_died:eng3" {
			t.Errorf("event Data=%q, want 'agent_died:eng3'", ev.Data)
		}
	case <-time.After(time.Second):
		t.Error("unsolicited event was swallowed by Request (not routed to Events)")
	}
}

// TestControlMux_CloseUnblocksRequest verifies that closing the mux unblocks
// a waiting Request call.
func TestControlMux_CloseUnblocksRequest(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	mux := NewControlMux(client)

	go func() {
		// Read the request so the write doesn't block.
		scanner := bufio.NewScanner(server)
		scanner.Scan()
		time.Sleep(50 * time.Millisecond)
		mux.Close()
	}()

	_, err := mux.Request(ControlCmd{Action: "peek", Target: "eng1"})
	if err == nil {
		t.Error("Request should return error after mux.Close()")
	}
}

// ini-5my1 regression tests. readLoop's pending-response branch used to do an
// unconditionally blocking `ch <- resp` outside pendingMu, on a channel of
// capacity 1. Request registers ch, THEN writes the command, and on a write
// error (e.g. the 3s networkWriteTimeout expiring) returns without ever
// reaching its own select — leaving a registered-but-abandoned channel. The
// cap-1 buffer absorbs one response; a SECOND response with the same ID (a
// malicious or buggy daemon can always produce one, since request IDs are
// predictable and monotonic) blocked readLoop forever. Because readLoop is
// the sole reader of the shared control stream, the wedge silently killed
// every RemotePane on that peer AND its own recovery path (the deferred
// close(m.done) never ran, so Done() never fired and reconnect never
// triggered).
//
// abandonedPendingEntry registers a pending channel with no receiver — the
// exact state Request's write-error path leaves behind — without going
// through Request itself (which would need a real write failure to trigger
// it).
func abandonedPendingEntry(mux *ControlMux, id string) chan ControlResp {
	ch := make(chan ControlResp, 1)
	mux.pendingMu.Lock()
	mux.pending[id] = ch
	mux.pendingMu.Unlock()
	return ch
}

func sendRawResponse(t *testing.T, w net.Conn, resp ControlResp) {
	t.Helper()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

// sendRawResponseAsync is sendRawResponse without the *testing.T dependency,
// for use inside a background goroutine that can legitimately outlive the
// test itself (e.g. a write that blocks forever on a wedged readLoop, which
// is exactly the failure mode under test). Calling t.Fatalf from such a
// goroutine after the test has already failed/returned panics the whole
// binary ("Fail in goroutine after Test has completed"), so this helper
// silently drops write errors -- correctness is asserted on the reader side
// in the test body, not here.
func sendRawResponseAsync(w net.Conn, resp ControlResp) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

// TestControlMux_SingleResponseToAbandonedEntry_KeepsReadLoopAlive is the
// CONTROL: exactly one response for an abandoned entry must not wedge
// readLoop, because the cap-1 buffer absorbs it. Isolates the duplicate ID
// (not the mere existence of an abandoned entry) as the trigger below.
func TestControlMux_SingleResponseToAbandonedEntry_KeepsReadLoopAlive(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mux := NewControlMux(client)
	abandonedPendingEntry(mux, "r1")

	go func() {
		sendRawResponseAsync(server, ControlResp{ID: "r1", OK: true, Data: "only"})
		sendRawResponseAsync(server, ControlResp{Data: "event-after-single"})
	}()

	select {
	case ev := <-mux.Events():
		if ev.Data != "event-after-single" {
			t.Errorf("event Data=%q, want %q", ev.Data, "event-after-single")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop wedged after a single response to an abandoned entry")
	}
}

// TestControlMux_DuplicateResponseID_DoesNotWedgeReadLoop is the bug: two
// responses carrying the SAME id as an abandoned pending entry must not
// permanently block readLoop. Before the fix, this hung until the test
// itself timed out; after the fix, the duplicate is dropped and readLoop
// keeps routing.
func TestControlMux_DuplicateResponseID_DoesNotWedgeReadLoop(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mux := NewControlMux(client)
	abandonedPendingEntry(mux, "r1")

	go func() {
		sendRawResponseAsync(server, ControlResp{ID: "r1", OK: true, Data: "first"})
		sendRawResponseAsync(server, ControlResp{ID: "r1", OK: true, Data: "second"})
		sendRawResponseAsync(server, ControlResp{Data: "event-after-duplicate"})
	}()

	// readLoop must still be alive to deliver this: it proves the second
	// same-ID response did not block forever on the abandoned channel.
	select {
	case ev := <-mux.Events():
		if ev.Data != "event-after-duplicate" {
			t.Errorf("event Data=%q, want %q", ev.Data, "event-after-duplicate")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WEDGE: readLoop did not deliver the post-duplicate event; " +
			"the second same-ID response blocked it forever")
	}

	// A fresh Request must complete via its normal response, not fall through
	// to its own 10s timeout. Read the incoming command and answer it with a
	// matching ID -- if routing is still broken (e.g. the fresh request's
	// entry also gets abandoned/blocked), this response is never delivered
	// and the assertion below times out well before Request's internal 10s
	// deadline would.
	go func() {
		scanner := bufio.NewScanner(server)
		if !scanner.Scan() {
			return
		}
		var cmd ControlCmd
		if json.Unmarshal(scanner.Bytes(), &cmd) != nil || cmd.ID == "" {
			return
		}
		sendRawResponseAsync(server, ControlResp{ID: cmd.ID, OK: true, Data: "pong"})
	}()
	done := make(chan struct{})
	go func() {
		_, _ = mux.Request(ControlCmd{Action: "peek", Target: "eng1"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WEDGE: a fresh Request after the duplicate did not complete within 3s")
	}
}

// TestControlMux_DuplicateResponseID_DoneStillClosesOnStreamEnd verifies the
// wedge's worst consequence directly: readLoop's defer close(m.done) must
// still run (so Done()-keyed reconnect can still fire) even after the peer
// has sent a duplicate response for an abandoned entry.
func TestControlMux_DuplicateResponseID_DoneStillClosesOnStreamEnd(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	mux := NewControlMux(client)
	abandonedPendingEntry(mux, "r1")

	sendRawResponse(t, server, ControlResp{ID: "r1", OK: true, Data: "first"})
	sendRawResponse(t, server, ControlResp{ID: "r1", OK: true, Data: "second"})
	server.Close() // Simulates the peer dying: readLoop's Scan should end.

	select {
	case <-mux.Done():
		// readLoop exited and closed done, as it must for reconnect to fire.
	case <-time.After(2 * time.Second):
		t.Fatal("WEDGE: Done() never closed after stream end; readLoop is " +
			"stuck on the duplicate response and reconnect can never trigger")
	}
}
