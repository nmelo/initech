package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// opsT0 is the fixed now of these tests: no test reads the real clock.
var opsT0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func opsStore(t *testing.T) *watch.Store {
	t.Helper()
	s, note := watch.Open(filepath.Join(t.TempDir(), "watches.yaml"), opsT0)
	if s == nil || note != "" {
		t.Fatalf("open: %v %q", s, note)
	}
	return s
}

// okValidate accepts every condition and records what it was asked.
type validateLog struct{ calls []string }

func (v *validateLog) ok(kind watch.Kind, params map[string]string, owner string) error {
	v.calls = append(v.calls, fmt.Sprintf("%s %v %s", kind, params, owner))
	return nil
}

func beadReq(bead, why string) WatchSetRequest {
	return WatchSetRequest{Kind: watch.KindBeadStatus, Params: map[string]string{"bead": bead, "status": "ready_for_qa"},
		Why: why, Deadline: opsT0.Add(time.Hour)}
}

func TestWatchSet_ValidatesThenStores_ForTheConnectionsOwner(t *testing.T) {
	s, v := opsStore(t), &validateLog{}
	id, err := watchSet(s, v.ok, "eng6", beadReq("ini-a", "read it"), opsT0)
	if err != nil || id != "w1" {
		t.Fatalf("set = %q, %v", id, err)
	}
	if len(v.calls) != 1 || v.calls[0] != "bead_status map[bead:ini-a status:ready_for_qa] eng6" {
		t.Errorf("validate calls = %v", v.calls)
	}
	ws := s.Snapshot(opsT0)
	if len(ws) != 1 || ws[0].Owner != "eng6" || ws[0].Why != "read it" || ws[0].State != watch.Waiting {
		t.Errorf("stored = %+v", ws)
	}
}

// Every refusal sets nothing: outside a pane, the validator's (an unknown
// bead, a status bd does not know, another's receipt, self-firing), and the
// store's (the cap, a duplicate, a past deadline).
func TestWatchSet_Refusals_SetNothing(t *testing.T) {
	refuse := func(watch.Kind, map[string]string, string) error { return errors.New("ini-zzz is not a bead bd knows") }
	cases := map[string]func(*watch.Store) error{
		"outside a pane": func(s *watch.Store) error {
			_, err := watchSet(s, (&validateLog{}).ok, "", beadReq("ini-a", "w"), opsT0)
			return err
		},
		"the validator": func(s *watch.Store) error {
			_, err := watchSet(s, refuse, "eng6", beadReq("ini-zzz", "w"), opsT0)
			return err
		},
		"a past deadline": func(s *watch.Store) error {
			r := beadReq("ini-a", "w")
			r.Deadline = opsT0.Add(-time.Minute)
			_, err := watchSet(s, (&validateLog{}).ok, "eng6", r, opsT0)
			return err
		},
	}
	for name, try := range cases {
		t.Run(name, func(t *testing.T) {
			s := opsStore(t)
			if err := try(s); err == nil || strings.Contains(err.Error(), "\n") {
				t.Fatalf("err = %v, want one line", err)
			}
			if n := len(s.Snapshot(opsT0)); n != 0 {
				t.Errorf("%d watches stored after a refusal", n)
			}
		})
	}
	t.Run("the cap", func(t *testing.T) {
		s := opsStore(t)
		for i := 0; i < watch.MaxWaitingPerOwner; i++ {
			if _, err := watchSet(s, (&validateLog{}).ok, "eng6", beadReq(fmt.Sprintf("ini-%d", i), "w"), opsT0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := watchSet(s, (&validateLog{}).ok, "eng6", beadReq("ini-over", "w"), opsT0); err == nil {
			t.Fatal("the 21st waiting watch was set")
		}
		if n := len(s.Snapshot(opsT0)); n != watch.MaxWaitingPerOwner {
			t.Errorf("%d stored, want %d", n, watch.MaxWaitingPerOwner)
		}
	})
}

func TestWatchCancel_OwnOnly_OutsideAPaneRefused_IDsNeverReused(t *testing.T) {
	s := opsStore(t)
	id, _ := watchSet(s, (&validateLog{}).ok, "eng6", beadReq("ini-a", "w"), opsT0)
	if _, err := watchCancel(s, "eng4", id, opsT0); err == nil {
		t.Error("eng4 cancelled eng6's watch")
	}
	if _, err := watchCancel(s, "", id, opsT0); err == nil || err != errWatchOutsidePane {
		t.Errorf("outside a pane: %v", err)
	}
	if got := s.Snapshot(opsT0)[0].State; got != watch.Waiting {
		t.Fatalf("after two refusals the watch is %s", got)
	}
	if out, err := watchCancel(s, "eng6", id, opsT0); err != nil || out != "cancelled "+id {
		t.Fatalf("own cancel = %q, %v", out, err)
	}
	if got := s.Snapshot(opsT0)[0].State; got != watch.Cancelled {
		t.Errorf("state = %s, want cancelled", got)
	}
	next, err := watchSet(s, (&validateLog{}).ok, "eng6", beadReq("ini-a", "w"), opsT0)
	if err != nil || next == id {
		t.Errorf("the next watch got %q (%v); ids are never reused", next, err)
	}
}

func TestWatchList_OwnOrAll_WhoMayAskAll(t *testing.T) {
	s := opsStore(t)
	watchSet(s, (&validateLog{}).ok, "eng6", beadReq("ini-a", "mine"), opsT0)
	watchSet(s, (&validateLog{}).ok, "eng4", beadReq("ini-b", "theirs"), opsT0)
	none := func(watch.Watch) bool { return false }
	own, err := watchList(s, "eng6", WatchListRequest{}, opsT0, watchKey, none)
	if err != nil || len(own) != 1 || own[0].Why != "mine" {
		t.Errorf("own list = %+v, %v", own, err)
	}
	for _, asker := range []string{"super", ""} {
		all, err := watchList(s, asker, WatchListRequest{All: true}, opsT0, watchKey, none)
		if err != nil || len(all) != 2 {
			t.Errorf("--all as %q = %d rows, %v", asker, len(all), err)
		}
	}
	if _, err := watchList(s, "eng6", WatchListRequest{All: true}, opsT0, watchKey, none); err == nil {
		t.Error("an engineer listed every watch")
	}
	if _, err := watchList(s, "", WatchListRequest{}, opsT0, watchKey, none); err == nil {
		t.Error("an own list outside any pane was answered")
	}
}

// Two waiting watches on the same check list each other's owners; a watch
// that will wake its sleeping owner says so; an ended watch shares nothing.
func TestWatchList_SharersAndWakes(t *testing.T) {
	s := opsStore(t)
	a, _ := watchSet(s, (&validateLog{}).ok, "eng6", beadReq("ini-a", "mine"), opsT0)
	watchSet(s, (&validateLog{}).ok, "eng4", beadReq("ini-a", "theirs"), opsT0)
	other, _ := watchSet(s, (&validateLog{}).ok, "eng6", beadReq("ini-c", "alone"), opsT0)
	wakes := func(w watch.Watch) bool { return w.ID == other }
	rows, _ := watchList(s, "eng6", WatchListRequest{}, opsT0, watchKey, wakes)
	byID := map[string]WatchListing{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if got := byID[a].Sharers; len(got) != 1 || got[0] != "eng4" {
		t.Errorf("sharers of %s = %v, want [eng4]", a, got)
	}
	if len(byID[other].Sharers) != 0 || !byID[other].Wakes || byID[a].Wakes {
		t.Errorf("rows = %+v", rows)
	}
	watchCancel(s, "eng6", a, opsT0)
	rows, _ = watchList(s, "eng6", WatchListRequest{}, opsT0, watchKey, func(watch.Watch) bool { return true })
	for _, r := range rows {
		if r.ID == a && (len(r.Sharers) != 0 || r.Wakes) {
			t.Errorf("ended %s still shares or wakes: %+v", a, r)
		}
	}
}

// Plain deadlines share nothing: each has its own deadline.
func TestWatchList_PlainDeadlines_ShareNothing(t *testing.T) {
	s := opsStore(t)
	dl := WatchSetRequest{Kind: watch.KindDeadline, Why: "w", Deadline: opsT0.Add(time.Hour)}
	watchSet(s, (&validateLog{}).ok, "eng6", dl, opsT0)
	dl.Deadline = opsT0.Add(2 * time.Hour)
	watchSet(s, (&validateLog{}).ok, "eng4", dl, opsT0)
	rows, _ := watchList(s, "", WatchListRequest{All: true}, opsT0, watchKey, func(watch.Watch) bool { return false })
	for _, r := range rows {
		if len(r.Sharers) != 0 {
			t.Errorf("%s shares with %v", r.ID, r.Sharers)
		}
	}
}
