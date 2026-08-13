// fleet_state.go holds session-global fleet state: which agents are hidden,
// which are protected from auto-suspend, and which are pinned to live-mode
// slots. These three facts are GLOBAL by operator decision -- hiding an agent
// from either window hides it everywhere -- but they used to live inside
// LayoutState, which ini-9ka.3 correctly made PER-WINDOW. Two individually
// correct decisions jointly made global visibility unrepresentable (ini-9ka.8's
// escalation); this store resolves it by giving the global facts their own
// home (ini-9ka.10).
//
// Modeled directly on assignment.go (ini-9ka.4): one file per session, atomic
// write, corrupt-is-an-error at the model layer, absent-is-fresh. The
// read-only degradation trio is structurally the same as ini-9ka.9's, reused
// rather than re-derived.
//
// AUTHORITY: window 1 is the only writer. Secondary windows do not touch this
// file; they send a set_fleet_state control command and window 1 applies it.
// That is a topology fact, not a policy choice -- secondaries have no push
// channel, they command window 1 and window 1 broadcasts outward.
package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// persistentFleetState is the on-disk shape. Sorted slices rather than maps so
// the file is stable across writes and diffable by an operator.
type persistentFleetState struct {
	Hidden     []string       `yaml:"hidden,omitempty"`
	Protected  []string       `yaml:"protected,omitempty"`
	LivePinned map[string]int `yaml:"live_pinned,omitempty"`
}

// FleetState is the session-global store. Keys are pane keys (bare name for
// local panes, host:name for remote), the same space LayoutState used, so
// remote panes generalize for free.
type FleetState struct {
	root       string
	hidden     map[string]bool
	protected  map[string]bool
	livePinned map[string]int

	// readOnly marks a FALLBACK store synthesized because the real file could
	// not be read (ini-9ka.9's rule, applied here). It answers reads normally
	// but save() refuses before touching the filesystem, because the
	// operator's real state is still in that unreadable file: writing would
	// convert a loud, recoverable parse error into silent erasure one
	// interaction later. A corrupt file is not an absent file.
	readOnly bool

	// memoryOnly marks a store with no project root: state is session-scoped
	// and never persisted. This mirrors saveLayoutIfConfigured's existing
	// "no-op if projectRoot is empty" contract, and it is NOT the same as
	// readOnly -- writes are ACCEPTED (so an ad-hoc or test TUI behaves
	// normally in-session), they simply do not reach a file.
	//
	// Without this a rootless TUI would be worse than useless: fleetStatePath("")
	// is RELATIVE, so save() would MkdirAll and drop a stray
	// .initech/fleet-state.yaml into whatever directory initech happened to be
	// launched from -- the same stray-CWD failure ini-9ka.9 called out for the
	// blank-root fallback, reached by a different door.
	memoryOnly bool
}

// ErrFleetStateReadOnly is returned when a write is attempted against a
// fallback store, so callers can distinguish "refused because unreadable"
// from an ordinary failure and tell the operator which it was.
var ErrFleetStateReadOnly = errors.New("fleet state is unreadable; hide/protect/pin cannot be changed until .initech/fleet-state.yaml is repaired or deleted")

// ErrFleetStateNotAuthority is returned when a non-window-1 TUI attempts to
// write directly. Secondary windows must route through set_fleet_state.
var ErrFleetStateNotAuthority = errors.New("only window 1 writes fleet state; secondary windows must send set_fleet_state")

// fleetStatePath returns the full path to .initech/fleet-state.yaml.
func fleetStatePath(projectRoot string) string {
	return filepath.Join(layoutDir(projectRoot), "fleet-state.yaml")
}

// newFleetState builds an empty writable store for a root.
func newFleetState(root string) *FleetState {
	return &FleetState{
		root:       root,
		hidden:     map[string]bool{},
		protected:  map[string]bool{},
		livePinned: map[string]int{},
	}
}

// newFallbackFleetState builds the read-only store used when the real one
// cannot be loaded. It keeps the root -- reads and error messages want to know
// which project this is -- and relies on readOnly, NOT on a blank root, to
// prevent writes. A blank root would be actively worse: fleetStatePath("") is
// RELATIVE, so save() would write a stray .initech/fleet-state.yaml into
// whatever directory initech was launched from and report success.
func newFallbackFleetState(root string) *FleetState {
	fs := newFleetState(root)
	fs.readOnly = true
	return fs
}

// LoadFleetState reads the session-global store.
//
// A missing store is a fresh session, not an error -- and it is also the ONLY
// condition under which legacy fields are imported from layout.yaml (see
// importLegacyFleetState). A store that exists but cannot be parsed IS an
// error: treating corruption as "fresh" would present as a successful reset
// while discarding the operator's real state.
func LoadFleetState(projectRoot string) (*FleetState, error) {
	fs := newFleetState(projectRoot)
	if projectRoot == "" {
		fs.memoryOnly = true
		return fs, nil
	}

	data, err := os.ReadFile(fleetStatePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			// IMPORT-ONCE. Absent store means the import has not happened.
			// Presence of the store -- even an empty one -- means it has, so
			// this is the only branch that ever reads layout.yaml.
			if imported, ok := importLegacyFleetState(projectRoot); ok {
				fs.hidden, fs.protected, fs.livePinned = imported.hidden, imported.protected, imported.livePinned
				if err := fs.save(); err != nil {
					return nil, fmt.Errorf("persist imported fleet state: %w", err)
				}
			}
			return fs, nil
		}
		return nil, fmt.Errorf("read fleet state: %w", err)
	}

	var pfs persistentFleetState
	if err := yaml.Unmarshal(data, &pfs); err != nil {
		return nil, fmt.Errorf("parse fleet state: %w", err)
	}
	for _, k := range pfs.Hidden {
		if k != "" {
			fs.hidden[k] = true
		}
	}
	for _, k := range pfs.Protected {
		if k != "" {
			fs.protected[k] = true
		}
	}
	for k, slot := range pfs.LivePinned {
		if k != "" && slot >= 0 {
			fs.livePinned[k] = slot
		}
	}
	return fs, nil
}

// importLegacyFleetState reads hidden/protected/live_pinned out of a
// pre-ini-9ka.10 layout.yaml, for the one-time migration.
//
// It reads the file directly rather than going through LoadLayout because
// LoadLayout filters persisted keys against a known-pane set, and an import
// must not silently drop state for an agent that happens not to be running at
// upgrade time. The legacy file is NEVER rewritten -- adoption-in-place, the
// ini-9ka.3 precedent: leaving the old fields as dead data has no failure
// mode, while rewriting opens a window in which the operator's state can be
// lost. The loader tolerates their presence forever.
func importLegacyFleetState(projectRoot string) (*FleetState, bool) {
	data, err := os.ReadFile(layoutPath(projectRoot))
	if err != nil {
		return nil, false
	}
	var pl PersistentLayout
	if err := yaml.Unmarshal(data, &pl); err != nil {
		return nil, false // Unreadable legacy layout: nothing to import, not an error.
	}

	fs := newFleetState(projectRoot)
	for _, k := range pl.Hidden {
		if k != "" {
			fs.hidden[k] = true
		}
	}
	// Accept both the current "protected" key and the older deprecated
	// "pinned" key, matching LoadLayout's own migration shim -- an upgrade
	// from two formats back must not lose protection state.
	protectedList := pl.Protected
	if len(protectedList) == 0 {
		protectedList = pl.DepPinned
	}
	for _, k := range protectedList {
		if k != "" {
			fs.protected[k] = true
		}
	}
	for k, slot := range pl.LivePinned {
		if k != "" && slot >= 0 {
			fs.livePinned[k] = slot
		}
	}

	if len(fs.hidden) == 0 && len(fs.protected) == 0 && len(fs.livePinned) == 0 {
		return nil, false // Nothing to import; do not create a file for it.
	}
	return fs, true
}

// save writes the store atomically. The staging path derives from the store's
// own file so it cannot collide with any other atomic write under .initech.
func (fs *FleetState) save() error {
	// Checked here rather than at each call site so there is no path --
	// present or future -- by which an unreadable store overwrites the file
	// it failed to read.
	if fs.readOnly {
		return ErrFleetStateReadOnly
	}
	if fs.memoryOnly {
		return nil // Session-scoped: accepted, deliberately not persisted.
	}

	dir := layoutDir(fs.root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create .initech/: %w", err)
	}

	pfs := persistentFleetState{LivePinned: fs.livePinned}
	for k := range fs.hidden {
		pfs.Hidden = append(pfs.Hidden, k)
	}
	for k := range fs.protected {
		pfs.Protected = append(pfs.Protected, k)
	}
	sort.Strings(pfs.Hidden)
	sort.Strings(pfs.Protected)

	data, err := yaml.Marshal(&pfs)
	if err != nil {
		return fmt.Errorf("marshal fleet state: %w", err)
	}

	final := fleetStatePath(fs.root)
	if err := writeFileAtomic(final, data, 0600); err != nil {
		return fmt.Errorf("write fleet state: %w", err)
	}
	return nil
}

// ── reads ───────────────────────────────────────────────────────────

// IsHidden reports whether the pane key is hidden fleet-wide.
func (fs *FleetState) IsHidden(key string) bool { return fs.hidden[key] }

// IsProtected reports whether the pane key is protected from auto-suspend.
func (fs *FleetState) IsProtected(key string) bool { return fs.protected[key] }

// LiveSlot returns the pinned live-mode slot for a pane key, if any.
func (fs *FleetState) LiveSlot(key string) (int, bool) {
	slot, ok := fs.livePinned[key]
	return slot, ok
}

// HiddenMap, ProtectedMap and LivePinnedMap return COPIES for the derived
// LayoutState projection. Copies, not the live maps, so a consumer that
// mutates its projection cannot reach back into the store -- the projection
// is refreshed FROM the store and never the reverse.
func (fs *FleetState) HiddenMap() map[string]bool    { return copyBoolMap(fs.hidden) }
func (fs *FleetState) ProtectedMap() map[string]bool { return copyBoolMap(fs.protected) }
func (fs *FleetState) LivePinnedMap() map[string]int {
	out := make(map[string]int, len(fs.livePinned))
	for k, v := range fs.livePinned {
		out[k] = v
	}
	return out
}

func copyBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ── writes (window 1 only; each persists immediately) ───────────────

// SetHidden sets or clears the fleet-wide hidden flag for a pane key.
func (fs *FleetState) SetHidden(key string, hidden bool) error {
	return fs.mutate(func() {
		if hidden {
			fs.hidden[key] = true
		} else {
			delete(fs.hidden, key)
		}
	})
}

// SetProtected sets or clears the fleet-wide protected flag.
func (fs *FleetState) SetProtected(key string, protected bool) error {
	return fs.mutate(func() {
		if protected {
			fs.protected[key] = true
		} else {
			delete(fs.protected, key)
		}
	})
}

// SetLiveSlot pins a pane key to a live-mode slot, evicting any other key
// already holding that slot (a slot holds one agent). Passing pinned=false
// unpins the key.
func (fs *FleetState) SetLiveSlot(key string, slot int, pinned bool) error {
	return fs.mutate(func() {
		if !pinned {
			delete(fs.livePinned, key)
			return
		}
		for k, s := range fs.livePinned {
			if s == slot && k != key {
				delete(fs.livePinned, k)
			}
		}
		fs.livePinned[key] = slot
	})
}

// ClearHidden reveals every hidden agent fleet-wide (the modal's A binding).
func (fs *FleetState) ClearHidden() error {
	return fs.mutate(func() { fs.hidden = map[string]bool{} })
}

// mutate applies a change and persists it, rolling the change back if the
// write fails so the in-memory store never claims a state that is not on
// disk. Refusal from a read-only fallback happens before any mutation.
func (fs *FleetState) mutate(apply func()) error {
	if fs.readOnly {
		return ErrFleetStateReadOnly
	}
	prevHidden := copyBoolMap(fs.hidden)
	prevProtected := copyBoolMap(fs.protected)
	prevLive := fs.LivePinnedMap()

	apply()

	if err := fs.save(); err != nil {
		fs.hidden, fs.protected, fs.livePinned = prevHidden, prevProtected, prevLive
		return err
	}
	return nil
}

// ── TUI seam: accessor, projection refresh, authority-aware mutators ──

// fleetState returns the session-global store, loading it once and caching it.
// An unreadable store degrades to a READ-ONLY fallback rather than an empty
// writable one (ini-9ka.9's rule): reads answer as "nothing hidden/protected/
// pinned", which is the correct degraded view, while writes are refused so the
// operator's real -- merely unparseable -- state is never overwritten.
func (t *TUI) fleetState() *FleetState {
	if t.fleet != nil {
		return t.fleet
	}
	fs, err := LoadFleetState(t.projectRoot)
	if err != nil {
		LogWarn("fleet", "fleet state unreadable, degrading read-only and refusing writes", "err", err)
		fs = newFallbackFleetState(t.projectRoot)
	}
	t.fleet = fs
	t.applyFleetProjection()
	return t.fleet
}

// applyFleetProjection refreshes LayoutState's derived Hidden/Protected/
// LivePinned maps from the store. This is the ONLY direction state flows:
// store -> memory, never memory -> store. Every mutator below calls it after
// a successful write, so the projection cannot drift from the persisted truth.
func (t *TUI) applyFleetProjection() {
	if t.fleet == nil {
		return
	}
	t.layoutState.Hidden = t.fleet.HiddenMap()
	t.layoutState.Protected = t.fleet.ProtectedMap()
	t.layoutState.LivePinned = t.fleet.LivePinnedMap()
}

// isFleetAuthority reports whether this TUI may write fleet state directly.
// Window 1 owns the file; secondaries have no push channel and must command
// window 1 instead (ini-9ka.8's topology fact).
func (t *TUI) isFleetAuthority() bool { return t.windowID == WindowOne }

// mutateFleet applies a global change with the authority rule enforced, then
// refreshes the projection. A secondary window is refused here rather than at
// each call site, so no present-or-future keybinding can bypass the authority
// by writing the file itself.
func (t *TUI) mutateFleet(action string, apply func(*FleetState) error) error {
	fs := t.fleetState()
	if !t.isFleetAuthority() {
		// Secondary windows do not write the file. This is unreachable in
		// normal flow -- setHidden/setProtected/setLiveSlot route secondaries
		// through sendFleetStateCmd before getting here -- but it is enforced
		// at the mutation point too, so no present-or-future path can bypass
		// the authority by calling mutateFleet directly.
		return ErrFleetStateNotAuthority
	}
	if err := apply(fs); err != nil {
		t.noticeFleetWriteFailed(action, err)
		return err
	}
	t.applyFleetProjection()
	return nil
}

// setHidden, setProtected, setLiveSlot and clearHidden are the write seam every
// keybinding and IPC path routes through.
func (t *TUI) setHidden(key string, hidden bool) error {
	if !t.isFleetAuthority() {
		if err := t.sendFleetStateCmd("hidden", key, hidden, 0); err != nil {
			t.noticeFleetWriteFailed("hidden "+key, err)
			return err
		}
		return nil
	}
	return t.mutateFleet("hide "+key, func(fs *FleetState) error { return fs.SetHidden(key, hidden) })
}

func (t *TUI) setProtected(key string, protected bool) error {
	if !t.isFleetAuthority() {
		if err := t.sendFleetStateCmd("protected", key, protected, 0); err != nil {
			t.noticeFleetWriteFailed("protected "+key, err)
			return err
		}
		return nil
	}
	return t.mutateFleet("protect "+key, func(fs *FleetState) error { return fs.SetProtected(key, protected) })
}

func (t *TUI) setLiveSlot(key string, slot int, pinned bool) error {
	if !t.isFleetAuthority() {
		if err := t.sendFleetStateCmd("live_pinned", key, pinned, slot); err != nil {
			t.noticeFleetWriteFailed("pin "+key, err)
			return err
		}
		return nil
	}
	return t.mutateFleet("pin "+key, func(fs *FleetState) error { return fs.SetLiveSlot(key, slot, pinned) })
}

func (t *TUI) clearHidden() error {
	return t.mutateFleet("reveal all", func(fs *FleetState) error { return fs.ClearHidden() })
}

// noticeFleetWriteFailed surfaces a refused or failed global write. A silent
// no-op would leave the operator believing a hide/protect/pin applied -- worst
// in the read-only case, where the refusal exists precisely BECAUSE their real
// state is still on disk, so the notice names the recovery action.
func (t *TUI) noticeFleetWriteFailed(action string, err error) {
	detail := fmt.Sprintf("%s failed: %v", action, err)
	switch {
	case errors.Is(err, ErrFleetStateReadOnly):
		detail = fmt.Sprintf("%s was not applied: fleet state is unreadable. "+
			"Your existing hide/protect/pin state is preserved on disk and was NOT overwritten. "+
			"Repair or delete .initech/fleet-state.yaml, then reopen this modal.", action)
	case errors.Is(err, ErrFleetStateNotAuthority):
		detail = fmt.Sprintf("%s was not applied: only window 1 writes fleet state.", action)
	}
	EmitEvent(t.agentEvents, AgentEvent{Type: EventAssignmentWriteRefused, Detail: detail, Time: time.Now()})
}

// ── set_fleet_state control command (secondary -> window 1) ─────────

// FleetStateCmd is the control command a SECONDARY window sends to window 1
// to change global fleet state. Secondaries never write the file: they have no
// push channel, so they command the authority and the authority broadcasts
// (ini-9ka.8's topology fact, ini-9ka.10's authority rule).
type FleetStateCmd struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"`         // "set_fleet_state"
	Name   string `json:"name"`           // Pane key.
	Field  string `json:"field"`          // "hidden" | "protected" | "live_pinned"
	On     bool   `json:"on"`             // Set or clear.
	Slot   int    `json:"slot,omitempty"` // Live-mode slot, for "live_pinned".
}

// applyFleetStateCmd applies a set_fleet_state command on window 1. It is the
// single entry point the daemon hands the command to, so a secondary's request
// takes exactly the same path -- and the same authority and read-only checks --
// as a local keypress.
func (t *TUI) applyFleetStateCmd(cmd FleetStateCmd) error {
	if cmd.Name == "" {
		return fmt.Errorf("name is required")
	}
	// MARSHALLED ONTO THE MAIN LOOP (ini-8od). This is called from the daemon's
	// per-connection goroutine (handleSetFleetState), while window 1's own
	// keybindings drive the identical path from the main loop. Both mutate
	// FleetState's plain maps AND, via applyFleetProjection, t.layoutState --
	// which the render loop reads every frame. Two unsynchronised writers to a
	// Go map can panic the process outright, and panicking window 1 takes the
	// whole fleet's display with it.
	//
	// A mutex on FleetState was the alternative and is weaker: it would guard
	// the maps it owns and leave the PROJECTION race untouched, since
	// applyFleetProjection writes t.layoutState and the renderer reads it. The
	// main loop is already the single writer of everything downstream of this
	// call, so handing it the mutation keeps one writer instead of adding a
	// lock that the next unguarded reader would silently step around -- the
	// same reasoning as the t.panes dispatch (ini-a1e.30).
	//
	// runOnMain executes inline when the caller IS the main goroutine, so
	// window 1's local keypresses do not deadlock on themselves.
	var err error
	if !t.runOnMain(func() { err = t.applyFleetStateField(cmd) }) {
		// The session is shutting down and the mutation never ran. Reporting
		// success here would tell a secondary window its change applied when
		// nothing was written -- the operator believing state changed when it
		// did not is the failure this codebase keeps refusing to ship.
		return fmt.Errorf("window 1 is shutting down; %s change for %q was not applied", cmd.Field, cmd.Name)
	}
	return err
}

// applyFleetStateField is the switch itself, split out so it always runs on the
// main goroutine (see applyFleetStateCmd). Never call it directly from a
// non-main goroutine.
func (t *TUI) applyFleetStateField(cmd FleetStateCmd) error {
	switch cmd.Field {
	case "hidden":
		return t.setHidden(cmd.Name, cmd.On)
	case "protected":
		return t.setProtected(cmd.Name, cmd.On)
	case "live_pinned":
		return t.setLiveSlot(cmd.Name, cmd.Slot, cmd.On)
	default:
		return fmt.Errorf("unknown fleet-state field %q", cmd.Field)
	}
}

// sendFleetStateCmd forwards a global change from a secondary window to window
// 1 over the peer control stream. Returns an error if there is no connection,
// so the caller can tell the operator the change did not apply rather than
// leaving them believing it did.
func (t *TUI) sendFleetStateCmd(field, key string, on bool, slot int) error {
	mux := t.windowOneMux()
	if mux == nil {
		return fmt.Errorf("not connected to window 1; %s change was not applied", field)
	}
	resp, err := mux.RequestRaw(FleetStateCmd{
		Action: "set_fleet_state",
		Name:   key,
		Field:  field,
		On:     on,
		Slot:   slot,
	})
	if err != nil {
		return fmt.Errorf("send %s to window 1: %w", field, err)
	}
	if !resp.OK {
		return fmt.Errorf("window 1 refused %s: %s", field, resp.Error)
	}
	return nil
}

// windowOneMux returns the control mux for this secondary window's connection
// to window 1. Every RemotePane in a secondary shares the one peer connection,
// so the first live mux is the right one.
func (t *TUI) windowOneMux() *ControlMux {
	for _, p := range t.panes {
		if rp, ok := p.(*RemotePane); ok {
			if m := rp.Mux(); m != nil {
				return m
			}
		}
	}
	return nil
}
