package tui

// strip_store.go keeps each agent's list strip (initech strip) across a
// restart of the pane and of the rig (D3). Each process that owns panes keeps
// its own file, never two writers to one file: window 1 writes strip.yaml and
// a serve daemon strip-serve.yaml, both in .initech.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The strip files, one per process that owns panes.
const (
	stripFileWindowOne = "strip.yaml"
	stripFileServe     = "strip-serve.yaml"
)

// stripFileVersion is the file's format version; another version is set
// aside, never read as this one.
const stripFileVersion = 1

// The limits an item list is checked against before anything changes (D2).
const (
	StripMaxItems = 10
	StripMaxChars = 300
)

// CheckStripItems trims each item's ends and checks the list against D2: at
// most StripMaxItems items, each at most StripMaxChars characters, no tab,
// newline or other control character. Empty items after trimming are
// dropped. It returns the list to store, or one line saying what is wrong.
func CheckStripItems(items []string) ([]string, error) {
	var out []string
	for _, it := range items {
		for _, r := range it {
			if r < 0x20 || r == 0x7f {
				return nil, fmt.Errorf("an item contains a tab, newline or other control character; one item per argument")
			}
		}
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if n := utf8.RuneCountInString(it); n > StripMaxChars {
			return nil, fmt.Errorf("an item is %d characters; the limit is %d", n, StripMaxChars)
		}
		out = append(out, it)
	}
	if len(out) > StripMaxItems {
		return nil, fmt.Errorf("%d items; the limit is %d", len(out), StripMaxItems)
	}
	return out, nil
}

// stripFileBody is the file as written.
type stripFileBody struct {
	Version int                 `yaml:"version"`
	Agents  map[string][]string `yaml:"agents"`
}

// errStripFileBad is a strip file that does not parse or has another version.
var errStripFileBad = errors.New("strip file does not parse or has another version")

// stripBook is one process's strips, by agent, and the file it keeps them in.
type stripBook struct {
	mu    sync.Mutex
	path  string
	lists map[string][]string
	// onChange, when set, is told the agent whose list changed (viewers).
	onChange func(agent string, items []string)
}

// processStrips is the book of the process that owns panes: NewPane reads an
// agent's list from it, so a restarted or added pane comes back with its
// strip. nil (tests, a viewer) means no strips.
var (
	processStripsMu sync.Mutex
	processStrips   *stripBook
)

func setProcessStrips(b *stripBook) {
	processStripsMu.Lock()
	processStrips = b
	processStripsMu.Unlock()
}

// processStripItems is agent's list in this process's book; nil when none.
func processStripItems(agent string) []string {
	processStripsMu.Lock()
	b := processStrips
	processStripsMu.Unlock()
	if b == nil {
		return nil
	}
	return b.get(agent)
}

// loadStripBook reads path. A missing or empty file is an empty book. A file
// that does not parse, or has another version, is renamed aside with one log
// line and the book starts empty: it is never half-read.
func loadStripBook(path string, now time.Time) *stripBook {
	b := &stripBook{path: path, lists: map[string][]string{}}
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return b
	}
	lists, err := parseStripFile(data)
	if err != nil {
		aside := path + ".bad-" + now.UTC().Format("20060102T150405Z")
		rerr := os.Rename(path, aside)
		LogWarn("strip", "strip file set aside; no pane shows a strip until one is set again",
			"file", path, "aside", aside, "err", err, "rename_err", rerr)
		return b
	}
	b.lists = lists
	return b
}

// parseStripFile reads a whole file, or nothing.
func parseStripFile(data []byte) (map[string][]string, error) {
	var body stripFileBody
	if err := yaml.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("%w: %v", errStripFileBad, err)
	}
	if body.Version != stripFileVersion {
		return nil, fmt.Errorf("%w: version %d", errStripFileBad, body.Version)
	}
	lists := map[string][]string{}
	for agent, items := range body.Agents {
		checked, err := CheckStripItems(items)
		if err != nil {
			return nil, fmt.Errorf("%w: agent %s: %v", errStripFileBad, agent, err)
		}
		if len(checked) > 0 {
			lists[agent] = checked
		}
	}
	return lists, nil
}

func (b *stripBook) get(agent string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.lists[agent]...)
}

// set replaces agent's list (nil or empty removes it) and writes the file
// whole. The book changes only when the file was written.
func (b *stripBook) set(agent string, items []string) error {
	b.mu.Lock()
	next := make(map[string][]string, len(b.lists)+1)
	for a, l := range b.lists {
		next[a] = l
	}
	if len(items) == 0 {
		delete(next, agent)
	} else {
		next[agent] = append([]string(nil), items...)
	}
	if err := writeStripFile(b.path, next); err != nil {
		b.mu.Unlock()
		return err
	}
	b.lists = next
	onChange := b.onChange
	b.mu.Unlock()
	if onChange != nil {
		onChange(agent, items)
	}
	return nil
}

// all is every agent with a list, sorted, for a viewer that connects.
func (b *stripBook) all() map[string][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string][]string, len(b.lists))
	for a, l := range b.lists {
		out[a] = append([]string(nil), l...)
	}
	return out
}

// writeStripFile writes lists whole, mode 0600, write-then-rename. With no
// lists left the file is removed, so a rig that never used a strip has none.
func writeStripFile(path string, lists map[string][]string) error {
	if len(lists) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	body := stripFileBody{Version: stripFileVersion, Agents: lists}
	data, err := yaml.Marshal(body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}
