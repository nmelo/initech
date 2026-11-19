package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/watch"
)

// Watches work in a release build (ini-kbpo): with delivery in place the
// release gate (ini-n0to) is gone, so a release build sets, counts and
// writes watches, and opens the modal, as a dev build does.

func releaseWatchTUI(t *testing.T, version string) (*TUI, string) {
	t.Helper()
	tu := watchTUI(t)
	path := filepath.Join(t.TempDir(), "watches.yaml")
	st, _ := watch.Open(path, opsT0)
	tu.watches, tu.version = st, version
	return tu, path
}

func TestWatches_WorkInAReleaseBuild(t *testing.T) {
	for _, v := range []string{"2.69.1", "v2.70.0", "dev"} {
		tu, path := releaseWatchTUI(t, v)
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

func TestWatches_TheModalOpensInAReleaseBuild(t *testing.T) {
	for _, open := range []string{"Alt+t", "the command bar"} {
		tu := wmStoreTUI(t)
		tu.version = "v2.69.1"
		if open == "Alt+t" {
			tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModAlt))
		} else {
			tu.execCmd("watches")
		}
		if !tu.watchesM.active || tu.cmd.error != "" {
			t.Errorf("%s in a release build: modal %v, line %q", open, tu.watchesM.active, tu.cmd.error)
		}
	}
}
