package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) handleKey(ev *tcell.EventKey) bool {
	// Welcome overlay: dismiss on any keypress.
	if t.welcome.active {
		t.welcome.active = false
		return false
	}

	// Help modal intercepts all input when active.
	if t.help.active {
		return t.handleHelpKey(ev)
	}

	// Event log modal intercepts all input when active.
	if t.eventLogM.active {
		return t.handleEventLogKey(ev)
	}

	// Top modal intercepts all input when active.
	if t.top.active {
		return t.handleTopKey(ev)
	}

	// Reorder modal intercepts all input when active.
	if t.reorder.active {
		return t.handleReorderKey(ev)
	}

	// Command modal intercepts all input when active.
	if t.cmd.active {
		return t.handleCmdKey(ev)
	}

	// Clear any lingering error message on next keypress.
	t.cmd.error = ""

	// Backtick opens the command modal.
	if ev.Key() == tcell.KeyRune && ev.Rune() == '`' && ev.Modifiers() == 0 {
		t.cmd.active = true
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.error = ""
		return false
	}

	// Alt-key combos are TUI shortcuts.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch ev.Key() {
		case tcell.KeyLeft:
			t.cycleFocus(-1)
			return false
		case tcell.KeyRight:
			t.cycleFocus(1)
			return false
		case tcell.KeyUp:
			t.cycleFocus(-1)
			return false
		case tcell.KeyDown:
			t.cycleFocus(1)
			return false
		case tcell.KeyRune:
			switch ev.Rune() {
			case '1':
				t.layoutState.Mode = LayoutFocus
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '2':
				t.layoutState.Mode = LayoutGrid
				t.layoutState.GridCols = 2
				t.layoutState.GridRows = 2
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '3':
				t.layoutState.Mode = LayoutGrid
				t.layoutState.GridCols = 3
				t.layoutState.GridRows = 3
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '4':
				t.layoutState.Mode = Layout2Col
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case 's':
				// Overlay toggle is deliberately not persisted. It's a
				// lightweight view preference (like scrollback position),
				// not a structural layout change. Always starts visible.
				t.layoutState.Overlay = !t.layoutState.Overlay
				return false
			case 'z':
				t.layoutState.Zoomed = !t.layoutState.Zoomed
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case 'u':
				// Manual update check (bypass 24h cache).
				t.checkForUpdate()
				return false
			case 'q':
				// Open confirmation instead of quitting immediately.
				// Alt+q is one key away from Alt+z and Alt+s; accidental
				// presses must not silently kill all agents.
				t.cmd.active = true
				t.cmd.pendingConfirm = "quit"
				t.cmd.confirmMsg = "Quit will stop all agents. Enter to confirm, Esc to cancel."
				t.cmd.confirmExpiry = time.Now().Add(10 * time.Second)
				return false
			}
		}
	}

	// Everything else goes to the focused pane.
	if fp := t.focusedPane(); fp != nil {
		fp.SendKey(ev)
	}
	return false
}

// handleHelpKey processes key events while the help modal is open.
func (t *TUI) handleHelpKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.help.active = false
		return false
	case tcell.KeyUp:
		if t.help.scrollOffset > 0 {
			t.help.scrollOffset--
		}
		return false
	case tcell.KeyDown:
		if t.help.scrollOffset < t.helpMaxOffset() {
			t.help.scrollOffset++
		}
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case '`', 'q':
			t.help.active = false
			return false
		case 'j':
			if t.help.scrollOffset < t.helpMaxOffset() {
				t.help.scrollOffset++
			}
			return false
		case 'k':
			if t.help.scrollOffset > 0 {
				t.help.scrollOffset--
			}
			return false
		}
	}
	return false
}

// handleCmdKey processes key events while the command modal is open.
func (t *TUI) handleCmdKey(ev *tcell.EventKey) bool {
	// Confirmation state: pending destructive command waiting for Enter.
	// Expiry is handled by pruneConfirmation() called on each render tick,
	// not here, so pressing Enter at exactly the deadline still confirms.
	if t.cmd.pendingConfirm != "" {
		switch ev.Key() {
		case tcell.KeyEnter:
			return t.executeConfirmed()
		case tcell.KeyEscape, tcell.KeyCtrlC:
			t.cmd.pendingConfirm = ""
			t.cmd.confirmMsg = ""
			t.cmd.active = false
			t.cmd.buf = t.cmd.buf[:0]
			return false
		default:
			// Any other key cancels the confirmation.
			t.cmd.pendingConfirm = ""
			t.cmd.confirmMsg = ""
			t.cmd.buf = t.cmd.buf[:0]
			return false
		}
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.cmd.suggestions = nil
		return false
	case tcell.KeyEnter:
		cmd := strings.TrimSpace(string(t.cmd.buf))
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.cmd.suggestions = nil
		return t.execCmd(cmd)
	case tcell.KeyTab:
		t.tabComplete()
		t.cmd.suggestions = nil
		return false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(t.cmd.buf) > 0 {
			t.cmd.buf = t.cmd.buf[:len(t.cmd.buf)-1]
		}
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.updateSuggestions()
		return false
	case tcell.KeyRune:
		// Backtick while empty closes the modal.
		if ev.Rune() == '`' && len(t.cmd.buf) == 0 {
			t.cmd.active = false
			t.cmd.tabBuf = ""
			t.cmd.tabHint = ""
			t.cmd.suggestions = nil
			return false
		}
		t.cmd.buf = append(t.cmd.buf, ev.Rune())
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.updateSuggestions()
		return false
	}
	return false
}

// executeConfirmed executes a confirmed destructive command. Called after the
// operator presses Enter a second time to confirm a pending destructive action.
func (t *TUI) executeConfirmed() bool {
	pending := t.cmd.pendingConfirm
	t.cmd.pendingConfirm = ""
	t.cmd.confirmMsg = ""
	t.cmd.active = false
	t.cmd.buf = t.cmd.buf[:0]

	parts := strings.Fields(pending)
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "quit":
		// Show immediate feedback before the multi-second shutdown.
		if t.screen != nil {
			sw, sh := t.screen.Size()
			style := tcell.StyleDefault.Background(tcell.NewRGBColor(30, 30, 30)).Foreground(tcell.ColorYellow).Bold(true)
			// Clear both status bar rows (sh-2 and sh-1) to remove the
			// confirmation prompt before drawing the quitting message.
			for _, row := range []int{sh - 2, sh - 1} {
				for x := 0; x < sw; x++ {
					t.screen.SetContent(x, row, ' ', nil, style)
				}
			}
			msg := " Quitting..."
			for i, ch := range msg {
				if i < sw {
					t.screen.SetContent(i, sh-2, ch, nil, style)
				}
			}
			t.screen.Show()
		}
		return true
	case "remove", "rm":
		if len(parts) >= 2 {
			if err := t.removePane(parts[1]); err != nil {
				t.cmd.error = "remove: " + err.Error()
			}
		}
	case "restart":
		if len(parts) >= 2 {
			if err := t.restartByName(parts[1]); err != nil {
				t.cmd.error = fmt.Sprintf("restart failed: %v", err)
			}
		}
	}
	return false
}

// tabComplete handles Tab keypresses in the command modal, completing agent
// names in-place. Single match: complete + trailing space. Multiple matches:
// complete to longest common prefix; second Tab shows all matches as a hint.
func (t *TUI) tabComplete() {
	buf := string(t.cmd.buf)
	parts := strings.Fields(buf)
	trailingSpace := len(buf) > 0 && buf[len(buf)-1] == ' '

	if len(parts) == 0 {
		return
	}

	cmd := parts[0]

	// Only agent-name commands are tab-completed.
	switch cmd {
	case "focus", "hide", "show", "unhide", "view", "remove", "rm", "restart", "r":
		// Fall through to completion logic.
	default:
		return
	}

	// Determine the partial argument being completed.
	var partial string
	if trailingSpace {
		partial = ""
	} else if len(parts) > 1 {
		partial = parts[len(parts)-1]
	} else {
		// Only the command is typed with no trailing space; nothing to complete yet.
		return
	}

	candidates := t.completionCandidates(cmd)

	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(c, partial) {
			matches = append(matches, c)
		}
	}

	if len(matches) == 0 {
		return
	}

	// Build the prefix up to (but not including) the partial argument.
	var argPrefix string
	if trailingSpace {
		argPrefix = buf
	} else if len(parts) > 1 {
		argPrefix = strings.Join(parts[:len(parts)-1], " ") + " "
	}

	if len(matches) == 1 {
		// Unambiguous: complete with trailing space.
		t.cmd.buf = []rune(argPrefix + matches[0] + " ")
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		return
	}

	// Multiple matches: complete to longest common prefix.
	lcp := longestCommonPrefix(matches)

	if lcp != partial {
		// Advance the partial to the LCP.
		t.cmd.buf = []rune(argPrefix + lcp)
		t.cmd.tabBuf = string(t.cmd.buf)
		t.cmd.tabHint = ""
		return
	}

	// LCP equals the partial: we're at maximum prefix. Show or refresh the hint.
	t.cmd.tabBuf = buf
	t.cmd.tabHint = strings.Join(matches, "  ")
}

// completionCandidates returns the agent names that are valid completions for
// the given command keyword.
func (t *TUI) completionCandidates(cmd string) []string {
	switch cmd {
	case "show":
		// All pane keys + "all" (reorder applies to any pane).
		names := make([]string, len(t.panes))
		for i, p := range t.panes {
			names[i] = paneKey(p)
		}
		names = append(names, "all")
		return names
	case "unhide":
		// Hidden panes plus the special "all" keyword.
		var names []string
		for _, p := range t.panes {
			if t.layoutState.Hidden[paneKey(p)] {
				names = append(names, paneKey(p))
			}
		}
		names = append(names, "all")
		return names
	case "hide":
		// Visible panes only.
		var names []string
		for _, p := range t.panes {
			if !t.layoutState.Hidden[paneKey(p)] {
				names = append(names, paneKey(p))
			}
		}
		return names
	case "pin":
		// Unpinned panes only.
		var names []string
		for _, p := range t.panes {
			if !t.layoutState.Pinned[paneKey(p)] {
				names = append(names, paneKey(p))
			}
		}
		return names
	case "unpin":
		// Pinned panes only.
		var names []string
		for _, p := range t.panes {
			if t.layoutState.Pinned[paneKey(p)] {
				names = append(names, paneKey(p))
			}
		}
		return names
	default:
		// All pane names.
		names := make([]string, len(t.panes))
		for i, p := range t.panes {
			names[i] = paneKey(p)
		}
		return names
	}
}

// longestCommonPrefix returns the longest string that is a prefix of every
// element in strs. Returns "" for an empty slice.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

// commandNames lists all valid command keywords for fuzzy matching.
var commandNames = []string{
	"grid", "focus", "zoom", "panel", "main",
	"show", "hide", "unhide", "view", "layout",
	"restart", "patrol", "top", "add", "remove",
	"log", "help", "quit", "pin", "unpin", "events",
}

// commandAliases maps short aliases to their canonical display form.
var commandAliases = map[string]string{
	"ps": "top (ps)",
	"r":  "restart (r)",
	"rm": "remove (rm)",
	"?":  "help (?)",
	"q":  "quit (q)",
}

// levenshtein returns the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	d := make([]int, lb+1)
	for j := range d {
		d[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := d[0]
		d[0] = i
		for j := 1; j <= lb; j++ {
			old := d[j]
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[j] = min(d[j]+1, min(d[j-1]+1, prev+cost))
			prev = old
		}
	}
	return d[lb]
}

// updateSuggestions computes fuzzy command matches for the first word in the
// command buffer. Called after every keystroke. Results stored in t.cmd.suggestions.
func (t *TUI) updateSuggestions() {
	t.cmd.suggestions = nil

	buf := strings.TrimSpace(string(t.cmd.buf))
	if buf == "" {
		return
	}

	// Only suggest while typing the first word (command keyword).
	// Once a space is typed, the user is on arguments; tab completion handles that.
	parts := strings.Fields(buf)
	if len(parts) > 1 || (len(buf) > 0 && buf[len(buf)-1] == ' ') {
		return
	}
	typed := strings.ToLower(parts[0])

	// Check exact match against commands and aliases.
	for _, name := range commandNames {
		if typed == name {
			return // exact match, no suggestion needed
		}
	}
	if _, ok := commandAliases[typed]; ok {
		return
	}

	type match struct {
		display  string
		dist     int
		isPrefix bool
	}
	var matches []match

	// Match against command names.
	for _, name := range commandNames {
		if strings.HasPrefix(name, typed) {
			matches = append(matches, match{name, 0, true})
		} else if dist := levenshtein(typed, name); dist <= 2 {
			matches = append(matches, match{name, dist, false})
		}
	}

	// Match against aliases.
	for alias, display := range commandAliases {
		if strings.HasPrefix(alias, typed) {
			matches = append(matches, match{display, 0, true})
		} else if dist := levenshtein(typed, alias); dist <= 2 {
			matches = append(matches, match{display, dist, false})
		}
	}

	// Sort: prefix matches first, then by distance.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].isPrefix != matches[j].isPrefix {
			return matches[i].isPrefix
		}
		return matches[i].dist < matches[j].dist
	})

	// Deduplicate and take top 3.
	seen := make(map[string]bool)
	for _, m := range matches {
		if seen[m.display] {
			continue
		}
		seen[m.display] = true
		t.cmd.suggestions = append(t.cmd.suggestions, m.display)
		if len(t.cmd.suggestions) >= 3 {
			break
		}
	}
}

// execCmd parses and executes a command string. Returns true if the TUI should quit.
func (t *TUI) execCmd(cmd string) bool {
	if cmd == "" {
		return false
	}

	parts := strings.Fields(cmd)
	switch parts[0] {
	case "grid":
		return t.cmdGrid(parts)
	case "focus":
		return t.cmdFocus(parts)
	case "zoom":
		return t.cmdZoom()
	case "panel":
		return t.cmdPanel()
	case "main":
		return t.cmdMain()
	case "show":
		return t.cmdShow(parts)
	case "unhide":
		return t.cmdUnhide(parts)
	case "hide":
		return t.cmdHide(parts)
	case "pin":
		return t.cmdPin(parts)
	case "unpin":
		return t.cmdUnpin(parts)
	case "view":
		return t.cmdView(parts)
	case "layout":
		return t.cmdLayout(parts)
	case "restart", "r":
		return t.cmdRestart(parts)
	case "patrol":
		return t.cmdPatrol()
	case "top", "ps":
		return t.cmdTop()
	case "add":
		return t.cmdAdd(parts)
	case "remove", "rm":
		return t.cmdRemove(parts)
	case "log", "events":
		return t.cmdLog()
	case "order":
		return t.cmdOrder()
	case "help", "?":
		return t.cmdHelp()
	case "quit", "q":
		return t.cmdQuit()
	default:
		t.cmd.error = fmt.Sprintf("unknown command %q", parts[0])
	}
	return false
}

// ── Command handlers ────────────────────────────────────────────────
// Each handler corresponds to one command in the backtick modal.
// They receive the already-split parts and return true only to quit.

func (t *TUI) cmdGrid(parts []string) bool {
	if len(parts) < 2 {
		visCount := t.visibleCountFromState()
		c, r := autoGrid(visCount)
		t.layoutState.Mode = LayoutGrid
		t.layoutState.GridCols = c
		t.layoutState.GridRows = r
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()
		return false
	}
	visCount := t.visibleCountFromState()
	cols, rows, ok := parseGrid(parts[1], visCount)
	if !ok {
		t.cmd.error = fmt.Sprintf("invalid grid %q, use CxR or just C (e.g. 3x3, 4)", parts[1])
		return false
	}
	t.layoutState.Mode = LayoutGrid
	t.layoutState.GridCols = cols
	t.layoutState.GridRows = rows
	t.layoutState.Zoomed = false
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdFocus(parts []string) bool {
	if len(parts) < 2 {
		t.layoutState.Mode = LayoutFocus
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()
		return false
	}
	name := parts[1]
	pv := t.findPaneByName(name)
	if pv == nil {
		t.cmd.error = fmt.Sprintf("unknown agent %q", name)
		return false
	}
	t.layoutState.Focused = paneKey(pv)
	t.layoutState.Mode = LayoutFocus
	t.layoutState.Zoomed = false
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdZoom() bool {
	t.layoutState.Zoomed = !t.layoutState.Zoomed
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdPanel() bool {
	// Overlay toggle is deliberately not persisted (see Alt+s comment).
	t.layoutState.Overlay = !t.layoutState.Overlay
	return false
}

func (t *TUI) cmdMain() bool {
	t.layoutState.Mode = Layout2Col
	t.layoutState.Zoomed = false
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdShow(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: show <name1> [name2] ..."
		return false
	}

	// Parse names: handle both comma-separated and space-separated.
	var names []string
	for _, p := range parts[1:] {
		for _, n := range strings.Split(p, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				names = append(names, n)
			}
		}
	}

	if len(names) == 1 && names[0] == "all" {
		sort.Slice(t.panes, func(i, j int) bool {
			return paneKey(t.panes[i]) < paneKey(t.panes[j])
		})
		t.applyLayout()
		t.saveLayoutIfConfigured()
		return false
	}

	// Deduplicate while preserving order.
	seen := make(map[string]bool, len(names))
	deduped := names[:0]
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			deduped = append(deduped, n)
		}
	}
	names = deduped

	// Validate all names exist.
	for _, name := range names {
		if t.findPaneByName(name) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", name)
			return false
		}
	}

	// Build new order: named panes first, then remaining in current order.
	namedSet := make(map[string]bool, len(names))
	for _, n := range names {
		namedSet[n] = true
	}
	var newOrder []PaneView
	for _, name := range names {
		for _, p := range t.panes {
			if paneKey(p) == name {
				newOrder = append(newOrder, p)
				break
			}
		}
	}
	for _, p := range t.panes {
		if !namedSet[paneKey(p)] {
			newOrder = append(newOrder, p)
		}
	}
	t.panes = newOrder
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdUnhide(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: unhide <name> or unhide all"
		return false
	}
	if parts[1] == "all" {
		t.layoutState.Hidden = make(map[string]bool)
		t.recalcGrid(false)
		t.saveLayoutIfConfigured()
		return false
	}
	if t.findPaneByName(parts[1]) == nil {
		t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
		return false
	}
	delete(t.layoutState.Hidden, parts[1])
	t.recalcGrid(false)
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdHide(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: hide <name>"
		return false
	}
	if parts[1] == "all" {
		t.cmd.error = "cannot hide all panes"
		return false
	}
	if t.findPaneByName(parts[1]) == nil {
		t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
		return false
	}
	if t.layoutState.Hidden[parts[1]] {
		return false // Already hidden.
	}
	if t.visibleCountFromState() <= 1 {
		t.cmd.error = "cannot hide last visible pane"
		return false
	}
	if t.layoutState.Hidden == nil {
		t.layoutState.Hidden = make(map[string]bool)
	}
	t.layoutState.Hidden[parts[1]] = true
	t.recalcGrid(false)
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdPin(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: pin <name>"
		return false
	}
	p := t.findPaneByName(parts[1])
	if p == nil {
		t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
		return false
	}
	if t.layoutState.Pinned == nil {
		t.layoutState.Pinned = make(map[string]bool)
	}
	t.layoutState.Pinned[parts[1]] = true
	if lp, ok := p.(*Pane); ok {
		lp.SetPinned(true)
	}
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdUnpin(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: unpin <name>"
		return false
	}
	p := t.findPaneByName(parts[1])
	if p == nil {
		t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
		return false
	}
	delete(t.layoutState.Pinned, parts[1])
	if lp, ok := p.(*Pane); ok {
		lp.SetPinned(false)
	}
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdView(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: view <name1> [name2] ..."
		return false
	}
	for _, name := range parts[1:] {
		if t.findPaneByName(name) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", name)
			return false
		}
	}
	show := make(map[string]bool, len(parts)-1)
	for _, name := range parts[1:] {
		show[name] = true
	}
	visCount := 0
	for _, p := range t.panes {
		if show[paneKey(p)] {
			visCount++
		}
	}
	if visCount == 0 {
		t.cmd.error = "view must include at least one valid pane"
		return false
	}
	hidden := make(map[string]bool)
	for _, p := range t.panes {
		if !show[paneKey(p)] {
			hidden[paneKey(p)] = true
		}
	}
	t.layoutState.Hidden = hidden
	t.recalcGrid(false)
	t.saveLayoutIfConfigured()
	return false
}

func (t *TUI) cmdLayout(parts []string) bool {
	if len(parts) < 2 {
		t.cmd.error = "usage: layout reset"
		return false
	}
	switch parts[1] {
	case "reset":
		// Delete the saved layout file and revert to defaults.
		// We deliberately don't call saveLayoutIfConfigured() here:
		// the intent is to remove persistence so the next startup
		// auto-calculates from the role count. Re-saving would
		// immediately recreate the file with default values.
		if t.projectRoot != "" {
			DeleteLayout(t.projectRoot)
		}
		keys := make([]string, len(t.panes))
		for i, p := range t.panes {
			keys[i] = paneKey(p)
		}
		t.layoutState = DefaultLayoutState(keys)
		t.applyLayout()
	default:
		t.cmd.error = fmt.Sprintf("unknown layout subcommand %q", parts[1])
	}
	return false
}

func (t *TUI) cmdRestart(parts []string) bool {
	if len(parts) >= 2 && parts[1] != "" {
		// Named restart requires confirmation: operator must press Enter again.
		name := parts[1]
		if t.findPaneByName(name) == nil {
			t.cmd.error = fmt.Sprintf("restart: unknown agent %q", name)
			return false
		}
		t.cmd.pendingConfirm = "restart " + name
		t.cmd.confirmMsg = fmt.Sprintf("Restart %s? Context window will be lost. Enter to confirm, Esc to cancel.", name)
		t.cmd.confirmExpiry = time.Now().Add(10 * time.Second)
		t.cmd.active = true
	} else {
		// No-arg restart: restart focused pane immediately (less dangerous).
		if err := t.restartFocused(); err != nil {
			t.cmd.error = fmt.Sprintf("restart failed: %v", err)
		}
	}
	return false
}

func (t *TUI) cmdPatrol() bool {
	// Build patrol output and copy to clipboard for easy pasting.
	var buf strings.Builder
	for _, p := range t.panes {
		header := p.Name() + " (" + p.Activity().String()
		if bead := p.BeadID(); bead != "" {
			header += " | " + bead
		}
		header += ")"
		buf.WriteString("=== " + header + " ===\n")
		content := strings.TrimRight(peekContent(p, 20), "\n")
		if content == "" {
			buf.WriteString("[no recent output]\n")
		} else {
			buf.WriteString(content + "\n")
		}
		buf.WriteByte('\n')
	}
	t.cmd.error = copyToClipboard(buf.String())
	return false
}

func (t *TUI) cmdTop() bool {
	t.cmd.error = "" // Clear stale errors so they don't reappear on close.
	t.top.active = true
	t.top.selected = 0
	t.top.cacheTime = time.Time{} // Force fresh data.
	return false
}

func (t *TUI) cmdAdd(parts []string) bool {
	if len(parts) < 2 || parts[1] == "" {
		t.cmd.error = "usage: add <name>"
	} else if err := t.addPane(parts[1]); err != nil {
		t.cmd.error = "add: " + err.Error()
	} else {
		t.cmd.error = "added " + parts[1]
	}
	return false
}

func (t *TUI) cmdRemove(parts []string) bool {
	if len(parts) < 2 || parts[1] == "" {
		t.cmd.error = "usage: remove <name>"
	} else if t.findPaneByName(parts[1]) == nil {
		t.cmd.error = fmt.Sprintf("remove: unknown agent %q", parts[1])
	} else {
		name := parts[1]
		t.cmd.pendingConfirm = "remove " + name
		t.cmd.confirmMsg = fmt.Sprintf("Remove %s? This kills the process. Enter to confirm, Esc to cancel.", name)
		t.cmd.confirmExpiry = time.Now().Add(10 * time.Second)
		t.cmd.active = true
	}
	return false
}

func (t *TUI) cmdLog() bool {
	t.cmd.error = ""
	t.eventLogM.active = true
	t.eventLogM.scrollOffset = 0
	return false
}

func (t *TUI) cmdOrder() bool {
	items := make([]string, len(t.panes))
	for i, p := range t.panes {
		items[i] = paneKey(p)
	}
	t.reorder = reorderModal{
		active: true,
		items:  items,
		cursor: 0,
	}
	return false
}

// handleReorderKey handles input while the reorder modal is active.
func (t *TUI) handleReorderKey(ev *tcell.EventKey) bool {
	n := len(t.reorder.items)
	if n == 0 {
		t.reorder.active = false
		return false
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		// Cancel: discard changes.
		t.reorder.active = false
		return false

	case tcell.KeyEnter:
		// Toggle pick/drop.
		t.reorder.moving = !t.reorder.moving
		return false

	case tcell.KeyRune:
		switch ev.Rune() {
		case ' ':
			// Confirm: apply the new order.
			t.reorder.active = false
			t.layoutState.Order = make([]string, len(t.reorder.items))
			copy(t.layoutState.Order, t.reorder.items)
			reorderPanes(t.panes, t.layoutState.Order)
			t.applyLayout()
			t.saveLayoutIfConfigured()
			return false

		case 'j':
			if t.reorder.moving {
				// Move picked item down.
				if t.reorder.cursor < n-1 {
					i := t.reorder.cursor
					t.reorder.items[i], t.reorder.items[i+1] = t.reorder.items[i+1], t.reorder.items[i]
					t.reorder.cursor++
				}
			} else {
				// Move cursor down.
				if t.reorder.cursor < n-1 {
					t.reorder.cursor++
				}
			}
			return false

		case 'k':
			if t.reorder.moving {
				// Move picked item up.
				if t.reorder.cursor > 0 {
					i := t.reorder.cursor
					t.reorder.items[i], t.reorder.items[i-1] = t.reorder.items[i-1], t.reorder.items[i]
					t.reorder.cursor--
				}
			} else {
				// Move cursor up.
				if t.reorder.cursor > 0 {
					t.reorder.cursor--
				}
			}
			return false
		}

	case tcell.KeyDown:
		if t.reorder.moving {
			if t.reorder.cursor < n-1 {
				i := t.reorder.cursor
				t.reorder.items[i], t.reorder.items[i+1] = t.reorder.items[i+1], t.reorder.items[i]
				t.reorder.cursor++
			}
		} else if t.reorder.cursor < n-1 {
			t.reorder.cursor++
		}
		return false

	case tcell.KeyUp:
		if t.reorder.moving {
			if t.reorder.cursor > 0 {
				i := t.reorder.cursor
				t.reorder.items[i], t.reorder.items[i-1] = t.reorder.items[i-1], t.reorder.items[i]
				t.reorder.cursor--
			}
		} else if t.reorder.cursor > 0 {
			t.reorder.cursor--
		}
		return false
	}
	return false
}

func (t *TUI) cmdHelp() bool {
	t.help.active = true
	t.help.scrollOffset = 0
	return false
}

func (t *TUI) cmdQuit() bool {
	t.cmd.pendingConfirm = "quit"
	t.cmd.confirmMsg = "Quit will stop all agents. Enter to confirm, Esc to cancel."
	t.cmd.confirmExpiry = time.Now().Add(10 * time.Second)
	t.cmd.active = true
	return false
}

// parseGrid parses "CxR" or just "C" (auto-calculating rows from numPanes).
func parseGrid(s string, numPanes int) (cols, rows int, ok bool) {
	s = strings.ToLower(s)
	if strings.Contains(s, "x") {
		parts := strings.SplitN(s, "x", 2)
		if len(parts) != 2 {
			return 0, 0, false
		}
		c, err1 := strconv.Atoi(parts[0])
		r, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || c < 1 || r < 1 || c > 10 || r > 10 {
			return 0, 0, false
		}
		return c, r, true
	}
	// Just a column count; auto-calculate rows.
	c, err := strconv.Atoi(s)
	if err != nil || c < 1 || c > 10 {
		return 0, 0, false
	}
	r := (numPanes + c - 1) / c
	if r < 1 {
		r = 1
	}
	return c, r, true
}

// findPaneByName returns the pane with the given name, or nil.
func (t *TUI) findPaneByName(name string) PaneView {
	// First try exact paneKey match (handles "workbench:eng1" for remote).
	for _, p := range t.panes {
		if paneKey(p) == name {
			return p
		}
	}
	// Fall back to bare Name match for IPC commands that use short names.
	for _, p := range t.panes {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// visibleCountFromState returns the number of visible panes based on layoutState.
func (t *TUI) visibleCountFromState() int {
	n := 0
	for _, p := range t.panes {
		if !t.layoutState.Hidden[paneKey(p)] {
			n++
		}
	}
	return n
}

func (t *TUI) handleResize() {
	t.screen.Sync()
	t.applyLayout()
}

func (t *TUI) cycleFocus(delta int) {
	n := len(t.panes)
	if n == 0 {
		return
	}
	// Find current focused index.
	cur := 0
	for i, p := range t.panes {
		if paneKey(p) == t.layoutState.Focused {
			cur = i
			break
		}
	}
	// Skip hidden panes. Try every pane at most once.
	next := cur
	for i := 0; i < n; i++ {
		next = (next + delta + n) % n
		if !t.layoutState.Hidden[paneKey(t.panes[next])] {
			t.layoutState.Focused = paneKey(t.panes[next])
			t.applyLayout()
			return
		}
	}
}


// restartFocused kills the focused pane's process and starts a new one.
func (t *TUI) restartFocused() error {
	fp := t.focusedPane()
	if fp == nil {
		return fmt.Errorf("no pane focused")
	}
	local, ok := fp.(*Pane)
	if !ok {
		return fmt.Errorf("restart not supported for remote panes")
	}
	return t.restartPane(local)
}

// restartByName finds the named pane and restarts it.
func (t *TUI) restartByName(name string) error {
	p := t.findPaneByName(name)
	if p == nil {
		return fmt.Errorf("unknown agent %q", name)
	}
	local, ok := p.(*Pane)
	if !ok {
		return fmt.Errorf("restart not supported for remote panes")
	}
	return t.restartPane(local)
}

// restartPane kills the given pane's process and starts a new one at the same
// index in the pane list.
func (t *TUI) restartPane(fp *Pane) error {
	idx := -1
	for i, p := range t.panes {
		if p == fp {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("pane not found")
	}
	cols := fp.emu.Width()
	rows := fp.emu.Height()
	if cols < 10 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}
	// Serialize with any in-flight IPC send before closing.
	// Without this lock, handleIPCSend may be mid-sleep inside its retry loop
	// (holding sendMu) while Close() tears down the PTY underneath it.
	fp.sendMu.Lock()
	fp.Close()
	fp.sendMu.Unlock()

	p, err := NewPane(fp.cfg, rows, cols)
	if err != nil {
		return err
	}
	p.eventCh = t.agentEvents
	p.safeGo = t.safeGo
	p.pinned = fp.pinned
	p.Start()
	t.panes[idx] = p
	t.applyLayout()
	return nil
}

// copyToClipboard writes text to the system clipboard using the appropriate
// tool for the current OS. Returns a status message suitable for t.cmd.error.
// On macOS uses pbcopy; on Linux tries wl-copy (Wayland) then xclip (X11).
// If no clipboard tool is available the copy is silently skipped and the
// caller message indicates the text remains visible in the patrol view.
func copyToClipboard(text string) string {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	if cmd == nil {
		return "patrol: clipboard unavailable (no pbcopy/wl-copy/xclip found)"
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return "patrol: " + err.Error()
	}
	return "patrol: copied to clipboard"
}
