// control_mux.go multiplexes a single yamux control stream so multiple
// RemotePanes can send concurrent requests without interleaving responses.
// A single reader goroutine routes responses by ID to waiting callers, and
// routes unsolicited events to a broadcast channel.
package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ControlMux multiplexes a yamux control stream for concurrent request/response
// use by multiple RemotePanes. Each Request call gets a unique ID, writes the
// command, and waits for the response with that ID. A single readLoop goroutine
// reads all messages and dispatches by ID.
type ControlMux struct {
	conn    net.Conn
	writeMu sync.Mutex // Serializes writes to the control stream.
	nextID  atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan ControlResp // Request ID -> response channel.

	events chan ControlResp // Unsolicited server-pushed messages (no ID).
	done   chan struct{}    // Closed when readLoop exits.
}

// NewControlMux creates a multiplexer for the given control stream connection
// and starts the background reader goroutine.
func NewControlMux(conn net.Conn) *ControlMux {
	m := &ControlMux{
		conn:    conn,
		pending: make(map[string]chan ControlResp),
		events:  make(chan ControlResp, 32),
		done:    make(chan struct{}),
	}
	go m.readLoop()
	return m
}

// Request sends a command on the control stream and waits for the correlated
// response (matched by ID). Returns an error if the stream is closed or the
// request times out after 10 seconds.
func (m *ControlMux) Request(cmd ControlCmd) (ControlResp, error) {
	id := fmt.Sprintf("r%d", m.nextID.Add(1))
	cmd.ID = id

	ch := make(chan ControlResp, 1)
	m.pendingMu.Lock()
	m.pending[id] = ch
	m.pendingMu.Unlock()

	defer func() {
		m.pendingMu.Lock()
		delete(m.pending, id)
		m.pendingMu.Unlock()
	}()

	// Write the command.
	data, _ := json.Marshal(cmd)
	m.writeMu.Lock()
	m.conn.SetWriteDeadline(time.Now().Add(networkWriteTimeout))
	_, err := m.conn.Write(data)
	if err == nil {
		_, err = m.conn.Write([]byte("\n"))
	}
	m.writeMu.Unlock()
	if err != nil {
		return ControlResp{}, fmt.Errorf("write: %w", err)
	}

	// Wait for the response.
	select {
	case resp := <-ch:
		return resp, nil
	case <-m.done:
		return ControlResp{}, fmt.Errorf("control stream closed")
	case <-time.After(10 * time.Second):
		return ControlResp{}, fmt.Errorf("request timeout")
	}
}

// Events returns a channel that receives unsolicited server-pushed messages
// (responses with no ID, such as agent_died or timer_fired events).
func (m *ControlMux) Events() <-chan ControlResp {
	return m.events
}

// Done returns a channel that is closed when the read loop exits (stream died).
func (m *ControlMux) Done() <-chan struct{} {
	return m.done
}

// Close shuts down the multiplexer by closing the underlying connection.
func (m *ControlMux) Close() {
	m.conn.Close()
}

// readLoop reads JSON lines from the control stream and routes them to
// waiting callers (by ID) or to the events channel (no ID).
func (m *ControlMux) readLoop() {
	defer close(m.done)
	scanner := bufio.NewScanner(m.conn)
	for scanner.Scan() {
		var resp ControlResp
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue // Malformed line, skip.
		}

		if resp.ID != "" {
			// Correlated response: route to the waiting caller.
			m.pendingMu.Lock()
			ch, ok := m.pending[resp.ID]
			m.pendingMu.Unlock()
			if ok {
				ch <- resp
			}
		} else {
			// Unsolicited event: send to the events channel (non-blocking).
			select {
			case m.events <- resp:
			default:
				// Events channel full; drop oldest.
				select {
				case <-m.events:
				default:
				}
				m.events <- resp
			}
		}
	}
}
