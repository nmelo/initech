package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func ids(pkg string, names ...string) []testID {
	var out []testID
	for _, n := range names {
		out = append(out, testID{pkg, n})
	}
	return out
}

// fleet is a realistic spread: enough tests that every shard of three gets some.
func fleet() []testID {
	var all []testID
	for i := 0; i < 60; i++ {
		all = append(all, testID{"example.com/a", fmt.Sprintf("TestA%d", i)})
		all = append(all, testID{"example.com/b", fmt.Sprintf("TestB%d", i)})
	}
	return all
}

// honest builds the records three correct shards would leave.
func honest(all []testID, n int) []record {
	var recs []record
	for k := 1; k <= n; k++ {
		r := record{dir: fmt.Sprintf("shard%d", k), all: all}
		for pkg, names := range selectShard(all, n, k) {
			r.selected = append(r.selected, ids(pkg, names...)...)
		}
		r.executed = append([]testID(nil), r.selected...)
		recs = append(recs, r)
	}
	return recs
}

func TestParseList_ReadsNamesPerPackageAndSkipsBenchmarks(t *testing.T) {
	out := "TestOne\nExampleTwo\nFuzzThree\nBenchmarkFour\nok  \texample.com/a\t0.10s\n" +
		"?   \texample.com/empty\t[no test files]\n" +
		"TestFive\nok  \texample.com/b\t(cached)\n"
	got, err := parseList(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	want := append(ids("example.com/a", "TestOne", "ExampleTwo", "FuzzThree"), ids("example.com/b", "TestFive")...)
	if !slices.Equal(got, want) {
		t.Fatalf("parseList = %v, want %v", got, want)
	}
}

func TestParseList_ABuildFailureIsAnError(t *testing.T) {
	if _, err := parseList(strings.NewReader("TestOne\nFAIL\texample.com/a [build failed]\n")); err == nil {
		t.Fatal("a package that failed to build was listed as if it had no tests")
	}
}

// Every test lands in exactly one shard, and a new test does not move the
// others.
func TestSelectShard_PartitionsAndIsStable(t *testing.T) {
	all := fleet()
	seen := map[testID]int{}
	for k := 1; k <= 3; k++ {
		sel := selectShard(all, 3, k)
		if len(sel) == 0 {
			t.Fatalf("shard %d got nothing from %d tests", k, len(all))
		}
		for pkg, names := range sel {
			for _, n := range names {
				seen[testID{pkg, n}]++
			}
		}
	}
	for _, id := range all {
		if seen[id] != 1 {
			t.Fatalf("%s landed in %d shards", id, seen[id])
		}
	}
	shardsOf := func(list []testID) map[testID]int {
		m := map[testID]int{}
		for k := 1; k <= 3; k++ {
			for pkg, names := range selectShard(list, 3, k) {
				for _, n := range names {
					m[testID{pkg, n}] = k
				}
			}
		}
		return m
	}
	before := shardsOf(all)
	after := shardsOf(append([]testID{{"example.com/a", "TestBrandNew"}}, all...))
	for _, id := range all {
		if after[id] != before[id] {
			t.Fatalf("adding one test moved %s from shard %d to %d", id, before[id], after[id])
		}
	}
}

// AC 4: a shard's command never carries -short.
func TestTestArgs_NeverShort(t *testing.T) {
	a := testArgs("example.com/a", []string{"TestOne", "TestTwo"})
	for _, f := range a {
		if strings.Contains(f, "short") {
			t.Fatalf("a shard runs %v", a)
		}
	}
	if !slices.Contains(a, "-count=1") || a[len(a)-1] != "^(TestOne|TestTwo)$" {
		t.Fatalf("shard args %v do not match make test-full's -count=1 with an anchored selector", a)
	}
}

// Results come from the JSON events, not the text: a test that prints a
// prompt with no newline still counts (the census's first live run caught
// eight such tests in cmd), a subtest is not top-level, and text that only
// looks like a result line counts for nothing.
func TestRelay_ReadsTopLevelResultsFromEvents(t *testing.T) {
	ev := func(action, test, output string) string {
		return fmt.Sprintf(`{"Action":%q,"Package":"example.com/a","Test":%q,"Output":%q}`, action, test, output) + "\n"
	}
	in := ev("run", "TestOne", "") + ev("output", "TestOne", "Project name [demo]: ") +
		ev("output", "TestOne", "--- PASS: TestOne (0.00s)\n") + ev("pass", "TestOne", "") +
		ev("pass", "TestOne/sub", "") + ev("fail", "TestTwo", "") + ev("skip", "TestThree", "") +
		ev("output", "TestFour", "--- PASS: TestFake (0.00s)\n") + ev("pass", "TestFour", "") +
		ev("pass", "ExampleFive", "") + ev("pass", "", "") + "not json at all\n"
	var shown strings.Builder
	got := relay(strings.NewReader(in), &shown)
	want := []string{"TestOne", "TestTwo", "TestThree", "TestFour", "ExampleFive"}
	if !slices.Equal(got, want) {
		t.Fatalf("relay = %v, want %v", got, want)
	}
	if !strings.Contains(shown.String(), "Project name [demo]: --- PASS: TestOne") || !strings.Contains(shown.String(), "not json at all") {
		t.Fatalf("relay did not show the output as -v would:\n%s", shown.String())
	}
}

func TestCensus_HonestShardsPass(t *testing.T) {
	if probs := census(honest(fleet(), 3)); len(probs) != 0 {
		t.Fatalf("honest shards failed the census: %v", probs)
	}
}

// AC 3 and feed-it-nothing: each way a shard set can miss or double a test
// fails the census, naming the test.
func TestCensus_FailsOnEveryHole(t *testing.T) {
	victim := func(recs []record) testID { return recs[1].selected[0] }
	cases := map[string]struct {
		mutate func([]record)
		want   string
	}{
		"a shard dropped a test from its selection": {func(r []record) {
			r[1].selected, r[1].executed = r[1].selected[1:], r[1].executed[1:]
		}, "selected by no shard"},
		"two shards selected the same test": {func(r []record) {
			r[0].selected = append(r[0].selected, victim(r))
			r[0].executed = append(r[0].executed, victim(r))
		}, "selected by 2 shards"},
		"a selected test never reported a result": {func(r []record) {
			r[1].executed = r[1].executed[1:]
		}, "never reported a result"},
		"a test ran twice": {func(r []record) {
			r[0].executed = append(r[0].executed, victim(r)) // it also ran in its own shard
		}, "ran 2 times"},
		"a new test outside every shard": {func(r []record) {
			for i := range r {
				r[i].all = append(append([]testID(nil), r[i].all...), testID{"example.com/a", "TestOrphan"})
			}
		}, "selected by no shard: example.com/a TestOrphan"},
		"a shard listed a different suite": {func(r []record) {
			r[2].all = r[2].all[1:]
		}, "listed a different set"},
		"a shard selected nothing": {func(r []record) {
			r[2].selected, r[2].executed = nil, nil
		}, "selected no tests"},
		"a selection outside the full list": {func(r []record) {
			r[0].selected = append(r[0].selected, testID{"example.com/z", "TestGhost"})
		}, "not in the full list"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			recs := honest(fleet(), 3)
			c.mutate(recs)
			probs := census(recs)
			if !slices.ContainsFunc(probs, func(p string) bool { return strings.Contains(p, c.want) }) {
				t.Fatalf("census = %v, want a problem containing %q", probs, c.want)
			}
		})
	}
}

// Feed it nothing: no records, or an empty suite, never passes.
func TestCensus_NothingNeverPasses(t *testing.T) {
	if len(census(nil)) == 0 {
		t.Fatal("no shard records passed the census")
	}
	empty := []record{{dir: "shard1"}, {dir: "shard2"}}
	if len(census(empty)) == 0 {
		t.Fatal("shards that listed and ran nothing passed the census")
	}
}
