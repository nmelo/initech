package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

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
			t.cmd.cursor = 0
			return false
		default:
			// Any other key cancels the confirmation.
			t.cmd.pendingConfirm = ""
			t.cmd.confirmMsg = ""
			t.cmd.buf = t.cmd.buf[:0]
			t.cmd.cursor = 0
			return false
		}
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.cursor = 0
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.cmd.suggestions = nil
		return false
	case tcell.KeyEnter:
		cmd := strings.TrimSpace(string(t.cmd.buf))
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.cursor = 0
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.cmd.suggestions = nil
		return t.execCmd(cmd)
	case tcell.KeyTab:
		t.tabComplete()
		t.cmd.cursor = len(t.cmd.buf)
		t.cmd.suggestions = nil
		return false

	// Movement: Ctrl+A / Home -> beginning of line.
	case tcell.KeyCtrlA, tcell.KeyHome:
		t.cmd.cursor = 0
		return false
	// Movement: Ctrl+E / End -> end of line.
	case tcell.KeyCtrlE, tcell.KeyEnd:
		t.cmd.cursor = len(t.cmd.buf)
		return false
	// Movement: Ctrl+B / Left -> back one character.
	case tcell.KeyCtrlB, tcell.KeyLeft:
		if t.cmd.cursor > 0 {
			t.cmd.cursor--
		}
		return false
	// Movement: Ctrl+F / Right -> forward one character.
	case tcell.KeyCtrlF, tcell.KeyRight:
		if t.cmd.cursor < len(t.cmd.buf) {
			t.cmd.cursor++
		}
		return false

	// Deletion: Backspace / Ctrl+H -> delete character left of cursor.
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if t.cmd.cursor > 0 {
			t.cmd.buf = append(t.cmd.buf[:t.cmd.cursor-1], t.cmd.buf[t.cmd.cursor:]...)
			t.cmd.cursor--
		}
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.updateSuggestions()
		return false
	// Deletion: Delete / Ctrl+D -> delete character at cursor.
	case tcell.KeyDelete, tcell.KeyCtrlD:
		if t.cmd.cursor < len(t.cmd.buf) {
			t.cmd.buf = append(t.cmd.buf[:t.cmd.cursor], t.cmd.buf[t.cmd.cursor+1:]...)
		}
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.updateSuggestions()
		return false
	// Deletion: Ctrl+W -> delete word left of cursor.
	case tcell.KeyCtrlW:
		if t.cmd.cursor > 0 {
			// Skip trailing spaces, then delete until the next space or start.
			i := t.cmd.cursor
			for i > 0 && t.cmd.buf[i-1] == ' ' {
				i--
			}
			for i > 0 && t.cmd.buf[i-1] != ' ' {
				i--
			}
			t.cmd.buf = append(t.cmd.buf[:i], t.cmd.buf[t.cmd.cursor:]...)
			t.cmd.cursor = i
		}
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.updateSuggestions()
		return false
	// Deletion: Ctrl+U -> delete from start to cursor.
	case tcell.KeyCtrlU:
		t.cmd.buf = t.cmd.buf[t.cmd.cursor:]
		t.cmd.cursor = 0
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		t.updateSuggestions()
		return false
	// Deletion: Ctrl+K -> delete from cursor to end.
	case tcell.KeyCtrlK:
		t.cmd.buf = t.cmd.buf[:t.cmd.cursor]
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
		// Insert at cursor position.
		t.cmd.buf = append(t.cmd.buf, 0)
		copy(t.cmd.buf[t.cmd.cursor+1:], t.cmd.buf[t.cmd.cursor:])
		t.cmd.buf[t.cmd.cursor] = ev.Rune()
		t.cmd.cursor++
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
	t.cmd.cursor = 0

	parts := strings.Fields(pending)
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "quit":
		// Show immediate feedback before the multi-second shutdown.
		if t.screen != nil {
			sw, sh := t.screen.Size()
			bg := tcell.NewRGBColor(30, 30, 30)
			clearStyle := tcell.StyleDefault.Background(bg)
			// Clear status bar rows (sh-3, sh-2, sh-1) to remove the
			// confirmation prompt before drawing the quitting message.
			for _, row := range []int{sh - 3, sh - 2, sh - 1} {
				for x := 0; x < sw; x++ {
					t.screen.SetContent(x, row, ' ', nil, clearStyle)
				}
			}
			msg := " Quitting..."
			rainbow := []tcell.Color{
				tcell.NewRGBColor(255, 0, 0),     // red
				tcell.NewRGBColor(255, 140, 0),   // orange
				tcell.NewRGBColor(255, 255, 0),   // yellow
				tcell.NewRGBColor(0, 200, 0),     // green
				tcell.NewRGBColor(0, 220, 220),   // cyan
				tcell.NewRGBColor(60, 60, 255),   // blue
				tcell.NewRGBColor(160, 32, 240),  // violet
			}
			for i, ch := range msg {
				if i < sw {
					style := tcell.StyleDefault.Background(bg).Foreground(rainbow[i%len(rainbow)]).Bold(true)
					t.screen.SetContent(i, sh-3, ch, nil, style)
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
	case "focus", "remove", "rm", "restart", "r":
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
	// All pane names.
	names := make([]string, len(t.panes))
	for i, p := range t.panes {
		names[i] = paneKey(p)
	}
	return names
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
	"layout", "restart", "patrol", "top", "add", "remove",
	"log", "help", "quit", "events", "agents", "mcp",
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
	case "layout":
		return t.cmdLayout(parts)
	case "restart", "r":
		return t.cmdRestart(parts)
	case "patrol":
		return t.cmdPatrol()
	case "top", "ps":
		return t.cmdTop()
	case "agents":
		t.openAgentsModal()
		return false
	case "add":
		return t.cmdAdd(parts)
	case "remove", "rm":
		return t.cmdRemove(parts)
	case "log", "events":
		return t.cmdLog()
	case "order":
		return t.cmdOrder()
	case "mcp":
		return t.cmdMcp()
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
	// Build patrol output summary.
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
	t.cmd.error = fmt.Sprintf("patrol: %d agents", len(t.panes))
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

func (t *TUI) cmdMcp() bool {
	t.mcpM.active = true
	t.mcpM.tokenRevealed = false
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
