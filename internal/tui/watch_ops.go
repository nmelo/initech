package tui

// watch_ops.go is what initech watch set, list and cancel do to the store,
// apart from the connection and the panes: the IPC handler works out the
// owner and hands it in, and these run on the main goroutine. Kept free of
// both so tests drive them against the real store.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// errWatchOutsidePane is the refusal for set and cancel run outside any
// agent's pane: a watch's owner is the pane that runs the command.
var errWatchOutsidePane = errors.New("run initech watch from inside your agent's pane: a watch belongs to the pane that sets it")

// watchValidator is 2B's check of a condition at set time: the bead exists,
// the status is one bd knows, the receipt is the owner's own, no self-firing.
type watchValidator func(kind watch.Kind, params map[string]string, owner string) error

// watchSet validates the condition and adds the watch for owner, returning
// its id. owner is "" outside any pane, which is refused.
func watchSet(s *watch.Store, validate watchValidator, owner string, req WatchSetRequest, now time.Time) (string, error) {
	if err := watchCheck(validate, owner, req); err != nil {
		return "", err
	}
	return watchAdd(s, owner, req, now)
}

// watchCheck is set's part off the main goroutine: the owner, then 2B's
// condition check (which reads the bead poll itself).
func watchCheck(validate watchValidator, owner string, req WatchSetRequest) error {
	if owner == "" {
		return errWatchOutsidePane
	}
	return validate(req.Kind, req.Params, owner)
}

// watchAdd is set's part on the main goroutine: the store's own refusals
// (mandatory parts, a past deadline, the cap, a duplicate) and the add.
func watchAdd(s *watch.Store, owner string, req WatchSetRequest, now time.Time) (string, error) {
	return s.Add(watch.Watch{Owner: owner, Kind: req.Kind, Params: req.Params, Why: req.Why,
		Deadline: req.Deadline, HoldIfAsleep: req.HoldIfAsleep}, now)
}

// watchCancel cancels owner's own watch id. owner is "" outside any pane.
func watchCancel(s *watch.Store, owner, id string, now time.Time) (string, error) {
	if owner == "" {
		return "", errWatchOutsidePane
	}
	if err := s.Cancel(id, owner, now); err != nil {
		return "", err
	}
	return "cancelled " + id, nil
}

// watchList is the asker's own watches, or with All every watch in the rig,
// which only super or the operator (outside any pane, asker "") may see. key
// names a watch's check, so watches sharing one list each other's owners;
// wakes says whether a watch's line will wake its sleeping owner.
func watchList(s *watch.Store, asker string, req WatchListRequest, now time.Time,
	key func(watch.Watch) string, wakes func(watch.Watch) bool) ([]WatchListing, error) {
	switch {
	case req.All && asker != "" && asker != superPaneName:
		return nil, fmt.Errorf("--all is for super or the operator outside any pane; %s lists its own watches", asker)
	case !req.All && asker == "":
		return nil, errors.New("outside any agent's pane there are no own watches: use initech watch list --all")
	}
	all := s.Snapshot(now)
	owners := map[string]map[string]bool{} // check key -> owners of waiting watches on it
	for _, w := range all {
		if w.State == watch.Waiting {
			k := key(w)
			if owners[k] == nil {
				owners[k] = map[string]bool{}
			}
			owners[k][w.Owner] = true
		}
	}
	out := []WatchListing{}
	for _, w := range all {
		if !req.All && w.Owner != asker {
			continue
		}
		row := WatchListing{Watch: w}
		if w.State == watch.Waiting {
			for o := range owners[key(w)] {
				if o != w.Owner {
					row.Sharers = append(row.Sharers, o)
				}
			}
			sort.Strings(row.Sharers)
			row.Wakes = wakes(w)
		}
		out = append(out, row)
	}
	return out, nil
}

// watchKey names a watch's check for list's "shared with": the kind and
// 2B's own Key, so watches judged by one check list each other's owners. A
// plain deadline shares nothing (each has its own deadline), and a kind no
// checker knows is keyed by its params, so the same condition still matches.
func watchKey(w watch.Watch) string {
	if w.Kind == watch.KindDeadline {
		return "deadline|" + w.ID
	}
	if c, ok := watchCheckers[w.Kind]; ok {
		return string(w.Kind) + "|" + c.Key(w.Params)
	}
	keys := make([]string, 0, len(w.Params))
	for k := range w.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(string(w.Kind))
	for _, k := range keys {
		fmt.Fprintf(&b, "|%s=%s", k, w.Params[k])
	}
	return b.String()
}
