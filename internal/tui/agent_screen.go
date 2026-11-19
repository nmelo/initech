package tui

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/nmelo/initech/internal/config"
)

type agentReader uint8

const (
	noAgentReader agentReader = iota
	codexReader
	agyReader
)

// Select the program from the launch configuration. Pane names and transcript
// text are user controlled and do not identify the executable.
func (p *Pane) agentReader() agentReader {
	switch p.agentType {
	case config.AgentTypeCodex:
		return codexReader
	case config.AgentTypeGeneric:
		if len(p.cfg.Command) > 0 {
			switch filepath.Base(p.cfg.Command[0]) {
			case "agy", "agy.exe":
				return agyReader
			}
		}
	}
	return noAgentReader
}

type agentFrame struct {
	box          bool
	draft        string
	draftRows    []string
	queued       []string
	nextTool     bool // Codex will submit these items at its next tool call.
	busy, dialog bool
	dialogName   string
	// menuFilter is the text in agy's input box above its open slash menu,
	// the text the menu filters on ("" when no menu is open).
	menuFilter string
	native     int
	transcript []string
}

func (f agentFrame) free() bool { return f.box && !f.busy && !f.dialog && f.draft == "" }

func (r agentReader) read(rows []string) agentFrame {
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	if len(rows) < 2 {
		return agentFrame{}
	}
	if r == agyReader {
		return readAgyFrame(rows)
	}
	if r == codexReader {
		return readCodexFrame(rows)
	}
	return agentFrame{}
}

// codexNoticeRow reports one of the status notices Codex draws between its
// Working row and its composer: a row that starts with ⚠ once trimmed, such
// as "⚠ weekly limit: 11% left · /status" or, near the limit, "⚠ weekly
// limit: only 5% left · /status" (ini-bvjy). The walk from the composer to
// the Working row steps over these and blank rows, nothing else.
func codexNoticeRow(row string) bool {
	return strings.HasPrefix(strings.TrimSpace(stripBraille(row)), "⚠")
}

// codexHint is the placeholder Codex draws in an empty composer.
const codexHint = "Ask Codex to do anything"

// agyHint is the placeholder agy draws in an empty input box.
const agyHint = "Auto mode: edits & safe commands auto-approved (shift+tab to cycle)"

// minHintPrefix is the shortest cut-off placeholder read as the placeholder:
// "Auto mode:" and "Ask Codex " are ten characters; a shorter start is too
// likely to be a real draft.
const minHintPrefix = 10

// codexHintOnly reports a composer that holds only Codex's placeholder: the
// whole hint, or in a narrow pane the hint cut at the right edge on its one
// row (cut bare, "Ask Codex to do anythin" at 26 columns, or with "…").
// Every row below it must be blank.
func codexHintOnly(box []string) bool {
	for _, row := range box[1:] {
		if row != "" {
			return false
		}
	}
	cut := strings.TrimSuffix(box[0], "…")
	return box[0] == codexHint || (len(cut) >= minHintPrefix && strings.HasPrefix(codexHint, cut))
}

// agyHintOnly reports an input box that holds only agy's placeholder: the
// whole hint, or the hint agy cuts with "..." when the pane is narrower than
// it ("Auto mode: edits & safe com..." at 33 columns).
func agyHintOnly(draft string) bool {
	if draft == agyHint {
		return true
	}
	cut, ok := strings.CutSuffix(draft, "...")
	return ok && len(cut) >= minHintPrefix && strings.HasPrefix(agyHint, cut)
}

// codexFooterRow finds the first row of Codex's model and context footer in
// the last seven rows, or -1. A wide pane draws it on one row ("main · model
// · Context 0% used · ..."). A narrow pane clips it to one row ending in "…"
// ("main · GPT-6.2-Sol high · Con…" at 32 columns), the composer's nearest
// row below it after blanks. Or a narrow pane wraps it, and the context figure
// starts a row of its own ("Context 0% used ·", or "· Context 0% used ·" at
// 26 columns) below the model row. The footer then starts at the top of that
// run of rows, so the composer search begins above it and the model row is
// never read as composer text.
func codexFooterRow(rows []string) int {
	for i := len(rows) - 1; i >= 0 && i >= len(rows)-7; i-- {
		line := strings.ToLower(strings.TrimSpace(stripBraille(rows[i])))
		if strings.Contains(line, " · context") || codexClippedFooter(rows, i) {
			return i
		}
		wrapped := strings.HasPrefix(line, "context ") || strings.HasPrefix(line, "· context ")
		if !wrapped || !strings.Contains(line, "% used") {
			continue
		}
		top := i
		for top > 0 {
			above := strings.TrimSpace(stripBraille(rows[top-1]))
			if above == "" || strings.HasPrefix(above, "›") {
				break
			}
			top--
		}
		if top == i {
			return -1 // No model row above it: not a footer this reader knows.
		}
		return top
	}
	return -1
}

// codexClippedFooter reports Codex's footer clipped to one row: indented,
// " · " segments, ending in "…", with only blank rows between it and a "›"
// composer row above it. A transcript row cannot pass: the composer it would
// need is below the transcript.
func codexClippedFooter(rows []string, i int) bool {
	row := stripBraille(rows[i])
	line := strings.TrimSpace(row)
	if !strings.HasPrefix(row, "  ") || !strings.HasSuffix(line, "…") || !strings.Contains(line, " · ") {
		return false
	}
	for j := i - 1; j >= 0; j-- {
		above := strings.TrimSpace(stripBraille(rows[j]))
		if above == "" {
			continue
		}
		return strings.HasPrefix(above, "›")
	}
	return false
}

func readCodexFrame(rows []string) agentFrame {
	last := strings.TrimSpace(rows[len(rows)-1])
	switch last {
	case "enter continue · esc skip":
		return agentFrame{dialog: true, dialogName: "update picker"}
	case "enter continue · esc back":
		return agentFrame{dialog: true, dialogName: "folder trust"}
	case "Press enter to confirm or esc to cancel":
		return agentFrame{dialog: true, dialogName: "command approval"}
	case "? / esc close":
		return agentFrame{dialog: true, dialogName: "shortcuts"}
	}
	// Find the model/context footer and the nearest prompt above it. The
	// transcript also uses ›, so a glyph alone cannot open the input path.
	footer := codexFooterRow(rows)
	if footer < 0 {
		return agentFrame{}
	}
	for i := footer - 1; i >= 0; i-- {
		row := strings.TrimRight(stripBraille(rows[i]), " ")
		if !strings.HasPrefix(row, "›") {
			continue
		}
		box := []string{strings.TrimSpace(strings.TrimPrefix(row, "›"))}
		for _, line := range rows[i+1 : footer] {
			box = append(box, strings.TrimSpace(stripBraille(line)))
		}
		f := agentFrame{box: true, draftRows: box, draft: strings.TrimSpace(strings.Join(box, " ")), transcript: rows[:i]}
		if codexHintOnly(box) {
			f.draft = ""
		}
		// Inspect the tail adjacent to this composer. Earlier turns may
		// quote exactly the same progress or queue text.
		j := i - 1
		for j >= 0 && (strings.TrimSpace(stripBraille(rows[j])) == "" || codexNoticeRow(rows[j])) {
			j--
		}
		if header, items := codexNextToolItems(rows[:j+1]); len(items) > 0 {
			f.busy, f.nextTool = true, true
			f.queued, f.native = items, len(items)
			f.transcript = rows[:header]
			return f
		}
		if j >= 0 && (strings.Contains(rows[j], "edit last queued message") || strings.HasPrefix(strings.TrimSpace(rows[j]), "↳ ")) {
			for ; j >= 0; j-- {
				line := strings.TrimSpace(rows[j])
				if line == "• Queued follow-up inputs" {
					f.busy = true
					f.transcript = rows[:j]
					f.queued = rows[j+1 : i]
					j--
					break
				}
				if !strings.HasPrefix(rows[j], " ") && !strings.HasPrefix(line, "↳ ") {
					break
				}
				if strings.HasPrefix(line, "↳ ") {
					f.native++
				}
			}
			// A clipped queue is still evidence of work.
			f.busy = true
		}
		for j >= 0 && strings.TrimSpace(stripBraille(rows[j])) == "" {
			j--
		}
		// A narrow pane wraps the active status across several rows.
		for ; j >= 0; j-- {
			line := strings.TrimSpace(rows[j])
			if line == "" {
				break
			}
			progress := strings.ToLower(line)
			if codexWorkingRow(normalizeCodexRow(line)) || strings.Contains(progress, " • esc to interrupt)") {
				f.busy = true
				break
			}
			if !strings.HasPrefix(rows[j], " ") {
				break
			}
		}
		return f
	}
	return agentFrame{}
}

// codexNextToolItems reads only the shelf immediately above the composer.
// Rows are padded by the terminal. Codex indents wrapped heading text by two
// columns and item continuations by four, including a standalone ellipsis.
func codexNextToolItems(rows []string) (int, []string) {
	const heading = "• Messages to be submitted after next tool call (press esc to interrupt and send immediately)"
	header := len(rows) - 1
	for header >= 0 && strings.HasPrefix(rows[header], "  ") && strings.TrimSpace(rows[header]) != "" {
		header--
	}
	if header < 0 || !strings.HasPrefix(rows[header], "• ") {
		return 0, nil
	}
	first := header + 1
	for first < len(rows) && !strings.HasPrefix(rows[first], "  ↳ ") {
		first++
	}
	if !matchesDisplayRows(rows[header:first], heading) {
		return 0, nil
	}
	var items []string
	for _, row := range rows[first:] {
		switch {
		case strings.HasPrefix(row, "  ↳ "):
			items = append(items, strings.TrimSpace(row))
		case strings.HasPrefix(row, "    ") && strings.TrimSpace(row) != "":
			items[len(items)-1] += "\n" + strings.TrimSpace(row)
		default:
			return 0, nil
		}
	}
	return header, items
}

// nextToolAppend compares the matched slot against the shelf seen with the
// literal draft. A different, identified follow-up shelf has no next-step
// items. An unreadable shelf cannot establish the starting position.
func (f agentFrame) nextToolAppend(before agentFrame, index int) bool {
	if index != len(f.queued)-1 {
		return false
	}
	if before.nextTool {
		return slices.Equal(before.queued, f.queued[:index])
	}
	return index == 0 && (before.native == 0 || len(before.queued) > 0)
}

func agyRule(row string) bool {
	s := strings.TrimSpace(row)
	return strings.HasPrefix(s, "────") && strings.Trim(s, "─") == ""
}

// A running shell task can add a bordered shelf below the actual input box.
var agyTaskRow = regexp.MustCompile(`^● \[[0-9]{2}:[0-9]{2}:[0-9]{2}\] .+`)

func agyTopRule(rows []string, end int) int {
	top := end - 1
	for top >= 0 && !agyRule(rows[top]) {
		top--
	}
	return top
}

func readAgyFrame(rows []string) agentFrame {
	last := strings.TrimSpace(rows[len(rows)-1])
	// The model picker leaves the previous composer on screen above the
	// choices. Only read the box paired with the bottom footer.
	end := len(rows) - 2
	for end >= 0 && strings.TrimSpace(rows[end]) == "" {
		end--
	}
	if strings.Contains(last, "enter Confirm") || (end >= 0 && strings.Contains(rows[end], "enter Confirm")) ||
		(end >= 0 && strings.Contains(rows[end], "enter Select") && strings.Contains(rows[end], "esc Go Back")) {
		name := "folder trust"
		if end >= 0 && strings.Contains(rows[end], "enter Select") {
			name = "model picker"
		}
		return agentFrame{dialog: true, dialogName: name}
	}
	// agy's slash menu: its key row sits above "esc to cancel" and is cut at
	// the pane's width ("↑/↓ Navigate · enter Select · tab Complete", "· t"
	// at 33 columns). It reads as a dialog, never a box or a running task.
	if end >= 0 && strings.HasPrefix(strings.TrimSpace(rows[end]), "↑/↓ Navigate") {
		return agentFrame{dialog: true, dialogName: agySlashMenu, menuFilter: agyMenuFilter(rows, end)}
	}
	queueVisible := agyQueueFooter(last)
	busy := strings.HasPrefix(last, "esc to cancel") || queueVisible
	if !busy && strings.Contains(last, "task(s)") {
		return agentFrame{busy: true}
	}
	if !busy && !strings.HasPrefix(last, "? for shortcuts") && !strings.HasPrefix(last, "auto ·") && !strings.HasPrefix(last, "plan ·") && !strings.HasPrefix(last, "accept-edits ·") {
		return agentFrame{}
	}
	if end < 2 || !agyRule(rows[end]) {
		return agentFrame{}
	}
	top := agyTopRule(rows, end)
	if busy && top >= 0 && top+1 < end && agyTaskRow.MatchString(strings.TrimSpace(rows[top+1])) {
		for _, line := range rows[top+1 : end] {
			if !agyTaskRow.MatchString(strings.TrimSpace(line)) {
				return agentFrame{busy: true}
			}
		}
		end = top
		top = agyTopRule(rows, end)
	}
	if top < 0 || top+1 >= end || !strings.HasPrefix(rows[top+1], ">") {
		return agentFrame{}
	}
	box := []string{strings.TrimSpace(strings.TrimPrefix(rows[top+1], ">"))}
	for _, row := range rows[top+2 : end] {
		box = append(box, strings.TrimSpace(row))
	}
	draft := strings.TrimSpace(strings.Join(box, " "))
	if agyHintOnly(draft) {
		draft = ""
	}
	frame := agentFrame{box: true, busy: busy, draftRows: box, draft: draft, transcript: rows[:top]}
	if queueVisible {
		// Only the queue directly above this composer belongs to its queue footer.
		start := top
		for start > 0 && strings.HasPrefix(strings.TrimSpace(rows[start-1]), "▸ ") {
			start--
		}
		frame.transcript = rows[:start]
		for _, row := range rows[start:top] {
			frame.queued = append(frame.queued, strings.TrimSuffix(strings.TrimSpace(row), " (alt+m to send immediately)"))
		}
		frame.native = len(frame.queued)
	}
	return frame
}

// agyQueueHint is the footer agy draws while messages wait in its queue.
const agyQueueHint = "Press up to edit queued messages"

// agyQueueFooter reports agy's queue footer, whole or cut at the pane's width
// ("Press up to edit queued message" at 33 columns): at least 16 characters
// of it, so a short row is never taken for it.
func agyQueueFooter(last string) bool {
	return strings.HasPrefix(last, agyQueueHint) || (len(last) >= 16 && strings.HasPrefix(agyQueueHint, last))
}

// agySlashMenu names agy's slash-command menu as a dialog.
const agySlashMenu = "slash menu"

// agyMenuFilter is the text of the input box drawn above agy's slash menu:
// the "> " row between the two rules above the menu's items. "" when that
// shape is not on screen.
func agyMenuFilter(rows []string, keyRow int) string {
	below := keyRow - 1
	for below >= 0 && !agyRule(rows[below]) {
		below--
	}
	if below < 2 || !agyRule(rows[below-2]) || !strings.HasPrefix(rows[below-1], ">") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(rows[below-1], ">"))
}

// Earlier entries can expand when agy moves its keyboard hint to the newest
// item. Match their visible content in order before attributing the append.
func agyQueuedAppend(before, after agentFrame, proof messageProof) int {
	if after.native != before.native+1 || len(before.queued) != before.native || len(after.queued) != after.native {
		return -1
	}
	row := strings.TrimPrefix(after.queued[before.native], "▸ ")
	match := proof.match(row, true)
	if match == proofNone {
		return -1
	}
	for i, prev := range before.queued {
		old := strings.TrimPrefix(prev, "▸ ")
		now := strings.TrimPrefix(after.queued[i], "▸ ")
		if !sameQueuePreview(old, now) || (match == proofPrefix && (proof.match(now, true) != proofNone || sameQueuePreview(row, now))) {
			return -1
		}
	}
	return before.native
}

func matchesDisplayRows(rows []string, body string) bool {
	rest := strings.TrimSpace(body)
	if rest == "" {
		return false
	}
	for _, row := range rows {
		part := strings.TrimSpace(row)
		if part == "" {
			continue
		}
		var ok bool
		rest, ok = strings.CutPrefix(rest, part)
		if !ok {
			return false
		}
		rest = strings.TrimLeft(rest, " \t\n")
	}
	return rest == ""
}
