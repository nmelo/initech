// shardtests runs one shard of the full (non -short) test suite, and checks
// that the shards together ran every test exactly once (ini-bus5).
//
//	go run ./scripts/shardtests run -shards 3 -shard 1 -out shard1
//	go run ./scripts/shardtests census -shards 3 shard1 shard2 shard3
//
// # WHY THIS EXISTS
//
// CI's full suite (`make test-full`, `go test ./... -count=1`) was the long
// pole of every run, and every release cut gates on CI. Splitting it across
// runners makes the gate faster only if the split runs NOTHING LESS: a shard
// selector that silently misses a test is a gate with a hole in it, and a
// green run does not witness tests that never ran. So the split carries its
// own census, and the census reads what the shards ran, not what they meant to.
//
// # HOW A TEST FINDS ITS SHARD
//
// Every test, example and fuzz target `go test -list` reports, per package, is
// assigned to a shard by a stable hash of shardSalt plus "package.name". A new
// test therefore lands in exactly one shard by construction and existing tests
// never move. The salt only balances the shards: it is the one that spread the
// measured test durations most evenly (a few tests run 10-26s), and it is
// re-tuned the same way if a shard drifts past the target in shardSalt's
// comment.
// Each shard runs `go test -count=1 -json <pkg> -run '^(names)$'` per
// package: the semantics of make test-full (no -short, the default timeout),
// plus -json so each top-level result is a structured witness. The tool prints
// every event's output, so the log reads like -v. A result is read from the
// JSON event, never from the text: a test that prints a prompt with no newline
// glues "--- PASS" onto the prompt, and a line-start parser misses it (the
// census's first live run caught exactly that, eight tests in cmd). -json runs
// the binary in the same verbose mode as -v, and no test reads
// testing.Verbose, so it changes the output only.
//
// # WHAT THE CENSUS PROVES
//
// From each shard's record: every shard listed the same tests; the shards'
// selections are disjoint and together equal that list; and the tests that
// reported a result are exactly the ones selected, none missing and none
// twice. A shard that selects nothing fails on its own, never passes empty.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// testID names one test: its package import path and its top-level name.
type testID struct{ pkg, name string }

func (t testID) String() string { return t.pkg + " " + t.name }

// shardSalt balances the three shards. Chosen by summing each test's measured
// duration (from a shard run's -json events) per shard for every salt from 0
// to 399 and keeping the one with the smallest maximum. Re-tune it the same
// way when a full-suite shard runs past two and a half minutes; any value is
// correct, since the census proves the partition whatever the salt.
const shardSalt = "304:"

// shardOf returns the 1-based shard of t among n shards.
func shardOf(t testID, n int) int {
	h := fnv.New32a()
	io.WriteString(h, shardSalt+t.pkg+"."+t.name)
	return int(h.Sum32()%uint32(n)) + 1
}

// runnable matches the top-level names go test -run selects: tests, examples
// and fuzz targets (whose seed corpus runs without -fuzz). Benchmarks run only
// under -bench, which make test-full does not pass.
var runnable = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)

// parseList reads `go test -list '.*' ./...` output: each package's names
// come before its "ok" line.
func parseList(r io.Reader) ([]testID, error) {
	var out, pending []testID
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case runnable.MatchString(line):
			pending = append(pending, testID{name: line})
		case strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "ok \t"):
			f := strings.Fields(line)
			if len(f) < 2 {
				return nil, fmt.Errorf("unreadable list line %q", line)
			}
			for _, t := range pending {
				out = append(out, testID{pkg: f[1], name: t.name})
			}
			pending = nil
		case strings.HasPrefix(line, "FAIL"):
			return nil, fmt.Errorf("listing failed: %s", line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("%d names listed with no package line after them", len(pending))
	}
	return out, nil
}

// selectShard returns the tests of shard k among n, grouped by package.
func selectShard(all []testID, n, k int) map[string][]string {
	sel := map[string][]string{}
	for _, t := range all {
		if shardOf(t, n) == k {
			sel[t.pkg] = append(sel[t.pkg], t.name)
		}
	}
	return sel
}

// testArgs is the go test command for one package of a shard: make
// test-full's semantics, -json for the witness events, never -short.
func testArgs(pkg string, names []string) []string {
	return []string{"test", "-count=1", "-json", pkg, "-run", "^(" + strings.Join(names, "|") + ")$"}
}

// testEvent is the part of a go test -json event the tool reads.
type testEvent struct {
	Action string
	Test   string
	Output string
}

// relay copies go test -json events from r: every event's output to w, so
// the log reads like -v, and returns the top-level tests that reported a
// result (pass, fail or skip). Subtests ("Parent/sub") are not top-level.
func relay(r io.Reader, w io.Writer) []string {
	var names []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		var ev testEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			fmt.Fprintln(w, sc.Text()) // not an event: pass it through
			continue
		}
		io.WriteString(w, ev.Output)
		switch ev.Action {
		case "pass", "fail", "skip":
			if ev.Test != "" && !strings.Contains(ev.Test, "/") {
				names = append(names, ev.Test)
			}
		}
	}
	return names
}

func writeIDs(path string, ids []testID) error {
	lines := make([]string, len(ids))
	for i, t := range ids {
		lines[i] = t.String()
	}
	sort.Strings(lines)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func readIDs(path string) ([]testID, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []testID
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("%s: unreadable line %q", path, line)
		}
		out = append(out, testID{f[0], f[1]})
	}
	return out, nil
}

// record is what one shard leaves for the census.
type record struct {
	dir                     string
	all, selected, executed []testID
}

func readRecord(dir string) (record, error) {
	r := record{dir: dir}
	var err error
	if r.all, err = readIDs(filepath.Join(dir, "all.txt")); err != nil {
		return r, err
	}
	if r.selected, err = readIDs(filepath.Join(dir, "selected.txt")); err != nil {
		return r, err
	}
	r.executed, err = readIDs(filepath.Join(dir, "executed.txt"))
	return r, err
}

// census checks the shards' records against each other. It returns every
// problem found, not just the first.
func census(recs []record) []string {
	var probs []string
	if len(recs) == 0 {
		return []string{"no shard records"}
	}
	key := func(ids []testID) string {
		s := make([]string, len(ids))
		for i, t := range ids {
			s[i] = t.String()
		}
		sort.Strings(s)
		return strings.Join(s, "\n")
	}
	want := key(recs[0].all)
	all := map[testID]bool{}
	for _, t := range recs[0].all {
		all[t] = true
	}
	if len(all) == 0 {
		probs = append(probs, "the full test list is empty")
	}
	selectedBy := map[testID][]string{}
	ranIn := map[testID][]string{}
	for _, r := range recs {
		if key(r.all) != want {
			probs = append(probs, fmt.Sprintf("%s listed a different set of tests than %s", r.dir, recs[0].dir))
		}
		if len(r.selected) == 0 {
			probs = append(probs, fmt.Sprintf("%s selected no tests", r.dir))
		}
		for _, t := range r.selected {
			selectedBy[t] = append(selectedBy[t], r.dir)
		}
		for _, t := range r.executed {
			ranIn[t] = append(ranIn[t], r.dir)
		}
	}
	var lines []string
	for t := range all {
		switch n := len(selectedBy[t]); {
		case n == 0:
			lines = append(lines, "selected by no shard: "+t.String())
		case n > 1:
			lines = append(lines, fmt.Sprintf("selected by %d shards (%s): %s", n, strings.Join(selectedBy[t], ", "), t))
		}
		switch n := len(ranIn[t]); {
		case n == 0 && len(selectedBy[t]) > 0:
			lines = append(lines, "selected but never reported a result: "+t.String())
		case n > 1:
			lines = append(lines, fmt.Sprintf("ran %d times (%s): %s", n, strings.Join(ranIn[t], ", "), t))
		}
	}
	for t := range selectedBy {
		if !all[t] {
			lines = append(lines, "selected but not in the full list: "+t.String())
		}
	}
	for t := range ranIn {
		if !all[t] {
			lines = append(lines, "ran but not in the full list: "+t.String())
		}
	}
	sort.Strings(lines)
	return append(probs, lines...)
}

func listAll(pkgs []string) ([]testID, error) {
	args := append([]string{"test", "-list", ".*"}, pkgs...)
	cmd := exec.Command("go", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Stdout.Write(out.Bytes())
		return nil, fmt.Errorf("go test -list: %w", err)
	}
	return parseList(&out)
}

func runShard(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	n := fs.Int("shards", 0, "number of shards")
	k := fs.Int("shard", 0, "this shard, 1-based")
	outDir := fs.String("out", "", "directory for this shard's record")
	fs.Parse(args)
	if *n < 1 || *k < 1 || *k > *n || *outDir == "" {
		return errors.New("run needs -shards N -shard K (1..N) -out DIR")
	}
	if strings.Contains(os.Getenv("GOFLAGS"), "short") {
		return fmt.Errorf("GOFLAGS=%q would make every shard -short; the full suite never runs -short", os.Getenv("GOFLAGS"))
	}
	pkgs := fs.Args()
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	all, err := listAll(pkgs)
	if err != nil {
		return err
	}
	sel := selectShard(all, *n, *k)
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	var selected, executed []testID
	var failed []string
	order := make([]string, 0, len(sel))
	for pkg := range sel {
		order = append(order, pkg)
	}
	sort.Strings(order)
	for _, pkg := range order {
		names := sel[pkg]
		for _, name := range names {
			selected = append(selected, testID{pkg, name})
		}
		a := testArgs(pkg, names)
		fmt.Printf("SHARD %d/%d: go %s  [%d tests]\n", *k, *n, strings.Join(a[:len(a)-1], " ")+" '^(...)$'", len(names))
		cmd := exec.Command("go", a...)
		cmd.Stderr = os.Stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		for _, name := range relay(stdout, os.Stdout) {
			executed = append(executed, testID{pkg, name})
		}
		if err := cmd.Wait(); err != nil {
			failed = append(failed, pkg)
		}
	}
	for path, ids := range map[string][]testID{"all.txt": all, "selected.txt": selected, "executed.txt": executed} {
		if err := writeIDs(filepath.Join(*outDir, path), ids); err != nil {
			return err
		}
	}
	fmt.Printf("SHARD %d/%d: %d of %d tests selected, %d reported a result, %d package(s) failed\n",
		*k, *n, len(selected), len(all), len(executed), len(failed))
	if len(selected) == 0 {
		return fmt.Errorf("shard %d/%d selected no tests: a shard never passes empty", *k, *n)
	}
	if len(failed) > 0 {
		return fmt.Errorf("failing package(s): %s", strings.Join(failed, ", "))
	}
	return nil
}

func runCensus(args []string) error {
	fs := flag.NewFlagSet("census", flag.ExitOnError)
	n := fs.Int("shards", 0, "number of shards expected")
	fs.Parse(args)
	dirs := fs.Args()
	if *n < 1 || len(dirs) != *n {
		return fmt.Errorf("census needs -shards N and exactly N record directories, got %d", len(dirs))
	}
	var recs []record
	for _, d := range dirs {
		r, err := readRecord(d)
		if err != nil {
			return fmt.Errorf("shard record %s: %w", d, err)
		}
		recs = append(recs, r)
	}
	// The witness: every test and the shard that ran it.
	shardOfID := map[testID]string{}
	for _, r := range recs {
		fmt.Printf("CENSUS %s: %d selected, %d reported a result\n", r.dir, len(r.selected), len(r.executed))
		for _, t := range r.executed {
			shardOfID[t] = r.dir
		}
	}
	lines := make([]string, 0, len(recs[0].all))
	for _, t := range recs[0].all {
		lines = append(lines, fmt.Sprintf("  %-12s %s", shardOfID[t], t))
	}
	sort.Strings(lines)
	fmt.Println(strings.Join(lines, "\n"))
	probs := census(recs)
	if len(probs) > 0 {
		for _, p := range probs {
			fmt.Println("CENSUS FAIL:", p)
		}
		return fmt.Errorf("the shards did not run every test exactly once (%d problem(s))", len(probs))
	}
	fmt.Printf("CENSUS OK: %d tests, each selected by exactly one of %d shards and run exactly once\n", len(recs[0].all), *n)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: shardtests run|census ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runShard(os.Args[2:])
	case "census":
		err = runCensus(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shardtests:", err)
		os.Exit(1)
	}
}
