package tui

import (
	"fmt"
	"strings"
)

// panelHiddenIdle returns the window's hidden agents and, of them, the ones
// the agents box counts idle: awake, not working and holding no bead
// (ini-vag8). Those have no row in the box and so no dot (ini-b0po), which is
// why the box's last line names them. The idle names keep roster order.
func panelHiddenIdle(agents []panelAgent, working, holding []panelBoxRow) (hidden int, idle []string) {
	shown := make(map[string]bool, len(working)+len(holding))
	for _, l := range [][]panelBoxRow{working, holding} {
		for _, r := range l {
			shown[panelRowKey(r.a)] = true
		}
	}
	for _, a := range agents {
		if !a.Hidden {
			continue
		}
		hidden++
		if a.awake() && !shown[panelRowKey(a)] {
			idle = append(idle, a.Name)
		}
	}
	return hidden, idle
}

// panelRowKey is the key panelSections settles an agent under.
func panelRowKey(a panelAgent) string {
	if a.Key != "" {
		return a.Key
	}
	return a.Name
}

// panelHiddenLine is the agents box's last line when any of the window's
// agents is hidden (ini-vag8, pm R2): how many are hidden, how many of them
// are idle and which, and the one way back that exists in this window. A
// second window has no command bar (ini-fn77) and an idle agent has no dot,
// so the way is the main window's Agents panel. Names past width become
// "+N"; "" when nothing is hidden.
func panelHiddenLine(hidden int, idle []string, secondWindow bool, width int) string {
	if hidden == 0 {
		return ""
	}
	way := "show them from the Agents panel (Option+a)"
	if secondWindow {
		way = "show them from the main window's Agents panel (Option+a there)"
	}
	if len(idle) == 0 {
		return fmt.Sprintf("%d hidden: %s", hidden, way)
	}
	line := func(names int) string {
		list := strings.Join(idle[:names], ", ")
		if rest := len(idle) - names; rest > 0 {
			if list != "" {
				list += ", "
			}
			list += fmt.Sprintf("+%d", rest)
		}
		return fmt.Sprintf("%d hidden, %d of them idle (%s): %s", hidden, len(idle), list, way)
	}
	for n := len(idle); n > 0; n-- {
		if l := line(n); len([]rune(l)) <= width {
			return l
		}
	}
	return line(0)
}
