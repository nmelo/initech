package tui

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// LIVE REMOTES (ini-7op8). Window 1 re-reads initech.yaml when it changes and
// applies only the remotes block: a remote added there connects and gets its
// roles, one removed detaches, and one whose addr or token changed is
// detached and connected again under the same name. initech remote add and
// remove edit the file and then ask for the same reload at once. A file that
// does not parse, or does not validate, changes nothing and is logged once.

const (
	remotesKickEvery = time.Second
	// remotesDebounce is how long the file must stay unchanged before it is
	// read, so an editor's save in several writes is read once, whole.
	remotesDebounce = 500 * time.Millisecond
)

// remotesReloader watches initech.yaml and reconciles the running remotes.
type remotesReloader struct {
	path      string
	pm        *peerManager
	onRemoved func(name string, changed bool)
	now       func() time.Time

	busy     atomic.Bool
	lastKick atomic.Int64

	mu        sync.Mutex // One reload at a time; guards the fields below.
	modTime   time.Time
	changedAt time.Time
	lastErr   string
}

func newRemotesReloader(path string, pm *peerManager, onRemoved func(string, bool), now func() time.Time) *remotesReloader {
	r := &remotesReloader{path: path, pm: pm, onRemoved: onRemoved, now: now}
	if fi, err := os.Stat(path); err == nil {
		r.modTime = fi.ModTime() // What the start read; not a change.
	}
	return r
}

// kick runs one check in the background unless one is running or the last
// was under a second ago. It is what the main goroutine calls, and it never
// waits.
func (r *remotesReloader) kick() {
	now := r.now().UnixNano()
	if now-r.lastKick.Load() < int64(remotesKickEvery) {
		return
	}
	if !r.busy.CompareAndSwap(false, true) {
		return
	}
	r.lastKick.Store(now)
	go func() {
		defer r.busy.Store(false)
		r.check()
	}()
}

// check notes a change to the file's mtime and reloads once it has held for
// remotesDebounce.
func (r *remotesReloader) check() {
	fi, err := os.Stat(r.path)
	if err != nil {
		return
	}
	now := r.now()
	r.mu.Lock()
	if !fi.ModTime().Equal(r.modTime) {
		r.modTime = fi.ModTime()
		r.changedAt = now
	}
	due := !r.changedAt.IsZero() && now.Sub(r.changedAt) >= remotesDebounce
	if due {
		r.changedAt = time.Time{}
	}
	r.mu.Unlock()
	if due {
		r.reload() //nolint:errcheck // Logged inside; the watch has no caller to tell.
	}
}

// reload reads initech.yaml now and makes the running remotes match it.
func (r *remotesReloader) reload() (remotesChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fi, err := os.Stat(r.path); err == nil {
		r.modTime = fi.ModTime()
		r.changedAt = time.Time{}
	}
	p, err := config.LoadQuiet(r.path)
	if err != nil {
		if err.Error() != r.lastErr {
			r.lastErr = err.Error()
			LogWarn("remotes", "initech.yaml refused; the running remotes are unchanged", "err", err)
		}
		return remotesChange{}, err
	}
	r.lastErr = ""
	c := r.pm.reconcile(p, r.onRemoved)
	if !c.Empty() {
		LogInfo("remotes", "remotes reloaded", "removed", strings.Join(c.Removed, ","),
			"changed", strings.Join(c.Changed, ","), "added", strings.Join(c.Added, ","))
	}
	return c, nil
}

// String says what a reload did, for initech remote add and remove.
func (c remotesChange) String() string {
	if c.Empty() {
		return "remotes unchanged"
	}
	var parts []string
	if len(c.Removed) > 0 {
		parts = append(parts, "removed "+strings.Join(c.Removed, ", "))
	}
	if len(c.Changed) > 0 {
		parts = append(parts, "reconnecting "+strings.Join(c.Changed, ", "))
	}
	if len(c.Added) > 0 {
		parts = append(parts, "connecting "+strings.Join(c.Added, ", "))
	}
	return strings.Join(parts, "; ")
}

// handlePeerRemoved drops a remote's panes once its manager has stopped,
// on the main goroutine. A remote removed from initech.yaml also leaves the
// layout; one that changed addr or token keeps its place, since its panes
// return under the same names. Nothing is sent to the peer: its agents run
// on, and any mail it holds for them stays there.
func (t *TUI) handlePeerRemoved(name string, changed bool) {
	n := 0
	for _, p := range t.panes {
		if rp, ok := p.(*RemotePane); ok && rp.Host() == name {
			n++
		}
	}
	if t.peerConnected == nil {
		t.peerConnected = make(map[string]bool)
	}
	// Marked offline first so handlePeerUpdate does not also say
	// "disconnected": the line below says what happened.
	t.peerConnected[name] = false
	t.handlePeerUpdate(name, nil, false)
	delete(t.peerConnected, name)
	detail := fmt.Sprintf("%s left the remotes (%d agents detached; their mail stays on the peer)", name, n)
	if changed {
		detail = fmt.Sprintf("%s reconnecting: its addr or token changed in initech.yaml", name)
	} else {
		t.forgetLayoutHost(name)
	}
	t.handleAgentEvent(AgentEvent{Type: EventPeerDisconnected, Detail: detail})
}

// forgetLayoutHost drops a removed remote's agents from the saved layout.
func (t *TUI) forgetLayoutHost(host string) {
	prefix := host + ":"
	kept := t.layoutState.Order[:0]
	for _, k := range t.layoutState.Order {
		if !strings.HasPrefix(k, prefix) {
			kept = append(kept, k)
		}
	}
	t.layoutState.Order = kept
	for _, m := range []map[string]bool{t.layoutState.Hidden, t.layoutState.Protected} {
		for k := range m {
			if strings.HasPrefix(k, prefix) {
				delete(m, k)
			}
		}
	}
	t.saveLayoutIfConfigured()
}

// handleIPCRemotesReload is initech remote add and remove's signal: reload
// now and say what changed.
func (t *TUI) handleIPCRemotesReload(conn net.Conn) {
	if t.remotes == nil {
		writeIPCResponse(conn, IPCResponse{Error: "this window does not keep the remotes; window 1 does"})
		return
	}
	c, err := t.remotes.reload()
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: "initech.yaml refused, the running remotes are unchanged: " + err.Error()})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Data: c.String()})
}
