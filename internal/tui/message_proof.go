package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const minimumProofPrefix = 16 // Unicode characters after collapsing whitespace.

type proofMatch uint8

const (
	proofNone proofMatch = iota
	proofPrefix
	proofFull
)

// The draft is checked separately, with its literal bytes. For evidence after
// submission, keep both forms: panes may display Markdown source or render it.
type messageProof struct{ literal, rendered string }

func newMessageProof(body string) messageProof {
	return messageProof{collapseSpace(body), collapseSpace(markdownDisplay(body))}
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

var proofMarkdown = goldmark.New(goldmark.WithExtensions(extension.Strikethrough)).Parser()

func markdownDisplay(s string) string {
	if !strings.ContainsAny(s, "`*_[~#!<>\\-") {
		return s
	}
	source := []byte(s)
	doc := proofMarkdown.Parse(text.NewReader(source))
	var out strings.Builder
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if n.Type() == ast.TypeBlock && out.Len() > 0 {
			out.WriteByte('\n')
		}
		switch v := n.(type) {
		case *ast.Text:
			out.Write(util.UnescapePunctuations(v.Segment.Value(source)))
			if v.SoftLineBreak() || v.HardLineBreak() {
				out.WriteByte('\n')
			}
		case *ast.String:
			out.Write(v.Value)
		case *ast.CodeSpan:
			out.Write(v.Text(source))
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			out.Write(v.Label(source))
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.HTMLBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				out.Write(line.Value(source))
			}
		case *ast.RawHTML:
			for i := 0; i < v.Segments.Len(); i++ {
				line := v.Segments.At(i)
				out.Write(line.Value(source))
			}
		}
		return ast.WalkContinue, nil
	})
	return out.String()
}

func cutPreview(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if prefix, ok := strings.CutSuffix(s, "..."); ok {
		return strings.TrimSpace(prefix), true
	}
	if prefix, ok := strings.CutSuffix(s, "…"); ok {
		return strings.TrimSpace(prefix), true
	}
	return s, false
}

// A terminal wrap may split a word or fall on whitespace. Consume every
// displayed row, including punctuation; only the undisplayed suffix may vary.
func matchProofRows(want, shown string, partial bool) proofMatch {
	rest := want
	for line := range strings.SplitSeq(shown, "\n") {
		part := collapseSpace(line)
		if part == "" {
			continue
		}
		var ok bool
		rest, ok = strings.CutPrefix(strings.TrimLeft(rest, " "), part)
		if !ok {
			return proofNone
		}
	}
	if rest == want {
		return proofNone
	}
	if rest == "" {
		return proofFull
	}
	if partial && utf8.RuneCountInString(want[:len(want)-len(rest)]) >= minimumProofPrefix {
		return proofPrefix
	}
	return proofNone
}

func (p messageProof) compare(shown string, partial bool) proofMatch {
	result := matchProofRows(p.literal, shown, partial)
	if result != proofFull && p.literal != p.rendered {
		result = max(result, matchProofRows(p.rendered, shown, partial))
	}
	return result
}

func (p messageProof) match(shown string, partial bool) proofMatch {
	// An ellipsis can be literal input. A complete exact match wins; otherwise
	// it is display furniture and contributes no characters to the minimum.
	visible, clipped := cutPreview(shown)
	if clipped {
		if p.compare(shown, false) == proofFull {
			return proofFull
		}
		if !partial {
			return proofNone
		}
	}
	result := p.compare(visible, partial)
	if result == proofFull && !clipped {
		return result
	}
	display := markdownDisplay(visible)
	if display != visible {
		result = max(result, p.compare(display, partial))
	}
	if result == proofNone || (result == proofFull && !clipped) {
		return result
	}
	if utf8.RuneCountInString(collapseSpace(display)) < minimumProofPrefix {
		return proofNone
	}
	return proofPrefix
}

func sameQueuePreview(old, now string) bool {
	if old == now {
		return true
	}
	a, cutA := cutPreview(old)
	b, cutB := cutPreview(now)
	return newMessageProof(b).match(a, cutA) != proofNone || newMessageProof(a).match(b, cutB) != proofNone
}

func (p messageProof) userTurn(r agentReader, rows []string) bool {
	glyph := "›"
	if r == agyReader {
		glyph = ">"
	}
	return p.visibleIndex(rows, glyph, false) != -1
}

func (p messageProof) queueIndex(r agentReader, rows []string) int {
	glyph := "↳"
	if r == agyReader {
		glyph = "▸"
	}
	return p.visibleIndex(rows, glyph, true)
}

// Return -1 when absent, -2 when several queue items could be the sender's.
// A pre-existing ambiguous match still bars a fresh confirmation.
func (p messageProof) visibleIndex(rows []string, glyph string, queue bool) int {
	index, found := 0, -1
	for i, row := range rows {
		if queue {
			row = strings.TrimSpace(row)
		}
		first, ok := strings.CutPrefix(row, glyph+" ")
		if !ok {
			continue
		}
		var shown strings.Builder
		shown.WriteString(first)
		for _, line := range rows[i+1:] {
			part := strings.TrimSpace(line)
			if part == "" {
				if p.match(shown.String(), false) == proofFull {
					break
				}
				continue
			}
			if !strings.HasPrefix(line, " ") || strings.HasPrefix(part, "↳ ") || strings.HasPrefix(part, "▸ ") || strings.HasPrefix(part, "⇧←") {
				break
			}
			shown.WriteByte('\n')
			shown.WriteString(part)
		}
		if p.match(shown.String(), true) != proofNone {
			if !queue {
				return index
			}
			if found >= 0 {
				return -2
			}
			found = index
		}
		index++
	}
	return found
}
