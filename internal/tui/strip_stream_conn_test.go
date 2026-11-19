//go:build !windows

package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// stripReadBound ends a read that waits for a message which never comes, so a
// broken stream fails this test instead of hanging the package. It is a hang
// guard, not a timing: every read returns as soon as its message arrives.
const stripReadBound = 30 * time.Second

// stripDial is the real window handshake (yamux client, control stream,
// hello), keeping the one scanner that read hello_ok for every later read. A
// scanner reads ahead: a second scanner on the same stream loses whatever the
// first had buffered, which is how a strip message went missing when each
// read made its own (measured: 4 failures in 50 runs, eng8 saw 3 in 10).
func stripDial(t *testing.T, addr, peerName string) (*yamux.Session, net.Conn, *bufio.Scanner) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	ctrl, err := session.Open()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}
	if err := writeJSON(ctrl, HelloMsg{Action: "hello", Version: 1, Token: testWindowToken, PeerName: peerName}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	sc := NewIPCScanner(ctrl)
	var ok HelloOKMsg
	if !stripScan(ctrl, sc) || json.Unmarshal(sc.Bytes(), &ok) != nil || ok.Action != "hello_ok" {
		t.Fatalf("no hello_ok: %v", sc.Err())
	}
	return session, ctrl, sc
}

// stripScan reads one message, bounded by the hang guard.
func stripScan(ctrl net.Conn, sc *bufio.Scanner) bool {
	ctrl.SetReadDeadline(time.Now().Add(stripReadBound))
	defer ctrl.SetReadDeadline(time.Time{})
	return sc.Scan()
}

// readStripMsg reads control messages, from the connection's one scanner,
// until a strip message for agent.
func readStripMsg(t *testing.T, ctrl net.Conn, sc *bufio.Scanner, agent string) stripMsg {
	t.Helper()
	for stripScan(ctrl, sc) {
		var m stripMsg
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Action == stripAction && m.Name == agent {
			return m
		}
	}
	t.Fatalf("no strip message for %s: %v", agent, sc.Err())
	return stripMsg{}
}

// D8: a window that connects after the list was set gets it at connect, and
// gets every change after.
func TestStripStream_SentAtConnectAndOnEveryChange(t *testing.T) {
	ws, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("eng1"), windowServerTestPane("eng2")})
	book := stripBookAt(t)
	if err := book.set("eng1", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	ws.daemon.attachStrips(book)

	session, ctrl, sc := stripDial(t, addr, "window2")
	defer session.Close()
	defer ctrl.Close()
	if m := readStripMsg(t, ctrl, sc, "eng1"); !reflect.DeepEqual(m.Items, []string{"one", "two"}) {
		t.Fatalf("at connect: %q", m.Items)
	}
	if err := book.set("eng1", []string{"three"}); err != nil {
		t.Fatal(err)
	}
	if m := readStripMsg(t, ctrl, sc, "eng1"); !reflect.DeepEqual(m.Items, []string{"three"}) {
		t.Fatalf("on change: %q", m.Items)
	}
	if err := book.set("eng1", nil); err != nil {
		t.Fatal(err)
	}
	if m := readStripMsg(t, ctrl, sc, "eng1"); len(m.Items) != 0 {
		t.Fatalf("on clear: %q", m.Items)
	}
}
