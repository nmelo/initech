package tui

// held_store.go keeps held mail across a restart of the process that holds it
// (ini-8rkg). A message told "held" (do-not-disturb, a suspended agent, an
// open dialog) lived only in its pane's in-memory queue, so a window 1 restart
// lost it and nobody heard. Each process that owns pane queues now writes its
// own file on every change to a queue, and reads it back at start.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// heldFileVersion is the held file's format version; another version is set
// aside, never read as this one.
const heldFileVersion = 1

// The held files, one per process that owns pane queues, never two writers to
// one file: window 1 and the serve daemon.
const (
	heldFileWindowOne = "held.yaml"
	heldFileServe     = "held-serve.yaml"
)

// Held entry kinds and states, as written.
const (
	heldKindDND       = "dnd"
	heldKindSuspended = "suspended"
	heldKindDialog    = "dialog"
	heldStateHeld     = "held"
	heldStateTyping   = "typing"
)

// heldEntry is one held message on disk.
type heldEntry struct {
	Pane     string     `yaml:"pane"`
	Text     string     `yaml:"text"`
	Enter    bool       `yaml:"enter"`
	Time     time.Time  `yaml:"time"`
	Kind     string     `yaml:"kind"`
	Released *time.Time `yaml:"released,omitempty"`
	From     string     `yaml:"from,omitempty"`
	State    string     `yaml:"state"`
}

type heldFileBody struct {
	Version int         `yaml:"version"`
	Held    []heldEntry `yaml:"held"`
}

// heldKindOf names why a queued message is held.
func heldKindOf(m QueuedMessage) string {
	switch {
	case m.DND:
		return heldKindDND
	case m.HeldForDialog:
		return heldKindDialog
	}
	return heldKindSuspended
}

func heldEntryOf(pane string, m QueuedMessage, state string) heldEntry {
	e := heldEntry{Pane: pane, Text: m.Text, Enter: m.Enter, Time: m.Time, Kind: heldKindOf(m), From: m.From, State: state}
	if !m.Released.IsZero() {
		r := m.Released
		e.Released = &r
	}
	return e
}

// heldSnapshot is this pane's held mail, oldest first: a message being typed
// (the drain's in-flight one, or the resume loop's), the rest a loop has in
// hand, then the queue.
func (p *Pane) heldSnapshot() []heldEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []heldEntry
	if p.redelivering != nil {
		out = append(out, heldEntryOf(p.name, *p.redelivering, heldStateTyping))
	}
	if p.heldTyping != nil {
		out = append(out, heldEntryOf(p.name, *p.heldTyping, heldStateTyping))
	}
	for _, m := range p.heldInHand {
		out = append(out, heldEntryOf(p.name, m, heldStateHeld))
	}
	for _, m := range p.messageQueue {
		out = append(out, heldEntryOf(p.name, m, heldStateHeld))
	}
	return out
}

// heldChanged tells the pane's owner its held mail changed, so the file is
// rewritten. Call it with p.mu NOT held.
func (p *Pane) heldChanged() {
	p.mu.Lock()
	hook := p.heldHook
	p.mu.Unlock()
	if hook != nil {
		hook(p)
	}
}

// setHeldHook wires a pane to its owner's held file.
func (p *Pane) setHeldHook(w *heldWriter) {
	if w == nil {
		return
	}
	p.mu.Lock()
	p.heldHook = func(p *Pane) { w.update(p, p.heldSnapshot()) }
	p.mu.Unlock()
}

// typeInHand marks msgs as taken off the queue by a loop that types them one
// at a time (resume): they stay in the file, the current one as typing.
func (p *Pane) typeInHand(msgs []QueuedMessage) {
	p.mu.Lock()
	p.heldInHand = append([]QueuedMessage(nil), msgs...)
	p.mu.Unlock()
	p.heldChanged()
}

// typingNext marks the next message in hand as being typed.
func (p *Pane) typingNext() {
	p.mu.Lock()
	if len(p.heldInHand) > 0 {
		m := p.heldInHand[0]
		p.heldTyping = &m
		p.heldInHand = p.heldInHand[1:]
	}
	p.mu.Unlock()
	p.heldChanged()
}

// typedOne clears the typing mark once the send has its verdict.
func (p *Pane) typedOne() {
	p.mu.Lock()
	p.heldTyping = nil
	p.mu.Unlock()
	p.heldChanged()
}

// heldWriter writes one process's held file. Writes are serialized, so the
// file always holds the latest state of every pane.
type heldWriter struct {
	mu   sync.Mutex
	path string
	// panes is keyed by pane OBJECT: a replaced pane and its successor
	// share a name for a moment, and neither may overwrite the other's mail.
	panes  map[*Pane][]heldEntry
	failed bool // a write failed; exit says what could not be kept
	// ready is false until the owner has read the file back at start: before
	// that a write would replace the file it has not read (an empty pane
	// removes it), so changes are only recorded.
	ready bool
}

// start marks the file read back and writes the current state once.
func (w *heldWriter) start() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ready = true
	if err := w.writeLocked(); err != nil {
		LogWarn("held", "held mail could not be written; it survives only while this process runs", "file", w.path, "err", err)
		w.failed = true
	}
}

func newHeldWriter(root, name string) *heldWriter {
	if root == "" {
		return nil
	}
	return &heldWriter{path: filepath.Join(layoutDir(root), name), panes: map[*Pane][]heldEntry{}}
}

// update records one pane's held mail and rewrites the file.
func (w *heldWriter) update(pane *Pane, entries []heldEntry) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(entries) == 0 {
		delete(w.panes, pane)
	} else {
		w.panes[pane] = entries
	}
	if !w.ready {
		return
	}
	if err := w.writeLocked(); err != nil {
		if !w.failed {
			LogWarn("held", "held mail could not be written; it survives only while this process runs",
				"file", w.path, "panes", len(w.panes), "err", err)
		}
		w.failed = true
		return
	}
	w.failed = false
}

// all is every held entry, oldest first.
func (w *heldWriter) all() []heldEntry {
	var out []heldEntry
	for _, es := range w.panes {
		out = append(out, es...)
	}
	// Oldest first; a pane's own order breaks ties (its entries are already
	// in its order, and the sort is stable within equal times by pane).
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.Before(out[j].Time)
		}
		return out[i].Pane < out[j].Pane
	})
	return out
}

// writeLocked writes the file atomically (a temp file in the same folder,
// renamed over the old one), mode 0600; no entries removes it.
func (w *heldWriter) writeLocked() error {
	entries := w.all()
	if len(entries) == 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := yaml.Marshal(&heldFileBody{Version: heldFileVersion, Held: entries})
	if err != nil {
		return err
	}
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".held-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // after a successful rename there is nothing here
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, w.path)
}

// unkept is, after a failed write, one line per pane with held mail: what
// could not be kept. Empty when every write succeeded.
func (w *heldWriter) unkept() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.failed {
		return nil
	}
	byName := map[string][]heldEntry{}
	for _, es := range w.panes {
		for _, e := range es {
			byName[e.Pane] = append(byName[e.Pane], e)
		}
	}
	var lines []string
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		senders := map[string]bool{}
		var list []string
		for _, e := range byName[n] {
			s := e.From
			if s == "" {
				s = "unknown"
			}
			if !senders[s] {
				senders[s] = true
				list = append(list, s)
			}
		}
		lines = append(lines, fmt.Sprintf("initech: %d held message(s) to %s (from %v) could not be kept across this restart", len(byName[n]), n, list))
	}
	return lines
}

// errHeldFileBad is a held file that does not parse or has another version.
var errHeldFileBad = errors.New("held file does not parse or has another version")

// readHeldFile reads a held file. A missing file is no entries; a bad one is
// errHeldFileBad.
func readHeldFile(path string) ([]heldEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var body heldFileBody
	if err := yaml.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("%w: %v", errHeldFileBad, err)
	}
	if body.Version != heldFileVersion {
		return nil, fmt.Errorf("%w: version %d", errHeldFileBad, body.Version)
	}
	return body.Held, nil
}

// setAside renames a bad held file to <name>.bad-<time>, never deleting it.
func setAsideHeldFile(path string, now time.Time) (string, error) {
	aside := path + ".bad-" + now.UTC().Format("20060102T150405Z")
	return aside, os.Rename(path, aside)
}

// heldLines are what a start says about held mail it could not put back, to
// whom: one line per message.
type heldLine struct{ to, text string }

// requeueHeld puts the held entries read at start back on their panes
// (ini-8rkg), each pane's in arrival order ahead of anything new, and returns
// the lines owed to senders. panes finds a live local pane by name.
//
// Do-not-disturb does not survive a restart, so dnd mail goes back released,
// as by dnd off; dialog mail waits for a dialog with its deadline counted from
// now; suspended mail stays queued. An entry marked typing is never typed
// again: its sender is told it may or may not have arrived. A pane that is
// gone, or a queue already full, is said to the sender.
func requeueHeld(entries []heldEntry, panes func(string) *Pane, now time.Time) []heldLine {
	var lines []heldLine
	byPane := map[string][]QueuedMessage{}
	var order []string
	for _, e := range entries {
		since := e.Time.Format("15:04")
		if e.State == heldStateTyping {
			lines = append(lines, heldLine{e.From, fmt.Sprintf("the message to %s held since %s may or may not have arrived (initech restarted while typing it)", e.Pane, since)})
			continue
		}
		if panes(e.Pane) == nil {
			lines = append(lines, heldLine{e.From, fmt.Sprintf("held message to %s since %s was not delivered: %s no longer exists", e.Pane, since, e.Pane)})
			continue
		}
		m := QueuedMessage{Text: e.Text, Enter: e.Enter, Time: e.Time, From: e.From}
		switch e.Kind {
		case heldKindDND:
			m.DND, m.Released = true, now
		case heldKindDialog:
			m.HeldForDialog, m.Released = true, now
		}
		if _, seen := byPane[e.Pane]; !seen {
			order = append(order, e.Pane)
		}
		byPane[e.Pane] = append(byPane[e.Pane], m)
	}
	for _, name := range order {
		p := panes(name)
		msgs := byPane[name]
		p.mu.Lock()
		room := maxMessageQueue - len(p.messageQueue)
		if room < 0 {
			room = 0
		}
		keep := msgs
		var refused []QueuedMessage
		if len(msgs) > room {
			keep, refused = msgs[:room], msgs[room:] // the oldest kept
		}
		p.messageQueue = append(append([]QueuedMessage(nil), keep...), p.messageQueue...)
		p.mu.Unlock()
		for _, m := range refused {
			lines = append(lines, heldLine{m.From, fmt.Sprintf("held message to %s since %s was not delivered: its queue is full", name, m.Time.Format("15:04"))})
		}
		LogInfo("held", "held mail put back after a restart", "pane", name, "count", len(keep), "refused", len(refused))
		p.heldChanged()
		if !p.IsSuspended() {
			p.startModalDrain() // suspended mail waits for the wake
		}
	}
	return lines
}

// loadHeldMail reads window 1's held file at start, after the panes are built
// and before the main loop serves a send (ini-8rkg), puts the mail back, and
// says what it could not. Main goroutine.
func (t *TUI) loadHeldMail(now time.Time) {
	if t.held == nil {
		return
	}
	defer t.held.start()
	entries, err := readHeldFile(t.held.path)
	if errors.Is(err, errHeldFileBad) {
		aside, rerr := setAsideHeldFile(t.held.path, now)
		LogWarn("held", "held mail file did not parse; set aside", "file", t.held.path, "aside", aside, "err", err, "rename_err", rerr)
		t.sendInitechLine(fmt.Sprintf("the held mail file %s did not parse and was set aside as %s; the mail in it was not delivered", filepath.Base(t.held.path), filepath.Base(aside)))
		return
	}
	if err != nil {
		LogWarn("held", "held mail file could not be read", "file", t.held.path, "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	local := func(name string) *Pane {
		for _, pv := range t.panes {
			if p, ok := pv.(*Pane); ok && p.name == name {
				return p
			}
		}
		return nil
	}
	for _, l := range requeueHeld(entries, local, now) {
		t.tellSender(l.to, l.text)
	}
}

// tellSender says one line to whoever sent a held message: an agent here
// through the send path, the operator as a notice in window 1, anyone else
// (a peer, or no sender recorded) to super, naming them.
func (t *TUI) tellSender(from, text string) {
	switch {
	case from == operatorSender:
		t.notifications = append(t.notifications, notification{
			event:   AgentEvent{Type: EventAgentStalled, Detail: text, Time: time.Now()},
			expires: time.Now().Add(notificationTTL),
		})
		LogInfo("held", "operator told about held mail", "words", len(text))
	case from != "" && t.hasLocalPane(from):
		t.sendInitechLineTo(from, text)
	default:
		who := from
		if who == "" {
			who = "an unknown sender"
		}
		t.sendInitechLine(fmt.Sprintf("for %s: %s", who, text))
	}
}

// hasLocalPane reports a local pane of that name. Main goroutine.
func (t *TUI) hasLocalPane(name string) bool {
	for _, pv := range t.panes {
		if p, ok := pv.(*Pane); ok && p.name == name {
			return true
		}
	}
	return false
}

// loadHeldMail is the serve daemon's start read of its own held file
// (ini-8rkg): the same rules as window 1's. A line goes to the sender's pane
// when the sender is one of this daemon's agents; otherwise it is logged, as
// the daemon has no super of its own.
func (d *Daemon) loadHeldMail(now time.Time) {
	if d.held == nil {
		return
	}
	defer d.held.start()
	entries, err := readHeldFile(d.held.path)
	if errors.Is(err, errHeldFileBad) {
		aside, rerr := setAsideHeldFile(d.held.path, now)
		LogWarn("held", "held mail file did not parse; set aside", "file", d.held.path, "aside", aside, "err", err, "rename_err", rerr)
		return
	}
	if err != nil || len(entries) == 0 {
		if err != nil {
			LogWarn("held", "held mail file could not be read", "file", d.held.path, "err", err)
		}
		return
	}
	for _, l := range requeueHeld(entries, d.findPane, now) {
		if p := d.findPane(l.to); p != nil {
			line := "[from " + initechSender + "] " + l.text
			go initechSend(p, line, sendOrigin{From: initechSender})
			continue
		}
		LogWarn("held", "held mail not delivered after a restart; its sender is not an agent here", "from", l.to, "words", len(l.text))
	}
}
