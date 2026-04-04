package tui

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDispatchIPC_SendClearsReadDeadlineAndDelegates(t *testing.T) {
	conn := &dispatchConn{}
	host := &dispatchHost{}
	req := IPCRequest{Action: "send", Target: "eng1", Text: "hello", Enter: true}

	dispatchIPC(host, conn, req, nil)

	if host.sendReq != req {
		t.Fatalf("send req = %#v, want %#v", host.sendReq, req)
	}
	if host.sendConn != conn {
		t.Fatal("HandleSend should receive the original connection")
	}
	if !host.sendCalled {
		t.Fatal("HandleSend should be called")
	}
	if !conn.readDeadline.Equal(time.Time{}) {
		t.Fatalf("read deadline = %v, want zero time", conn.readDeadline)
	}
}

func TestDispatchIPC_PeekErrors(t *testing.T) {
	tests := []struct {
		name    string
		req     IPCRequest
		host    *dispatchHost
		wantErr string
	}{
		{
			name:    "missing target",
			req:     IPCRequest{Action: "peek"},
			host:    &dispatchHost{},
			wantErr: "target is required",
		},
		{
			name:    "shutting down",
			req:     IPCRequest{Action: "peek", Target: "eng1"},
			host:    &dispatchHost{findOK: false},
			wantErr: "shutting down",
		},
		{
			name:    "pane not found",
			req:     IPCRequest{Action: "peek", Target: "ghost"},
			host:    &dispatchHost{findOK: true},
			wantErr: `pane "ghost" not found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &dispatchConn{}
			dispatchIPC(tt.host, conn, tt.req, nil)
			resp := decodeDispatchResponse(t, conn)
			if resp.OK {
				t.Fatal("peek response should fail")
			}
			if !strings.Contains(resp.Error, tt.wantErr) {
				t.Fatalf("error = %q, want %q", resp.Error, tt.wantErr)
			}
		})
	}
}

func TestDispatchIPC_PeekSuccess(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.emu.Write([]byte("hello world\r\nline two\r\n"))

	conn := &dispatchConn{}
	host := &dispatchHost{findPane: p, findOK: true}
	dispatchIPC(host, conn, IPCRequest{Action: "peek", Target: "eng1", Lines: 2}, nil)

	resp := decodeDispatchResponse(t, conn)
	if !resp.OK {
		t.Fatalf("peek response not OK: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "hello world") {
		t.Fatalf("peek data = %q, want emulator content", resp.Data)
	}
}

func TestDispatchIPC_ListResponses(t *testing.T) {
	t.Run("shutting down", func(t *testing.T) {
		conn := &dispatchConn{}
		host := &dispatchHost{allOK: false}

		dispatchIPC(host, conn, IPCRequest{Action: "list"}, nil)

		resp := decodeDispatchResponse(t, conn)
		if resp.OK {
			t.Fatal("list response should fail while shutting down")
		}
		if resp.Error != "shutting down" {
			t.Fatalf("error = %q, want shutting down", resp.Error)
		}
	})

	t.Run("success", func(t *testing.T) {
		conn := &dispatchConn{}
		host := &dispatchHost{
			allOK: true,
			panes: []PaneInfo{
				{Name: "eng1", Activity: "idle", Alive: true, Visible: true},
				{Name: "qa1", Host: "macbook", Activity: "running", Alive: true, Visible: false},
			},
		}

		dispatchIPC(host, conn, IPCRequest{Action: "list"}, nil)

		resp := decodeDispatchResponse(t, conn)
		if !resp.OK {
			t.Fatalf("list response not OK: %s", resp.Error)
		}
		var panes []PaneInfo
		if err := json.Unmarshal([]byte(resp.Data), &panes); err != nil {
			t.Fatalf("unmarshal list payload: %v", err)
		}
		if len(panes) != 2 {
			t.Fatalf("got %d panes, want 2", len(panes))
		}
		if panes[1].Host != "macbook" || panes[1].Name != "qa1" || panes[1].Visible {
			t.Fatalf("pane[1] = %+v, want remote hidden qa1", panes[1])
		}
	})
}

func TestDispatchIPC_ExtendedAndUnknown(t *testing.T) {
	t.Run("handled by host", func(t *testing.T) {
		conn := &dispatchConn{}
		host := &dispatchHost{extendedHandled: true}
		raw := []byte(`{"action":"custom","target":"eng1"}`)

		dispatchIPC(host, conn, IPCRequest{Action: "custom", Target: "eng1"}, raw)

		if !host.extendedCalled {
			t.Fatal("HandleExtended should be called")
		}
		if string(host.extendedRaw) != string(raw) {
			t.Fatalf("raw JSON = %q, want %q", string(host.extendedRaw), string(raw))
		}
		resp := decodeDispatchResponse(t, conn)
		if !resp.OK || resp.Data != "handled" {
			t.Fatalf("response = %+v, want handled OK", resp)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		conn := &dispatchConn{}
		host := &dispatchHost{}

		dispatchIPC(host, conn, IPCRequest{Action: "explode"}, nil)

		resp := decodeDispatchResponse(t, conn)
		if resp.OK {
			t.Fatal("unknown action should fail")
		}
		if !strings.Contains(resp.Error, `unknown action "explode"`) {
			t.Fatalf("error = %q, want unknown action", resp.Error)
		}
	})
}

func TestDispatchScheduleResponses(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		conn := &dispatchConn{}
		dispatchSchedule(&dispatchHost{}, conn, []byte("{not json"))
		resp := decodeDispatchResponse(t, conn)
		if resp.Error != "invalid schedule request" {
			t.Fatalf("error = %q, want invalid schedule request", resp.Error)
		}
	})

	t.Run("invalid fire_at", func(t *testing.T) {
		conn := &dispatchConn{}
		raw := []byte(`{"target":"eng1","text":"hello","fire_at":"tomorrow"}`)
		dispatchSchedule(&dispatchHost{}, conn, raw)
		resp := decodeDispatchResponse(t, conn)
		if !strings.Contains(resp.Error, "invalid fire_at:") {
			t.Fatalf("error = %q, want invalid fire_at", resp.Error)
		}
	})

	t.Run("missing timer store", func(t *testing.T) {
		conn := &dispatchConn{}
		fireAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		raw := []byte(`{"target":"eng1","text":"hello","fire_at":"` + fireAt + `"}`)
		dispatchSchedule(&dispatchHost{}, conn, raw)
		resp := decodeDispatchResponse(t, conn)
		if resp.Error != "timer store not initialized" {
			t.Fatalf("error = %q, want timer store not initialized", resp.Error)
		}
	})

	t.Run("add failure", func(t *testing.T) {
		conn := &dispatchConn{}
		ts := newBrokenTimerStore(t)
		host := &dispatchHost{timers: ts}
		fireAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		raw := []byte(`{"target":"eng1","text":"hello","fire_at":"` + fireAt + `"}`)

		dispatchSchedule(host, conn, raw)

		resp := decodeDispatchResponse(t, conn)
		if resp.OK {
			t.Fatal("schedule should fail when add persistence fails")
		}
		if !strings.Contains(resp.Error, "timer not persisted") {
			t.Fatalf("error = %q, want persistence failure", resp.Error)
		}
	})

	t.Run("success", func(t *testing.T) {
		conn := &dispatchConn{}
		ts := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"))
		host := &dispatchHost{timers: ts}
		fireAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
		raw := []byte(`{"target":"eng1","host":"macbook","text":"hello","enter":true,"fire_at":"` + fireAt.Format(time.RFC3339) + `"}`)

		dispatchSchedule(host, conn, raw)

		resp := decodeDispatchResponse(t, conn)
		if !resp.OK {
			t.Fatalf("schedule response not OK: %s", resp.Error)
		}
		if resp.Data != "at-1" {
			t.Fatalf("timer ID = %q, want at-1", resp.Data)
		}
		timers := ts.List()
		if len(timers) != 1 {
			t.Fatalf("stored timers = %d, want 1", len(timers))
		}
		if timers[0].Host != "macbook" || timers[0].Target != "eng1" || !timers[0].Enter {
			t.Fatalf("stored timer = %+v, want remote enter timer", timers[0])
		}
	})
}

func TestDispatchListTimersResponses(t *testing.T) {
	t.Run("nil store returns empty list", func(t *testing.T) {
		conn := &dispatchConn{}
		dispatchListTimers(&dispatchHost{}, conn)
		resp := decodeDispatchResponse(t, conn)
		if !resp.OK || resp.Data != "[]" {
			t.Fatalf("response = %+v, want OK empty list", resp)
		}
	})

	t.Run("returns sorted timers", func(t *testing.T) {
		conn := &dispatchConn{}
		ts := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"))
		later := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
		sooner := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
		if _, err := ts.Add("eng1", "", "later", false, later); err != nil {
			t.Fatalf("Add later: %v", err)
		}
		if _, err := ts.Add("eng2", "", "sooner", false, sooner); err != nil {
			t.Fatalf("Add sooner: %v", err)
		}

		dispatchListTimers(&dispatchHost{timers: ts}, conn)

		resp := decodeDispatchResponse(t, conn)
		if !resp.OK {
			t.Fatalf("list_timers response not OK: %s", resp.Error)
		}
		var timers []Timer
		if err := json.Unmarshal([]byte(resp.Data), &timers); err != nil {
			t.Fatalf("unmarshal timers: %v", err)
		}
		if len(timers) != 2 {
			t.Fatalf("got %d timers, want 2", len(timers))
		}
		if timers[0].Text != "sooner" || timers[1].Text != "later" {
			t.Fatalf("timers = %+v, want sorted by FireAt", timers)
		}
	})
}

func TestDispatchCancelTimerResponses(t *testing.T) {
	t.Run("missing timer store", func(t *testing.T) {
		conn := &dispatchConn{}
		dispatchCancelTimer(&dispatchHost{}, conn, IPCRequest{Text: "at-1"})
		resp := decodeDispatchResponse(t, conn)
		if resp.Error != "timer store not initialized" {
			t.Fatalf("error = %q, want timer store not initialized", resp.Error)
		}
	})

	t.Run("unknown timer", func(t *testing.T) {
		conn := &dispatchConn{}
		ts := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"))
		dispatchCancelTimer(&dispatchHost{timers: ts}, conn, IPCRequest{Text: "at-9"})
		resp := decodeDispatchResponse(t, conn)
		if !strings.Contains(resp.Error, `timer "at-9" not found`) {
			t.Fatalf("error = %q, want not found", resp.Error)
		}
	})

	t.Run("cancel failure", func(t *testing.T) {
		conn := &dispatchConn{}
		ts := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"))
		timer, err := ts.Add("eng1", "", "hello", false, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		blockingPath := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blockingPath, []byte("x"), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		ts.path = filepath.Join(blockingPath, "timers.json")

		dispatchCancelTimer(&dispatchHost{timers: ts}, conn, IPCRequest{Text: timer.ID})

		resp := decodeDispatchResponse(t, conn)
		if resp.OK {
			t.Fatal("cancel should fail when persistence fails")
		}
		if !strings.Contains(resp.Error, "cancel not persisted") {
			t.Fatalf("error = %q, want cancel persistence failure", resp.Error)
		}
		if ts.Pending() != 1 {
			t.Fatalf("pending timers = %d, want rollback to preserve timer", ts.Pending())
		}
	})

	t.Run("success", func(t *testing.T) {
		conn := &dispatchConn{}
		ts := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"))
		fireAt := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Minute)
		timer, err := ts.Add("eng1", "macbook", "hello", true, fireAt)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}

		dispatchCancelTimer(&dispatchHost{timers: ts}, conn, IPCRequest{Text: timer.ID})

		resp := decodeDispatchResponse(t, conn)
		if !resp.OK {
			t.Fatalf("cancel response not OK: %s", resp.Error)
		}
		want := timer.ID + " (macbook:eng1 at " + fireAt.Local().Format("15:04") + ")"
		if resp.Data != want {
			t.Fatalf("cancel data = %q, want %q", resp.Data, want)
		}
		if ts.Pending() != 0 {
			t.Fatalf("pending timers = %d, want 0", ts.Pending())
		}
	})
}

type dispatchHost struct {
	findPane        PaneView
	findOK          bool
	panes           []PaneInfo
	allOK           bool
	timers          *TimerStore
	webhookURL      string
	projectName     string
	sendCalled      bool
	sendConn        net.Conn
	sendReq         IPCRequest
	extendedHandled bool
	extendedCalled  bool
	extendedConn    net.Conn
	extendedReq     IPCRequest
	extendedRaw     []byte
}

func (h *dispatchHost) FindPaneView(name string) (PaneView, bool) {
	return h.findPane, h.findOK
}

func (h *dispatchHost) AllPanes() ([]PaneInfo, bool) {
	return h.panes, h.allOK
}

func (h *dispatchHost) HandleSend(conn net.Conn, req IPCRequest) {
	h.sendCalled = true
	h.sendConn = conn
	h.sendReq = req
}

func (h *dispatchHost) Timers() *TimerStore {
	return h.timers
}

func (h *dispatchHost) NotifyConfig() (string, string) {
	return h.webhookURL, h.projectName
}

func (h *dispatchHost) HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool {
	h.extendedCalled = true
	h.extendedConn = conn
	h.extendedReq = req
	h.extendedRaw = append([]byte(nil), rawJSON...)
	if h.extendedHandled {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: "handled"})
	}
	return h.extendedHandled
}

type dispatchConn struct {
	written      []byte
	readDeadline time.Time
}

func (c *dispatchConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *dispatchConn) Write(b []byte) (int, error) {
	c.written = append(c.written, b...)
	return len(b), nil
}
func (c *dispatchConn) Close() error                      { return nil }
func (c *dispatchConn) LocalAddr() net.Addr               { return nil }
func (c *dispatchConn) RemoteAddr() net.Addr              { return nil }
func (c *dispatchConn) SetDeadline(time.Time) error       { return nil }
func (c *dispatchConn) SetReadDeadline(t time.Time) error { c.readDeadline = t; return nil }
func (c *dispatchConn) SetWriteDeadline(time.Time) error  { return nil }

func decodeDispatchResponse(t *testing.T, conn *dispatchConn) IPCResponse {
	t.Helper()
	line := string(conn.written)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	var resp IPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		t.Fatalf("unmarshal IPC response: %v (raw %q)", err, line)
	}
	return resp
}

func newBrokenTimerStore(t *testing.T) *TimerStore {
	t.Helper()
	base := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(base, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return NewTimerStore(filepath.Join(base, "timers.json"))
}
