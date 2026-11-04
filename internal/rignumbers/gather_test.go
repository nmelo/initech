package rignumbers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSlug_ReadsGitHubURLs(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/nmelo/initech":     "nmelo/initech",
		"https://github.com/nmelo/initech.git": "nmelo/initech",
		"git@github.com:nmelo/initech.git":     "nmelo/initech",
	} {
		if got, ok := Slug(url); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", url, got, ok, want)
		}
	}
	for _, url := range []string{"", "https://gitlab.com/a/b", "/local/path"} {
		if got, ok := Slug(url); ok {
			t.Errorf("%q gave %q", url, got)
		}
	}
}

// fakeGH answers the two repo reads from canned JSON and counts its calls.
type fakeGH struct {
	calls    []string
	commits  string
	runs     string
	failWith error
}

func (f *fakeGH) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.failWith != nil {
		return nil, f.failWith
	}
	if len(args) > 0 && args[0] == "api" {
		return []byte(f.commits), nil
	}
	return []byte(f.runs), nil
}

const commitsJSON = `[{"commit":{"message":"fix(tui): a thing (ini-aaa)\n\nbody","committer":{"date":"2026-11-04T17:55:00Z"}}},
{"commit":{"message":"docs: a sentence","committer":{"date":"2026-11-04T16:00:00Z"}}}]`

const runsJSON = `[{"conclusion":"success","createdAt":"2026-11-04T17:50:00Z","updatedAt":"2026-11-04T17:52:00Z"},
{"conclusion":"failure","createdAt":"2026-11-04T17:40:00Z","updatedAt":"2026-11-04T17:46:00Z"}]`

func TestGatherRepo_ReadsCommitsAndRunsInTwoCalls(t *testing.T) {
	f := &fakeGH{commits: commitsJSON, runs: runsJSON}
	in := Input{Now: now}
	if err := GatherRepo(context.Background(), f.run, "nmelo/initech", &in); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || !strings.Contains(f.calls[0], "repos/nmelo/initech/commits") || !strings.Contains(f.calls[1], "--event push") {
		t.Fatalf("calls %q", f.calls)
	}
	n := Compute(in)
	intIs(t, "merges/h", n.MergesPerHour, 1)
	intIs(t, "gate red", n.GateRed, 1)
	if in.Commits[0].Subject != "fix(tui): a thing (ini-aaa)" {
		t.Errorf("subject %q, want the first line only", in.Commits[0].Subject)
	}
}

// gh unreachable: an error to log once, both reads unknown.
func TestGatherRepo_GHUnreachableLeavesTheNumbersUnknown(t *testing.T) {
	f := &fakeGH{failWith: errors.New("could not resolve host")}
	in := Input{Now: now}
	err := GatherRepo(context.Background(), f.run, "nmelo/initech", &in)
	if err == nil || !strings.Contains(err.Error(), "could not resolve host") {
		t.Fatalf("err %v", err)
	}
	if in.CommitsKnown || in.RunsKnown || !in.HasRepo {
		t.Fatalf("input %+v", in)
	}
}

// A rig with no repo reads nothing.
func TestGatherRepo_NoSlugReadsNothing(t *testing.T) {
	f := &fakeGH{}
	in := Input{Now: now, HasRepo: true}
	if err := GatherRepo(context.Background(), f.run, "", &in); err != nil || len(f.calls) != 0 || in.HasRepo {
		t.Fatalf("err %v calls %q input %+v", err, f.calls, in)
	}
}
