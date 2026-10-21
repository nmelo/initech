package tui

import (
	"encoding/json"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// ini-l8s5: the list reply names the project behind the socket, from the TUI
// and from a serve daemon alike, so initech status run against another rig's
// socket can label the roster with that rig.
func TestListReply_NamesTheProjectBehindTheSocket(t *testing.T) {
	off := false
	tuiHost := newTestTUI(testPane("eng1"))
	tuiHost.project = &config.Project{Name: "other-rig"}
	daemonHost := &Daemon{project: &config.Project{Name: "served-rig", Beads: config.BeadsConfig{Enabled: &off}}}
	for name, tc := range map[string]struct {
		host IPCHost
		want IPCProject
	}{
		"tui":    {tuiHost, IPCProject{Name: "other-rig", BeadsEnabled: true}},
		"daemon": {daemonHost, IPCProject{Name: "served-rig", BeadsEnabled: false}},
	} {
		conn := &dispatchConn{}
		dispatchIPC(tc.host, conn, IPCRequest{Action: "list"}, nil)
		var resp IPCResponse
		if err := json.Unmarshal(conn.written, &resp); err != nil {
			t.Fatalf("%s: reply %q: %v", name, conn.written, err)
		}
		if !resp.OK || resp.Project == nil || *resp.Project != tc.want {
			t.Errorf("%s: list reply project %+v, want %+v", name, resp.Project, tc.want)
		}
	}
	// Feed it nothing: no project loaded, no field on the wire.
	conn := &dispatchConn{}
	dispatchIPC(newTestTUI(testPane("eng1")), conn, IPCRequest{Action: "list"}, nil)
	var raw map[string]any
	if err := json.Unmarshal(conn.written, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["project"]; ok {
		t.Errorf("a TUI with no project sent a project field: %s", conn.written)
	}
}
