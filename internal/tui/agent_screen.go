package tui

import (
	"path/filepath"
	"regexp"
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
	busy, dialog bool
	dialogName   string
	native       int
	transcript   []string
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

var codexWeeklyNotice = regexp.MustCompile(`^ +⚠ weekly limit: [0-9]{1,3}% left · /status *$`)

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
	footer := -1
	for i := len(rows) - 1; i >= 0 && i >= len(rows)-7; i-- {
		line := strings.ToLower(strings.TrimSpace(stripBraille(rows[i])))
		if strings.Contains(line, " · context") {
			footer = i
			break
		}
	}
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
		if f.draft == "Ask Codex to do anything" {
			f.draft = ""
		}
		// Inspect the tail adjacent to this composer. Earlier turns may
		// quote exactly the same progress or queue text.
		j := i - 1
		for j >= 0 && (strings.TrimSpace(stripBraille(rows[j])) == "" || codexWeeklyNotice.MatchString(rows[j])) {
			j--
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
	queueVisible := strings.HasPrefix(last, "Press up to edit queued messages")
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
	if draft == "Auto mode: edits & safe commands auto-approved (shift+tab to cycle)" {
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
