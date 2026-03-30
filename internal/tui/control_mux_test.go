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
