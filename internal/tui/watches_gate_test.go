package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/watch"
)

// The release gate (ini-n0to). 2C's delivery commit deletes these tests with
// the gate, and turns them into "works in a release build".

// gateTUI is a window 1 whose store file lives in a temp folder that holds
// nothing else, so a written watches.yaml shows.
func gateTUI(t *testing.T, version string) (*TUI, string) {
	t.Helper()
	tu := watchTUI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "watches.yaml")
	st, _ := watch.Open(path, opsT0)
	tu.watches, tu.version = st, version
	return tu, path
}

func TestWatchesGate_AReleaseBuildRefusesEveryWatchAction(t *testing.T) {
	tu, path := gateTUI(t, "2.69.1")
	for _, req := range []IPCRequest{
		setIPC("eng6", beadReq("ini-a", "read it")),
		{Action: "watch_list", From: "eng6", Text: `{}`},
		{Action: "watch_cancel", From: "eng6", Target: "w1"},
		{Action: "watch_count", Target: "eng6"},
	} {
		resp := watchIPC(t, tu, req)
		if resp.OK || resp.Error != watchesOffLine {
			t.Errorf("%s in a release build: %+v, want the refusal %q", req.Action, resp, watchesOffLine)
		}
	}
	if ws := tu.watches.Snapshot(opsT0); len(ws) != 0 {
		t.Errorf("a release build set a watch: %+v", ws)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a release build wrote %s (stat: %v)", filepath.Base(path), err)
	}
}

// The twin: a dev build, and an empty version (a dev build by IsReleaseVersion),
// set and list as built, and the file is written.
func TestWatchesGate_ADevBuildKeepsWatches(t *testing.T) {
	for _, v := range []string{"dev", "", "2.70.0-rc1"} {
		tu, path := gateTUI(t, v)
		if resp := watchIPC(t, tu, setIPC("eng6", beadReq("ini-a", "read it"))); !resp.OK {
			t.Errorf("version %q: set refused: %+v", v, resp)
		}
		if resp := watchIPC(t, tu, IPCRequest{Action: "watch_count", Target: "eng6"}); !resp.OK || resp.Data != "1" {
			t.Errorf("version %q: count %+v", v, resp)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("version %q: the store was not written: %v", v, err)
		}
	}
}

func TestWatchesGate_AReleaseBuildShowsTheLineInsteadOfTheModal(t *testing.T) {
	for _, open := range []string{"Alt+t", "the command bar"} {
		tu := wmStoreTUI(t)
		tu.version = "v2.69.1"
		if open == "Alt+t" {
			tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModAlt))
		} else {
			tu.execCmd("watches")
		}
		if tu.watchesM.active {
			t.Errorf("%s opened the modal in a release build", open)
		}
		if tu.cmd.error != watchesOffLine {
			t.Errorf("%s: command bar line %q, want %q", open, tu.cmd.error, watchesOffLine)
		}
	}
	tu := wmStoreTUI(t)
	tu.version = "dev"
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModAlt))
	if !tu.watchesM.active || tu.cmd.error != "" {
		t.Errorf("a dev build: modal %v, line %q", tu.watchesM.active, tu.cmd.error)
	}
}
