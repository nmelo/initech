package tui

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// watchesFile is window 1's watches file under .initech.
const watchesFile = "watches.yaml"

// loadWatches opens window 1's watch store at start, before the first bead
// poll. A file it had to set aside is logged and told to super in one line.
// Only window 1 calls it: a second window and the serve daemon keep no
// watches. Main goroutine.
func (t *TUI) loadWatches(root string, now time.Time) {
	s, note := watch.Open(filepath.Join(root, ".initech", watchesFile), now)
	t.watches = s
	// Watches 2C (ini-kbpo): receipts tell delivery what became of a line;
	// held mail is back already, so the files say where each line is.
	onReceipt = t.watchReceiptChanged
	t.resolveHandedAtStart(now)
	t.deliverWatches(now) // also publishes the receipts to keep (ini-yjcy)
	// A rig without bd never polls: the deadline timer is armed here (ini-20r2).
	t.armWatchTimer(now)
	if note != "" {
		LogWarn("watches", "watches file set aside", "note", note)
		t.sendInitechLine("watches: " + note)
	}
}

// watchesTick runs after every bead poll on the main goroutine: history over
// an hour old goes, and a write that failed is tried again. A run of failing
// writes is logged once and told to super once, after
// watch.WriteTroubleTellSuper. Judging conditions is not this; it ends no
// watch.
func (t *TUI) watchesTick(now time.Time) {
	if t.watches == nil {
		return
	}
	t.watches.Prune(now)
	tr := t.watches.Pass(now)
	if tr.Log {
		LogWarn("watches", "watches file cannot be written; endings wait until it can", "file", tr.File, "err", tr.Err)
	}
	if tr.TellSuper {
		t.sendInitechLine(fmt.Sprintf("watches: %s could not be written for %s (%v); watch endings are held until it can",
			filepath.Base(tr.File), watch.WriteTroubleTellSuper, tr.Err))
	}
}
