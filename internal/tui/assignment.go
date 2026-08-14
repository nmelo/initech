// assignment.go holds the group-to-window assignment model: WHICH groups a
// window shows. It is deliberately separate from layout persistence
// (layout.go), which holds HOW a window arranges what it shows. The two are
// independent concerns that round-trip independently -- assignment must load
// and save correctly with layout state corrupt, absent, or disagreeing, and
// vice versa (ini-9ka.3 / ini-9ka.4).
//
// Nothing in this file reads LayoutState. Where a query needs the agent->group
// or group-universe mapping (both of which live in layout state), the caller
// passes it in. That is what keeps the independence structural rather than
// a convention someone can quietly break.
package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// WindowOne is the identity of the primary window. It is the empty string for
// two reasons: it is the same identity space per-window layout files use
// (ini-9ka.3), so a window's layout file and its assignment entry key off one
// identifier rather than needing translation; and it makes "absent from the
// store" and "assigned to window 1" the same state, which is what gives the
// fresh-project default for free.
const WindowOne = ""

// WindowOnePeerName is the name window 1 answers to ON THE WIRE, as distinct
// from WindowOne above, which is its identity in the ASSIGNMENT model.
//
// The two exist because the assignment model wants "absent means window 1"
// (hence the empty string) while the connection layer cannot dial or verify an
// empty name: a secondary window's remotes map needs a non-empty key, and
// connectPeer asserts the server's advertised peer_name equals the label the
// client dialled. Keeping both identities is fine; keeping them in two places
// that could disagree was ini-1ch, where window 1 advertised "" against a
// client expecting "window1" and every attach failed the handshake.
const WindowOnePeerName = "window1"

// WindowPeerName returns the canonical identity of window n -- the ONE string
// that names it in the assignment store, on the wire, and in the connected-
// window registry. The family is IRREGULAR by history: window1 (no hyphen,
// ini-1ch's wire name) then window-2, window-3 (cmd/window.go's viewer
// identities). That irregularity is exactly why two independent generators
// drifted (ini-xq4r): the agents modal synthesized the plausible regular form
// "window2" for a viewer that presents "window-2", so the viewer never matched
// its own assignment -- it planned zero panes while window 1's orphan rule,
// correctly, kept everything for a window that as far as it could tell never
// attached. Every producer of a window identity MUST call this; cmd/window.go
// delegates here.
func WindowPeerName(n int) string {
	if n <= 1 {
		return WindowOnePeerName
	}
	return fmt.Sprintf("window-%d", n)
}

// legacyWindowIDRe matches the assignment identities the pre-xq4r modal
// generator produced ("window2", "window3", ...). Only that generator ever
// wrote this form -- window 1 is "window1" (matched and left alone via
// WindowPeerName) and viewers are "window-N" -- so normalizing it on load
// cannot clobber a legitimate custom identity.
var legacyWindowIDRe = regexp.MustCompile(`^window([2-9][0-9]*)$`)

// persistentAssignment is the on-disk shape. Only NON-default assignments are
// stored: a group absent from the map is on window 1. This keeps a fresh
// project's file empty (or missing) rather than requiring a seeding step that
// could fail, and means a group introduced later at runtime (ensureGroups
// seeds new bands as agents appear) is automatically on window 1 rather than
// in an undefined state.
type persistentAssignment struct {
	GroupWindow map[string]string `yaml:"group_window,omitempty"`
}

// WindowAssignment maps group labels to window identities and persists itself
// on every change. It owns its project root so edits cannot be made without
// being written -- the spec requires both assignment-editing grains to persist
// immediately, and a model that relies on the caller remembering to save is a
// footgun for the modal UI (ini-9ka.5) that will drive it.
type WindowAssignment struct {
	// healed records that LoadAssignment normalized a legacy identity, so the
	// authority can persist the healed form ONCE (ini-m495): without the
	// persist, the heal and its log re-ran on every load forever -- observed
	// live firing each reconnect cycle for hours.
	healed      bool
	root        string
	groupWindow map[string]string

	// readOnly marks a FALLBACK store -- one synthesized because the real
	// store on disk could not be read (ini-9ka.9). It answers reads normally
	// (everything on window 1, which is the correct degraded view) but cannot
	// persist: save() refuses before touching the filesystem.
	//
	// This exists because the alternative is silent data loss. A fallback
	// store built from a CORRUPT file still carries that file's root, so any
	// write would replace the operator's real -- merely unreadable --
	// arrangement with a near-empty one, converting a loud, recoverable parse
	// error into quiet erasure one interaction later. A corrupt file is not
	// an absent file, and the fallback must not treat it as one.
	readOnly bool
}

// ErrAssignmentReadOnly is returned when a write is attempted against a
// fallback store. Callers match on it to tell "your move was refused because
// the store is unreadable" apart from an ordinary validation failure, so the
// operator can be told the difference.
var ErrAssignmentReadOnly = errors.New("assignment store is unreadable; window assignments cannot be changed until .initech/assignments.yaml is repaired or deleted")

// newFallbackAssignment builds the read-only store used when the real one
// cannot be loaded. It deliberately keeps the root -- reads and error messages
// want to know which project it belongs to -- and relies on readOnly, NOT on
// an empty root, to prevent writes. An empty root would be actively worse:
// assignmentPath("") is RELATIVE, so save() would MkdirAll and write a stray
// .initech/assignments.yaml into whatever directory initech was launched
// from, reporting success while corrupting nothing the operator can find.
func newFallbackAssignment(root string) *WindowAssignment {
	return &WindowAssignment{
		root:        root,
		groupWindow: map[string]string{},
		readOnly:    true,
	}
}

// assignmentPath returns the full path to .initech/assignments.yaml.
func assignmentPath(projectRoot string) string {
	return filepath.Join(layoutDir(projectRoot), "assignments.yaml")
}

// LoadAssignment reads the assignment store for a project. A missing store is
// a fresh project, not an error: every group defaults to window 1.
//
// A store that exists but cannot be parsed IS an error. Treating corruption as
// "fresh project" would silently present as a successful reset while losing
// the operator's real arrangement -- the caller needs to be able to tell those
// apart.
func LoadAssignment(projectRoot string) (*WindowAssignment, error) {
	a := &WindowAssignment{root: projectRoot, groupWindow: map[string]string{}}
	// a.healed is set below when a legacy identity was normalized; the load
	// itself stays read-only (viewers call it, and the civ rule forbids their
	// writing shared state). The AUTHORITY persists via PersistHealIfNeeded.

	data, err := os.ReadFile(assignmentPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return a, nil // Fresh project: all groups on window 1.
		}
		return nil, fmt.Errorf("read assignment store: %w", err)
	}

	var pa persistentAssignment
	if err := yaml.Unmarshal(data, &pa); err != nil {
		return nil, fmt.Errorf("parse assignment store: %w", err)
	}
	for group, window := range pa.GroupWindow {
		// Heal stores written by the pre-xq4r generator: "window2" named a
		// window that could never attach (viewers present "window-2"), so the
		// group's panes rendered nowhere. One-time, logged, save-on-next-move.
		if m := legacyWindowIDRe.FindStringSubmatch(window); m != nil {
			normalized := "window-" + m[1]
			LogInfo("assignment", "normalized legacy window identity", "group", group, "from", window, "to", normalized)
			window = normalized
			a.healed = true
		}
		// Drop entries that could not have been written by MoveGroup. A
		// hand-edited or truncated store should degrade to the default for
		// the bad entries rather than propagate an invalid window identity
		// into path or key construction downstream.
		if group == "" || !validWindowID(window) || window == WindowOne {
			continue
		}
		a.groupWindow[group] = window
	}
	return a, nil
}

// save writes the store atomically (temp file + rename), mirroring
// SaveLayout. The staging path is derived from the store's own path so it
// cannot collide with any other atomic write under .initech.
func (a *WindowAssignment) save() error {
	// A fallback store never writes. Checked here rather than at each call
	// site so there is no path -- present or future -- by which an unreadable
	// store can overwrite the file it failed to read.
	if a.readOnly {
		return ErrAssignmentReadOnly
	}
	dir := layoutDir(a.root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create .initech/: %w", err)
	}

	pa := persistentAssignment{GroupWindow: a.groupWindow}
	data, err := yaml.Marshal(&pa)
	if err != nil {
		return fmt.Errorf("marshal assignment: %w", err)
	}

	final := assignmentPath(a.root)
	if err := writeFileAtomic(final, data, 0600); err != nil {
		return fmt.Errorf("write assignment: %w", err)
	}
	return nil
}

// PersistHealIfNeeded writes the healed store to disk, once, on the window
// that OWNS the file (ini-m495). Viewers never call this -- LoadAssignment is
// deliberately read-only because they share it, and the civ rule forbids a
// viewer writing shared session state. A read-only fallback store never
// persists either (its save refuses, which is correct: the operator's real
// state is still in the unreadable file).
func (a *WindowAssignment) PersistHealIfNeeded() {
	if a == nil || !a.healed || a.readOnly {
		return
	}
	if err := a.save(); err != nil {
		LogWarn("assignment", "could not persist identity heal; will re-heal next load", "err", err)
		return
	}
	a.healed = false
	LogInfo("assignment", "persisted legacy identity heal")
}

// MoveGroup assigns a group to a window and persists immediately. Moving a
// group to WindowOne removes its entry, since absence is how window 1 is
// represented. Returns an error without mutating anything if the group label
// is empty or the window identity is unsafe.
func (a *WindowAssignment) MoveGroup(group, windowID string) error {
	if group == "" {
		return fmt.Errorf("group label must not be empty")
	}
	if !validWindowID(windowID) {
		return fmt.Errorf("invalid window id %q: must contain only letters, digits, or hyphens", windowID)
	}

	prev, had := a.groupWindow[group]
	if windowID == WindowOne {
		delete(a.groupWindow, group)
	} else {
		a.groupWindow[group] = windowID
	}

	if err := a.save(); err != nil {
		// Roll back so the in-memory model never claims a state that is not
		// on disk -- the whole point of persisting on every edit.
		if had {
			a.groupWindow[group] = prev
		} else {
			delete(a.groupWindow, group)
		}
		return err
	}
	return nil
}

// WindowOfGroup returns the window a group is assigned to. Any group with no
// explicit entry -- including one never seen before -- is on window 1, so this
// always returns exactly one window and never reports "unassigned".
func (a *WindowAssignment) WindowOfGroup(group string) string {
	if w, ok := a.groupWindow[group]; ok {
		return w
	}
	return WindowOne
}

// WindowOfAgent returns the window an agent is currently assigned to, via its
// group. groupOf is the agent-key -> group-label mapping, which lives in
// LAYOUT state (LayoutState.GroupOf) and is therefore passed in rather than
// read here: reading it directly would be more convenient and would silently
// couple assignment to layout, breaking the independence both concerns are
// specified to have. An agent with no group entry is on window 1, so every
// agent resolves to exactly one window.
func (a *WindowAssignment) WindowOfAgent(agentKey string, groupOf map[string]string) string {
	group, ok := groupOf[agentKey]
	if !ok {
		return WindowOne
	}
	return a.WindowOfGroup(group)
}

// GroupsForWindow returns the subset of allGroups assigned to windowID,
// preserving allGroups' order. allGroups is the group universe, which lives in
// LAYOUT state (LayoutState.Groups) and is passed in for the same reason
// groupOf is above.
//
// This answers for ANY window identity, connected or not. Fold-back
// (ini-9ka.7) depends on that: it reads a window's assignment precisely when
// that window is gone, in order to know what to restore into window 1 and what
// to hand back on reattach. There is deliberately no liveness parameter and no
// API here that reacts to a disconnect -- a window dying does not change what
// is assigned to it, which is what makes reattach restore exactly.
func (a *WindowAssignment) GroupsForWindow(windowID string, allGroups []string) []string {
	var out []string
	for _, g := range allGroups {
		if a.WindowOfGroup(g) == windowID {
			out = append(out, g)
		}
	}
	return out
}
