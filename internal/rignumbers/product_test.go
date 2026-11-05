package rignumbers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// GatherProduct (ini-61by): the product clone's origin/main, after a bounded
// fetch, and its origin's runs.

type fakeCall struct {
	name     string
	args     []string
	deadline time.Duration // time left on the ctx when called; 0 when none
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []fakeCall
	reply func(name string, args []string) ([]byte, error)
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	c := fakeCall{name: name, args: args}
	if d, ok := ctx.Deadline(); ok {
		c.deadline = time.Until(d)
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	return f.reply(name, args)
}

func (f *fakeRunner) joined() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.name+" "+strings.Join(c.args, " "))
	}
	return out
}

// twoRepos answers as a rig whose first repo is scaffolding and whose
// product clone is /rig/product, on github.com/acme/product.
func twoRepos(t *testing.T, fetchErr error) *fakeRunner {
	t.Helper()
	return &fakeRunner{reply: func(name string, args []string) ([]byte, error) {
		all := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(all, "git -C /rig/product fetch"):
			return nil, fetchErr
		case strings.HasPrefix(all, "git -C /rig/product log origin/main"):
			return []byte("2026-11-05T15:00:00Z\tfeat: product work (ini-1)\n2026-11-05T14:30:00Z\tfix: more product\n"), nil
		case strings.HasPrefix(all, "git -C /rig/product remote get-url origin"):
			return []byte("git@github.com:acme/product.git\n"), nil
		case strings.HasPrefix(all, "gh run list -R acme/product"):
			return []byte(`[{"conclusion":"success","createdAt":"2026-11-05T15:00:00Z","updatedAt":"2026-11-05T15:04:00Z"}]`), nil
		case strings.Contains(all, "scaffold"):
			return []byte(`[]`), nil
		}
		return nil, errors.New("unexpected: " + all)
	}}
}

// The key names the second repo: commits and runs are the product's.
func TestGatherProduct_ReadsTheProductsMainAndRuns(t *testing.T) {
	f := twoRepos(t, nil)
	var in Input
	if err := GatherProduct(context.Background(), f.run, "/rig/product", &in); err != nil {
		t.Fatal(err)
	}
	if !in.HasRepo || !in.CommitsKnown || len(in.Commits) != 2 || in.Commits[0].Subject != "feat: product work (ini-1)" {
		t.Fatalf("commits = %+v known=%v", in.Commits, in.CommitsKnown)
	}
	if !in.Commits[0].When.Equal(time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("commit time = %v", in.Commits[0].When)
	}
	if !in.RunsKnown || len(in.Runs) != 1 || in.Runs[0].Conclusion != "success" {
		t.Fatalf("runs = %+v known=%v", in.Runs, in.RunsKnown)
	}
	calls := f.joined()
	if len(calls) != 4 || !strings.HasPrefix(calls[0], "git -C /rig/product fetch") {
		t.Fatalf("calls = %q, want fetch first, then log, remote, gh runs", calls)
	}
	for _, c := range calls {
		if strings.Contains(c, "scaffold") || strings.HasPrefix(c, "gh api") {
			t.Fatalf("read the wrong repo or the API commits: %q", c)
		}
	}
}

// The bound: every read carries a deadline of at most ReadBudget, asserted
// from the ctx the runner is handed; nothing waits.
func TestGatherProduct_EveryReadIsBoundedByTheReadBudget(t *testing.T) {
	f := twoRepos(t, nil)
	var in Input
	_ = GatherProduct(context.Background(), f.run, "/rig/product", &in)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.deadline <= 0 || c.deadline > ReadBudget {
			t.Errorf("%s %v: deadline %v, want within %v", c.name, c.args, c.deadline, ReadBudget)
		}
	}
}

// A failed fetch leaves the commits unknown, never a stale main.
func TestGatherProduct_AFailedFetchIsUnknownNotStale(t *testing.T) {
	f := twoRepos(t, errors.New("no network"))
	var in Input
	err := GatherProduct(context.Background(), f.run, "/rig/product", &in)
	if err == nil || !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("err = %v, want the fetch named", err)
	}
	if in.CommitsKnown || len(in.Commits) != 0 {
		t.Fatalf("commits read after a failed fetch: %+v", in.Commits)
	}
	for _, c := range f.joined() {
		if strings.Contains(c, " log ") {
			t.Fatalf("read the log after a failed fetch: %q", c)
		}
	}
	if !in.RunsKnown {
		t.Fatal("a failed fetch also hid the runs, which do not need it")
	}
}

// Feed it nothing: an origin that is not on GitHub reads the commits and
// leaves the runs unknown; no dir reads nothing.
func TestGatherProduct_NoGitHubOriginLeavesRunsUnknown(t *testing.T) {
	f := &fakeRunner{reply: func(name string, args []string) ([]byte, error) {
		all := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(all, " fetch "):
			return nil, nil
		case strings.Contains(all, " log "):
			return []byte("2026-11-05T15:00:00Z\tx\n"), nil
		case strings.Contains(all, "get-url"):
			return []byte("https://gitlab.example/acme/product.git\n"), nil
		}
		return nil, errors.New("unexpected: " + all)
	}}
	var in Input
	err := GatherProduct(context.Background(), f.run, "/rig/product", &in)
	if err == nil || in.RunsKnown || !in.CommitsKnown {
		t.Fatalf("err=%v runsKnown=%v commitsKnown=%v", err, in.RunsKnown, in.CommitsKnown)
	}
	for _, c := range f.joined() {
		if strings.HasPrefix(c, "gh ") {
			t.Fatalf("asked gh about a repo not on GitHub: %q", c)
		}
	}
	var none Input
	called := false
	if err := GatherProduct(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}, "", &none); err != nil || called || none.HasRepo {
		t.Fatalf("no dir: err=%v called=%v hasRepo=%v", err, called, none.HasRepo)
	}
}

// A log line it cannot read fails the commits rather than guessing.
func TestGatherProduct_AnUnreadableLogLineIsAnError(t *testing.T) {
	for _, bad := range []string{"not-a-time\tsubject\n", "no tab here\n"} {
		f := &fakeRunner{reply: func(name string, args []string) ([]byte, error) {
			all := name + " " + strings.Join(args, " ")
			switch {
			case strings.Contains(all, " fetch "):
				return nil, nil
			case strings.Contains(all, " log "):
				return []byte(bad), nil
			case strings.Contains(all, "get-url"):
				return []byte("git@github.com:acme/product.git\n"), nil
			}
			return []byte(`[]`), nil
		}}
		var in Input
		if err := GatherProduct(context.Background(), f.run, "/rig/product", &in); err == nil || in.CommitsKnown {
			t.Errorf("%q: err=%v known=%v", bad, err, in.CommitsKnown)
		}
	}
}
