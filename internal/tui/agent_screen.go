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
		for j >= 0 && strings.TrimSpace(stripBraille(rows[j])) == "" {
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
	if strings.Contains(last, "task(s)") {
		return agentFrame{busy: true}
	}
	queueVisible := strings.HasPrefix(last, "Press up to edit queued messages")
	busy := strings.HasPrefix(last, "esc to cancel") || queueVisible
	if !busy && !strings.HasPrefix(last, "? for shortcuts") && !strings.HasPrefix(last, "auto ·") && !strings.HasPrefix(last, "plan ·") && !strings.HasPrefix(last, "accept-edits ·") {
		return agentFrame{}
	}
	if end < 2 || !agyRule(rows[end]) {
		return agentFrame{}
	}
	top := end - 1
	for top >= 0 && !agyRule(rows[top]) {
		top--
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

// userTurnMatcher compares only displayed user turns. Prepare the rendered
// form once, so the pre-submit checks and every proof sample agree.
func (r agentReader) userTurnMatcher(body string) func([]string) bool {
	glyph, rendered := "›", body
	if r == agyReader {
		glyph = ">"
	} else if r == codexReader && strings.Contains(body, "`") {
		rendered = codexInlineCode(body)
	}
	return func(rows []string) bool {
		return visibleMessage(rows, glyph, body) >= 0 ||
			(rendered != body && visibleMessage(rows, glyph, rendered) >= 0)
	}
}

var backtickRuns = regexp.MustCompile("`+")

// Codex 0.170 renders inline code in user turns but keeps the typed bytes in
// the composer and native queue. Only project paired inline delimiters. Leave
// block markup and ambiguous escapes to the literal comparison.
func codexInlineCode(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if len(line)-len(trimmed) >= 4 || strings.HasPrefix(line, "\t") || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			return body
		}
		runs := backtickRuns.FindAllStringIndex(line, -1)
		// A closer is the next maximal run of the same length. Indexing it
		// once keeps unmatched or differently sized runs linear in the input.
		nextSameWidth := make([]int, len(runs))
		lastByWidth := make(map[int]int)
		for j := len(runs) - 1; j >= 0; j-- {
			width := runs[j][1] - runs[j][0]
			nextSameWidth[j] = lastByWidth[width]
			lastByWidth[width] = j
		}
		var out strings.Builder
		start := 0
		for j := 0; j < len(runs); j++ {
			open := runs[j]
			if open[0] > 0 && line[open[0]-1] == '\\' {
				return body
			}
			close := nextSameWidth[j]
			if close == 0 {
				continue
			}
			code := line[open[1]:runs[close][0]]
			if strings.HasPrefix(code, " ") && strings.HasSuffix(code, " ") && strings.Trim(code, " ") != "" {
				code = code[1 : len(code)-1]
			}
			out.WriteString(line[start:open[0]])
			out.WriteString(code)
			start, j = runs[close][1], close
		}
		if start > 0 {
			out.WriteString(line[start:])
			lines[i] = out.String()
		}
	}
	return strings.Join(lines, "\n")
}

func (r agentReader) queuedIndex(rows []string, body string) int {
	if r == agyReader {
		return visibleMessage(rows, "▸", body)
	}
	return visibleMessage(rows, "↳", body)
}

// agy renders the first line plus "..." for one multiline queued item.
// Accept this preview only for a single append to the previously seen queue;
// the full body must already have been seen in the composer.
func agyQueuedAppend(before, after agentFrame, body string) int {
	if after.native != before.native+1 || len(after.queued) != after.native || !slices.Equal(before.queued, after.queued[:before.native]) {
		return -1
	}
	row := after.queued[before.native]
	if row == "▸ "+body {
		return before.native
	}
	first, _, multiline := strings.Cut(body, "\n")
	if multiline && row == "▸ "+strings.TrimSpace(first)+"..." {
		return before.native
	}
	return -1
}

// visibleMessage compares actual text within each display row. A soft wrap
// can split a word or fall on whitespace, but spaces inside a row are data.
func visibleMessage(rows []string, glyph, body string) int {
	index := 0
	for i, row := range rows {
		if glyph == "↳" || glyph == "▸" {
			row = strings.TrimSpace(row)
		}
		first, ok := strings.CutPrefix(row, glyph+" ")
		if !ok {
			continue
		}
		lines := []string{first}
		for _, line := range rows[i+1:] {
			if strings.TrimSpace(line) == "" {
				if matchesDisplayRows(lines, body) {
					return index
				}
				continue
			}
			if !strings.HasPrefix(line, " ") || strings.HasPrefix(strings.TrimSpace(line), "↳ ") || strings.HasPrefix(strings.TrimSpace(line), "⇧←") {
				break
			}
			lines = append(lines, line)
		}
		if matchesDisplayRows(lines, body) {
			return index
		}
		index++
	}
	return -1
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
