//go:build !windows

package tui

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"
)

// readStripMsg reads control messages until a strip message for agent, or
// fails when the bound passes with none (a bound on a read, not a sleep).
func readStripMsg(t *testing.T, ctrl net.Conn, agent string) stripMsg {
	t.Helper()
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer ctrl.SetReadDeadline(time.Time{})
	sc := NewIPCScanner(ctrl)
	for sc.Scan() {
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

	session, ctrl, _ := dialWindow(t, addr, "window2")
	defer session.Close()
	defer ctrl.Close()
	if m := readStripMsg(t, ctrl, "eng1"); !reflect.DeepEqual(m.Items, []string{"one", "two"}) {
		t.Fatalf("at connect: %q", m.Items)
	}
	if err := book.set("eng1", []string{"three"}); err != nil {
		t.Fatal(err)
	}
	if m := readStripMsg(t, ctrl, "eng1"); !reflect.DeepEqual(m.Items, []string{"three"}) {
		t.Fatalf("on change: %q", m.Items)
	}
	if err := book.set("eng1", nil); err != nil {
		t.Fatal(err)
	}
	if m := readStripMsg(t, ctrl, "eng1"); len(m.Items) != 0 {
		t.Fatalf("on clear: %q", m.Items)
	}
}
