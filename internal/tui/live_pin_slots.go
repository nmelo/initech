package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// pastGridNoticeFor is how long the bar shows the line naming pins that have
// no free live slot on the return to Live.
const pastGridNoticeFor = 5 * time.Second

// placePastGridPins runs once on a return to Live from a live split
// (ini-dc33). A pin made there may carry a slot number past the Live grid
// (agentsToggleLivePin). Each such pin, lowest number first, moves into the
// lowest free slot the grid has; the rest keep their number, so they stay
// pinned, are never placed over another pin, and Live places them when a grid
// large enough is chosen. The bar names those once, here, never per tick.
func (t *TUI) placePastGridPins() {
	if t.layoutState.Mode != LayoutLive {
		return
	}
	numSlots := t.layoutState.GridCols * t.layoutState.GridRows
	type pin struct {
		key  string
		slot int
	}
	occupied := make(map[int]bool, len(t.layoutState.LivePinned))
	var extra []pin
	for k, s := range t.layoutState.LivePinned {
		if s < numSlots {
			occupied[s] = true
		} else {
			extra = append(extra, pin{k, s})
		}
	}
	sort.Slice(extra, func(i, j int) bool {
		if extra[i].slot != extra[j].slot {
			return extra[i].slot < extra[j].slot
		}
		return extra[i].key < extra[j].key
	})
	var waiting []string
	free := 0
	for _, e := range extra {
		for free < numSlots && occupied[free] {
			free++
		}
		if free >= numSlots || t.setLiveSlot(e.key, free, true) != nil {
			waiting = append(waiting, e.key)
			continue
		}
		occupied[free] = true
	}
	if t.liveEngine != nil {
		t.liveEngine.Pinned = t.layoutState.LivePinned
	}
	if len(waiting) == 0 {
		return
	}
	t.cmd.notice = pastGridNotice(waiting)
	t.cmd.noticeUntil = time.Now().Add(pastGridNoticeFor)
}

// pastGridNotice is the bar's one line for pins with no free live slot.
func pastGridNotice(agents []string) string {
	if len(agents) == 1 {
		return fmt.Sprintf("%s is pinned with no free live slot; it shows when one is free", agents[0])
	}
	return fmt.Sprintf("%s are pinned with no free live slot; they show when one is free", strings.Join(agents, ", "))
}
