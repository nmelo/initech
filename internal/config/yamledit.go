package config

// yamledit.go changes an existing initech.yaml by editing its text, never by
// re-encoding it. Operators annotate this file: comments, blank lines, their
// own key order, commented-out examples. A yaml.Node re-encode drops blank
// lines and re-indents, so the only way to keep every line a caller did not
// change is to splice the change into the original bytes.
//
// Three trees drive an edit. OLD is the file's own node tree, used only for
// line and column positions. BASE is what the file decodes to, re-encoded;
// NEW is the caller's Project, encoded. Only BASE-vs-NEW differences become
// edits, so a key the Project type does not know, or a value spelled
// differently from how initech would write it (root: ~/x), is never touched.
//
// Edits:
//   - a changed single-line scalar replaces only its value text (an
//     end-of-line comment stays);
//   - a new key is inserted after the last content line of its mapping, at
//     the mapping's indent (above any trailing comments);
//   - a removed key or block-list item deletes from its first line to its
//     last content line (comments and blank lines before the next sibling
//     stay);
//   - a list item added goes after the item it follows;
//   - anything else (a flow-style list, a value changing kind, a multi-line
//     scalar) re-renders only that one key's entry.
//
// After splicing, the result must decode to exactly the caller's Project; if
// it does not, the edit is refused with the key and line, never the value,
// and the file is left alone. There is no whole-file fallback.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Update re-reads the config at path under an exclusive lock, applies mutate
// to what the file holds now, and writes back only the keys mutate changed.
// Callers that hold a Project across a prompt, a clone or a whole TUI session
// use Update rather than Write, so a change made to the file in the meantime
// (by hand or by another initech process) is never reverted.
func Update(path string, mutate func(*Project) error) error {
	return withConfigLock(path, func(target string) error {
		src, err := os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		p, err := decodeProject(src)
		if err != nil {
			return err
		}
		if updateReadHook != nil {
			updateReadHook()
		}
		if err := mutate(p); err != nil {
			return err
		}
		return writeEdited(target, src, p)
	})
}

// updateReadHook runs between Update's read and its write. Tests use it to
// hold the race window open; it is nil in production.
var updateReadHook func()

// decodeProject parses initech.yaml bytes the way Load does (root's ~
// expanded) without validating. Parse errors are scrubbed of values.
func decodeProject(src []byte) (*Project, error) {
	var p Project
	if err := yaml.Unmarshal(src, &p); err != nil {
		return nil, fmt.Errorf("parse config: %s", scrubYAMLError(err))
	}
	p.Root = expandHome(p.Root)
	return &p, nil
}

// yamlQuotedValue matches the `value` a yaml.v3 type error quotes.
var yamlQuotedValue = regexp.MustCompile("`[^`]*`")

// scrubYAMLError returns err's text with any quoted value replaced, so a
// token in a mistyped field never reaches an error message or a log.
func scrubYAMLError(err error) string {
	return yamlQuotedValue.ReplaceAllString(err.Error(), "<value>")
}

// writeEdited splices the difference between what src decodes to and p into
// src and replaces the file at target with the result. No difference means
// no write (beyond tightening a loose mode).
func writeEdited(target string, src []byte, p *Project) error {
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm() & 0o600
	if mode == 0 {
		mode = 0o600
	}
	out, err := editConfigYAML(src, p)
	if err != nil {
		return err
	}
	if bytes.Equal(out, src) {
		if info.Mode().Perm() != mode {
			return os.Chmod(target, mode)
		}
		return nil
	}
	return replaceFile(target, out, mode)
}

// replaceFile writes data to a temp file beside target and renames it over
// target, so a reader never sees a half-written config.
func replaceFile(target string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".initech.yaml.tmp-*")
	if err != nil {
		return fmt.Errorf("write initech.yaml: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write initech.yaml: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write initech.yaml: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write initech.yaml: %w", err)
	}
	return nil
}

// ── locking ─────────────────────────────────────────────────────────

// configLockStaleTTL clears a lock left by a crashed writer. A config edit
// takes milliseconds, so this is generous.
const configLockStaleTTL = 30 * time.Second

// configLockWait bounds how long a writer waits for another one.
var configLockWait = 10 * time.Second

// withConfigLock runs fn with an exclusive lock on the config at path, keyed
// by the file the path resolves to (a symlinked initech.yaml is edited in
// place at its target). The lock is a create-exclusive file beside that
// target, in the operator's own directory, so it works on every platform and
// no other user can hold it (a lock in a shared temp dir could be created
// first by anyone, blocking every write). It exists only while a write runs.
func withConfigLock(path string, fn func(target string) error) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		target = path
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	lock := configLockPath(target)
	deadline := time.Now().Add(configLockWait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			break
		}
		if !os.IsExist(err) {
			return fmt.Errorf("create config lock: %w", err)
		}
		if fi, statErr := os.Stat(lock); statErr == nil && time.Since(fi.ModTime()) > configLockStaleTTL {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting to write %s (another initech process is writing it)", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer os.Remove(lock)
	return fn(target)
}

// configLockPath is the lock file for the config at target: beside it.
func configLockPath(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".lock")
}

// ── the edit ────────────────────────────────────────────────────────

// errNotEditable marks a refusal; the message names the key and line only.
var errNotEditable = errors.New("initech.yaml left unchanged")

// textEdit replaces src[start:end] with text (start == end inserts).
type textEdit struct {
	start, end int
	text       string
	key        string // dotted key path, for a refusal
	line       int    // 1-based line in src
}

// yamlEditor holds the original bytes, their line offsets and the edits
// collected so far.
type yamlEditor struct {
	src    []byte
	starts []int // starts[i] = byte offset of line i+1; last entry = len(src)
	indent int   // indent step for rendered blocks
	edits  []textEdit
}

// editConfigYAML returns src with the difference between what src decodes to
// and p spliced in. It refuses (src unchanged) when src does not parse, is
// not a mapping, or the result would not decode to exactly p.
func editConfigYAML(src []byte, p *Project) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %s", scrubYAMLError(err))
	}
	base, err := decodeProject(src)
	if err != nil {
		return nil, err
	}
	bn, err := encodeNode(base)
	if err != nil {
		return nil, err
	}
	nn, err := encodeNode(p)
	if err != nil {
		return nil, err
	}

	e := &yamlEditor{src: src, indent: 4}
	e.starts = lineStarts(src)

	var root *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	switch {
	case root == nil:
		// Empty or comments-only: everything is new, appended at the end.
		if !nodesEqual(bn, nn) {
			for i := 0; i+1 < len(nn.Content); i += 2 {
				e.insert(len(src), nn.Content[i], nn.Content[i+1], "", nn.Content[i].Value, e.lineCount())
			}
		}
	case root.Kind != yaml.MappingNode:
		return nil, fmt.Errorf("%w: the top level is not a list of keys (line %d)", errNotEditable, root.Line)
	default:
		e.indent = detectIndent(root, 4)
		e.diffMapping(root, e.lineCount()+1, bn, nn, "")
	}
	if len(e.edits) == 0 && nodesEqual(bn, nn) {
		return src, nil
	}

	out, err := e.apply()
	if err != nil {
		return nil, err
	}
	if err := verifyEdit(out, nn, &doc, e.edits); err != nil {
		return nil, err
	}
	return out, nil
}

// verifyEdit is the guard: out must decode to exactly the wanted tree.
func verifyEdit(out []byte, want *yaml.Node, old *yaml.Node, edits []textEdit) error {
	got, err := decodeProject(out)
	if err != nil && len(edits) > 0 {
		ed := edits[0]
		return fmt.Errorf("%w: could not edit %s (line %d) safely; make this change by hand", errNotEditable, ed.key, ed.line)
	}
	if err != nil {
		return err
	}
	gn, err := encodeNode(got)
	if err != nil {
		return err
	}
	if nodesEqual(gn, want) {
		return nil
	}
	path := diffPath(gn, want, "")
	return fmt.Errorf("%w: could not edit %s (line %d) safely; make this change by hand", errNotEditable, path, lineOfPath(old, path))
}

// encodeNode encodes v the way Marshal would, as a node tree.
func encodeNode(v interface{}) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return &n, nil
}

// diffMapping records the edits that turn OLD mapping om (whose entries end
// before line regionEnd) from BASE bm into NEW nm.
func (e *yamlEditor) diffMapping(om *yaml.Node, regionEnd int, bm, nm *yaml.Node, prefix string) {
	var keys []string
	seen := map[string]bool{}
	for _, m := range []*yaml.Node{nm, bm} {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if k := m.Content[i].Value; !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	for _, k := range keys {
		path := joinKey(prefix, k)
		oi := mapIndex(om, k)
		bv, nv := mapValue(bm, k), mapValue(nm, k)
		if nodesEqual(bv, nv) {
			continue
		}
		switch {
		case nv == nil:
			if oi >= 0 {
				e.remove(om, oi, regionEnd, path)
			}
		case oi < 0:
			last := om.Content[len(om.Content)-2]
			at := e.contentEnd(last.Line, regionEnd)
			e.insert(at, mapKey(nm, k), nv, e.indentOf(om), path, last.Line)
		case bv == nil:
			e.replaceEntry(om, oi, regionEnd, mapKey(nm, k), nv, path)
		default:
			e.change(om, oi, regionEnd, bv, mapKey(nm, k), nv, path)
		}
	}
}

// change edits entry oi of om whose value differs between BASE and NEW.
func (e *yamlEditor) change(om *yaml.Node, oi, regionEnd int, bv, nk, nv *yaml.Node, path string) {
	ok, ov := om.Content[oi], om.Content[oi+1]
	end := e.entryEnd(om, oi, regionEnd)
	block := ov.Style&yaml.FlowStyle == 0 && ov.Line > ok.Line
	switch {
	case ov.Kind == yaml.ScalarNode && nv.Kind == yaml.ScalarNode:
		if e.replaceScalar(ok, ov, nv, end, path) {
			return
		}
	case ov.Kind == yaml.MappingNode && bv.Kind == yaml.MappingNode && nv.Kind == yaml.MappingNode && block && len(ov.Content) > 0:
		e.diffMapping(ov, e.lineFrom(end), bv, nv, path)
		return
	case ov.Kind == yaml.SequenceNode && bv.Kind == yaml.SequenceNode && nv.Kind == yaml.SequenceNode && block:
		if e.diffScalarList(ov, end, bv, nv, path) {
			return
		}
	}
	e.replaceEntry(om, oi, regionEnd, nk, nv, path)
}

// replaceScalar swaps an inline single-line scalar's text for nv's,
// keeping whatever follows it on the line. It reports false when the
// scalar is not that simple.
func (e *yamlEditor) replaceScalar(ok, ov, nv *yaml.Node, end int, path string) bool {
	if ov.Line != ok.Line || ov.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return false
	}
	if end > e.lineStart(ov.Line+1) {
		return false // continues on the next line
	}
	text, ok2 := renderScalar(nv)
	if !ok2 {
		return false
	}
	start := e.offset(ov.Line, ov.Column)
	stop, ok3 := scalarEnd(e.src[start:e.lineEndNoNL(ov.Line)], ov.Style)
	if !ok3 {
		return false
	}
	e.edits = append(e.edits, textEdit{start: start, end: start + stop, text: text, key: path, line: ov.Line})
	return true
}

// diffScalarList edits a block list of scalars item by item (a longest
// common subsequence keeps every unchanged item and its comments). It
// reports false when the list is not a simple block list of scalars.
func (e *yamlEditor) diffScalarList(ov *yaml.Node, end int, bv, nv *yaml.Node, path string) bool {
	if len(ov.Content) == 0 || len(ov.Content) != len(bv.Content) {
		return false
	}
	for _, list := range [][]*yaml.Node{ov.Content, bv.Content, nv.Content} {
		for _, it := range list {
			if it.Kind != yaml.ScalarNode {
				return false
			}
		}
	}
	items := ov.Content
	itemEnd := func(i int) int {
		next := end
		if i+1 < len(items) {
			next = e.lineStart(items[i+1].Line)
		}
		return e.contentEndBefore(items[i].Line, next)
	}
	for i, it := range items {
		if it.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.FlowStyle) != 0 || itemEnd(i) > e.lineStart(it.Line+1) {
			return false
		}
		if i > 0 && it.Line == items[i-1].Line {
			return false
		}
	}
	first := e.src[e.lineStart(items[0].Line):e.lineEndNoNL(items[0].Line)]
	dash := bytes.IndexByte(first, '-')
	if dash < 0 || strings.TrimSpace(string(first[:dash])) != "" {
		return false
	}
	prefix := string(first[:dash])

	b, n := bv.Content, nv.Content
	lcs := make([][]int, len(b)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(n)+1)
	}
	for i := len(b) - 1; i >= 0; i-- {
		for j := len(n) - 1; j >= 0; j-- {
			if nodesEqual(b[i], n[j]) {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var edits []textEdit
	insertAt := func(i int) (int, int) { // offset and line for an insert before item i
		if i == 0 {
			return e.lineStart(items[0].Line), items[0].Line
		}
		return itemEnd(i - 1), items[i-1].Line
	}
	i, j := 0, 0
	for i < len(b) || j < len(n) {
		switch {
		case i < len(b) && j < len(n) && nodesEqual(b[i], n[j]):
			i, j = i+1, j+1
		case j < len(n) && (i == len(b) || lcs[i][j+1] >= lcs[i+1][j]):
			text, ok := renderScalar(n[j])
			if !ok {
				return false
			}
			at, line := insertAt(i)
			edits = append(edits, textEdit{start: at, end: at, text: e.nl(at) + prefix + "- " + text + "\n", key: path, line: line})
			j++
		default:
			edits = append(edits, textEdit{start: e.lineStart(items[i].Line), end: itemEnd(i), key: path, line: items[i].Line})
			i++
		}
	}
	e.edits = append(e.edits, edits...)
	return true
}

// remove deletes entry oi of om: from its key's line to its last content
// line. Comments above the next key stay.
func (e *yamlEditor) remove(om *yaml.Node, oi, regionEnd int, path string) {
	k := om.Content[oi]
	e.edits = append(e.edits, textEdit{start: e.lineStart(k.Line), end: e.entryEnd(om, oi, regionEnd), key: path, line: k.Line})
}

// replaceEntry re-renders entry oi of om from NEW.
func (e *yamlEditor) replaceEntry(om *yaml.Node, oi, regionEnd int, nk, nv *yaml.Node, path string) {
	k := om.Content[oi]
	start := e.lineStart(k.Line)
	text := renderEntry(nk, nv, e.indentOf(om), e.indent)
	if k.Column-1 != len(e.indentOf(om)) || strings.TrimSpace(string(e.src[start:e.offset(k.Line, k.Column)])) != "" {
		text = "\x00" // key does not start its line: let the guard refuse
	}
	e.edits = append(e.edits, textEdit{start: start, end: e.entryEnd(om, oi, regionEnd), text: text, key: path, line: k.Line})
}

// insert adds "key: value" at offset at, indented by ind.
func (e *yamlEditor) insert(at int, nk, nv *yaml.Node, ind, path string, line int) {
	e.edits = append(e.edits, textEdit{start: at, end: at, text: e.nl(at) + renderEntry(nk, nv, ind, e.indent), key: path, line: line})
}

// nl returns "\n" when inserting at the end of a file that lacks one.
func (e *yamlEditor) nl(at int) string {
	if at == len(e.src) && len(e.src) > 0 && e.src[len(e.src)-1] != '\n' {
		return "\n"
	}
	return ""
}

// entryEnd is the offset just past the last content line of entry oi.
func (e *yamlEditor) entryEnd(om *yaml.Node, oi, regionEnd int) int {
	next := e.lineStart(regionEnd)
	if oi+2 < len(om.Content) {
		next = e.lineStart(om.Content[oi+2].Line)
	}
	return e.contentEndBefore(om.Content[oi].Line, next)
}

// contentEnd is entryEnd for the last entry of a region ending at line
// regionEnd (exclusive).
func (e *yamlEditor) contentEnd(fromLine, regionEnd int) int {
	return e.contentEndBefore(fromLine, e.lineStart(regionEnd))
}

// contentEndBefore walks back from offset next over blank and comment-only
// lines (they belong to what follows) and returns the offset just past the
// last content line at or after fromLine.
func (e *yamlEditor) contentEndBefore(fromLine, next int) int {
	line := e.lineAt(next) - 1
	if next == len(e.src) {
		line = e.lineCount()
	}
	for line > fromLine {
		t := strings.TrimSpace(string(e.src[e.lineStart(line):e.lineEndNoNL(line)]))
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		line--
	}
	return e.lineStart(line + 1)
}

// indentOf is the whitespace before om's first key.
func (e *yamlEditor) indentOf(om *yaml.Node) string {
	return strings.Repeat(" ", om.Content[0].Column-1)
}

// apply splices the edits into src, in order of position.
func (e *yamlEditor) apply() ([]byte, error) {
	sort.SliceStable(e.edits, func(i, j int) bool { return e.edits[i].start < e.edits[j].start })
	var out bytes.Buffer
	cursor := 0
	for _, ed := range e.edits {
		if ed.start < cursor || ed.end < ed.start {
			return nil, fmt.Errorf("%w: could not edit %s (line %d) safely; make this change by hand", errNotEditable, ed.key, ed.line)
		}
		out.Write(e.src[cursor:ed.start])
		out.WriteString(ed.text)
		cursor = ed.end
	}
	out.Write(e.src[cursor:])
	return out.Bytes(), nil
}

// ── line arithmetic ─────────────────────────────────────────────────

func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}
	return append(starts, len(src))
}

// lineCount is the number of lines in src.
func (e *yamlEditor) lineCount() int {
	if len(e.src) == 0 {
		return 0
	}
	return len(e.starts) - 1
}

// lineStart is the offset of 1-based line n (len(src) past the end).
func (e *yamlEditor) lineStart(n int) int {
	if n < 1 {
		return 0
	}
	if n > e.lineCount() {
		return len(e.src)
	}
	return e.starts[n-1]
}

// lineEndNoNL is the offset of line n's newline (or end of file).
func (e *yamlEditor) lineEndNoNL(n int) int {
	end := e.lineStart(n + 1)
	if end > e.lineStart(n) && e.src[end-1] == '\n' {
		end--
	}
	return end
}

// lineFrom is the 1-based line that starts at offset off (one past the last
// line at the end of the file).
func (e *yamlEditor) lineFrom(off int) int {
	if off >= len(e.src) {
		return e.lineCount() + 1
	}
	return e.lineAt(off)
}

// lineAt is the 1-based line containing offset off.
func (e *yamlEditor) lineAt(off int) int {
	return sort.Search(e.lineCount(), func(i int) bool { return e.starts[i+1] > off }) + 1
}

// offset converts a 1-based line and column (in characters) to a byte
// offset.
func (e *yamlEditor) offset(line, col int) int {
	start := e.lineStart(line)
	runes := []rune(string(e.src[start:e.lineEndNoNL(line)]))
	if col-1 > len(runes) {
		col = len(runes) + 1
	}
	return start + len(string(runes[:col-1]))
}

// scalarEnd returns the length of the scalar token at the start of rest:
// up to the closing quote for quoted styles, otherwise up to an end-of-line
// comment or the end of the line, trailing space excluded.
func scalarEnd(rest []byte, style yaml.Style) (int, bool) {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\\' {
				i++
			} else if rest[i] == '"' {
				return i + 1, true
			}
		}
		return 0, false
	case style&yaml.SingleQuotedStyle != 0:
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\'' {
				if i+1 < len(rest) && rest[i+1] == '\'' {
					i++
					continue
				}
				return i + 1, true
			}
		}
		return 0, false
	}
	end := len(rest)
	for i := 1; i < len(rest); i++ {
		if rest[i] == '#' && (rest[i-1] == ' ' || rest[i-1] == '\t') {
			end = i
			break
		}
	}
	return len(bytes.TrimRight(rest[:end], " \t")), true
}

// ── rendering and comparing ─────────────────────────────────────────

// renderScalar is a scalar's single-line YAML text; false if it needs more
// than one line.
func renderScalar(n *yaml.Node) (string, bool) {
	c := *n
	c.Style &^= yaml.LiteralStyle | yaml.FoldedStyle
	b, err := yaml.Marshal(&c)
	if err != nil {
		return "", false
	}
	s := strings.TrimSuffix(string(b), "\n")
	if strings.Contains(s, "\n") {
		return "", false
	}
	return s, true
}

// renderEntry renders "key: value" as block YAML, every line prefixed by ind.
func renderEntry(k, v *yaml.Node, ind string, step int) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(step)
	_ = enc.Encode(&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{k, v}})
	_ = enc.Close()
	lines := strings.SplitAfter(buf.String(), "\n")
	var out strings.Builder
	for _, l := range lines {
		if l == "" {
			continue
		}
		if strings.TrimSpace(l) != "" {
			out.WriteString(ind)
		}
		out.WriteString(l)
	}
	return out.String()
}

// detectIndent is the file's indent step: the first nested block mapping's
// key column minus its parent's.
func detectIndent(m *yaml.Node, def int) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0 && v.Line > k.Line {
			if d := v.Content[0].Column - k.Column; d > 0 {
				return d
			}
		}
	}
	return def
}

// nodesEqual compares two encoded trees by kind, tag and value.
func nodesEqual(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind == yaml.DocumentNode && len(a.Content) == 1 {
		a = a.Content[0]
	}
	if b.Kind == yaml.DocumentNode && len(b.Content) == 1 {
		b = b.Content[0]
	}
	if a.Kind != b.Kind || len(a.Content) != len(b.Content) {
		return false
	}
	if a.Kind == yaml.ScalarNode && (a.Value != b.Value || a.ShortTag() != b.ShortTag()) {
		return false
	}
	for i := range a.Content {
		if !nodesEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// diffPath is the dotted path of the first difference between two encoded
// mappings.
func diffPath(a, b *yaml.Node, prefix string) string {
	if a.Kind == yaml.DocumentNode && len(a.Content) == 1 {
		a = a.Content[0]
	}
	if b.Kind == yaml.DocumentNode && len(b.Content) == 1 {
		b = b.Content[0]
	}
	if a.Kind != yaml.MappingNode || b.Kind != yaml.MappingNode {
		return prefix
	}
	for _, m := range []*yaml.Node{b, a} {
		for i := 0; i+1 < len(m.Content); i += 2 {
			k := m.Content[i].Value
			av, bv := mapValue(a, k), mapValue(b, k)
			if !nodesEqual(av, bv) {
				if av != nil && bv != nil {
					return diffPath(av, bv, joinKey(prefix, k))
				}
				return joinKey(prefix, k)
			}
		}
	}
	return prefix
}

// lineOfPath is the line of the deepest key on path that the old file has.
func lineOfPath(doc *yaml.Node, path string) int {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	line := 1
	for _, k := range strings.Split(path, ".") {
		if n == nil || n.Kind != yaml.MappingNode {
			break
		}
		i := mapIndex(n, k)
		if i < 0 {
			break
		}
		line = n.Content[i].Line
		n = n.Content[i+1]
	}
	return line
}

func joinKey(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// mapIndex is the index of key k in mapping m's Content, or -1.
func mapIndex(m *yaml.Node, k string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return i
		}
	}
	return -1
}

func mapKey(m *yaml.Node, k string) *yaml.Node {
	if i := mapIndex(unwrapDoc(m), k); i >= 0 {
		return unwrapDoc(m).Content[i]
	}
	return nil
}

func mapValue(m *yaml.Node, k string) *yaml.Node {
	if i := mapIndex(unwrapDoc(m), k); i >= 0 {
		return unwrapDoc(m).Content[i+1]
	}
	return nil
}

func unwrapDoc(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0]
	}
	return n
}
