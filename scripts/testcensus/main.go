// testcensus enforces that the test suite is the SAME SUITE on every platform
// CI covers, or that each difference was chosen out loud (ini-2x8.8).
//
//	go run ./scripts/testcensus
//	go run ./scripts/testcensus -platforms linux,darwin,windows
//
// # WHY THIS EXISTS
//
// A build constraint removes tests from a platform silently, and CI still
// reports that platform green -- a green run does not witness suites that never
// ran. This project hit that four times before adding this check, the last time
// at SUITE granularity: two whole attention suites were absent from Windows
// while every leg reported success.
//
// The shape was the same every time. ONE file legitimately needs a constraint
// (it drives a PTY, say). A portable symbol -- a const, a helper -- is parked in
// that file. Every consumer of the symbol must then carry the constraint too,
// and their consumers after them. Nobody chose it, no review catches it, and the
// only artifact is an absence.
//
// # WHY IT IS STATIC, AND WHY THAT MATTERS
//
// The census comes from `go list`, which applies build constraints without
// compiling or running anything. So one machine can compute every platform's
// test-file set, and this runs at COMMIT time in `make check` rather than only
// in CI.
//
// That placement is the point. The standing asymmetry on this project is that
// test-TIME platform failures belong to CI -- cross-compiled tests cannot run,
// and no Windows runner exists at commit time. But this class is not test-time:
// whether a file is COMPILED for a platform is answerable statically, on any
// machine, before the push. It belongs with `GOOS=windows go vet`, not with the
// CI matrix.
//
// # A WITNESS MAKES ABSENCE VISIBLE; THIS MAKES IT FAIL
//
// The CI step that lists test names per leg is the honest interim and stays
// useful for reading, but it needs a human to notice. This does not.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const exemptionsPath = ".github/test-census-exemptions.txt"

func main() {
	platforms := flag.String("platforms", "linux,darwin,windows", "comma-separated GOOS values to compare")
	exemptions := flag.String("exemptions", exemptionsPath, "path to the exemption file")
	verbose := flag.Bool("v", false, "list every test file per platform")
	flag.Parse()

	if err := run(strings.Split(*platforms, ","), *exemptions, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "\ntestcensus: "+err.Error())
		os.Exit(1)
	}
}

func run(platforms []string, exemptionsFile string, verbose bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	census := Census{}
	for _, goos := range platforms {
		goos = strings.TrimSpace(goos)
		if goos == "" {
			continue
		}
		files, err := testFilesFor(goos, root)
		if err != nil {
			return fmt.Errorf("census for %s: %w", goos, err)
		}
		if len(files) == 0 {
			// An empty census would exempt every file on that platform by
			// making it look like nothing was ever expected there.
			return fmt.Errorf("no test files found for GOOS=%s -- refusing to compare against an empty census", goos)
		}
		census[goos] = files
	}
	if len(census) < 2 {
		return fmt.Errorf("need at least 2 platforms to compare, got %d", len(census))
	}

	var exemptions []Exemption
	raw, err := os.ReadFile(exemptionsFile)
	switch {
	case err == nil:
		var parseErrs []error
		exemptions, parseErrs = ParseExemptions(string(raw))
		if len(parseErrs) > 0 {
			for _, e := range parseErrs {
				fmt.Fprintln(os.Stderr, "  "+e.Error())
			}
			return fmt.Errorf("%s has %d malformed entr(ies)", exemptionsFile, len(parseErrs))
		}
	case !os.IsNotExist(err):
		return err
	}

	names := make([]string, 0, len(census))
	for p := range census {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		fmt.Printf("  %-8s %d test files\n", p, len(census[p]))
		if verbose {
			keys := make([]string, 0, len(census[p]))
			for k := range census[p] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Println("      " + k)
			}
		}
	}

	problems, stale := Compare(census, exemptions)

	for _, s := range stale {
		fmt.Printf("\nSTALE EXEMPTION (%s line %d): %s\n    reason given: %s\n"+
			"    It is no longer absent where this excuses it. Remove the line -- a stale exemption\n"+
			"    is a standing licence for that file to vanish again unnoticed.\n",
			exemptionsFile, s.Line, s.Key, s.Reason)
	}

	if len(problems) > 0 {
		fmt.Printf("\n%d test file(s) compiled on some platforms and not others, undeclared:\n\n", len(problems))
		for _, p := range problems {
			fmt.Println("  " + p.String())
			fmt.Println()
		}
		return fmt.Errorf(`CROSS-PLATFORM TEST CENSUS MISMATCH.

A test file that compiles on one platform and not another is usually a build
constraint that propagated somewhere nobody intended: a portable symbol parked
in a constrained file drags every consumer out with it, and CI still reports the
short-changed platform green, because a green run does not witness tests that
never ran.

If the constraint propagated, fix it -- move the portable half into an untagged
file. If the absence is genuine, declare it in
%s with a REASON,
so the next person reads a decision instead of an accident`, exemptionsPath)
	}

	if len(stale) > 0 {
		return fmt.Errorf("%d stale exemption(s) in %s", len(stale), exemptionsFile)
	}

	fmt.Printf("  OK: same test files on all %d platforms (%d declared exemption(s), all still applicable)\n",
		len(names), len(exemptions))
	return nil
}

// testFilesFor returns the repo-relative test files the toolchain compiles for
// goos. Uses `go list`, so build constraints are applied by the toolchain itself
// rather than by re-implementing constraint parsing here -- the one place where
// a second implementation would be guaranteed to drift from the real answer.
func testFilesFor(goos, root string) (map[string]bool, error) {
	cmd := exec.Command("go", "list", "-e",
		"-f", "{{.Dir}}\t{{join .TestGoFiles \",\"}}\t{{join .XTestGoFiles \",\"}}", "./...")
	cmd.Env = append(os.Environ(), "GOOS="+goos)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}

	files := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		dir := parts[0]
		for _, group := range parts[1:] {
			for _, f := range strings.Split(group, ",") {
				if f == "" {
					continue
				}
				rel, err := filepath.Rel(root, filepath.Join(dir, f))
				if err != nil {
					continue
				}
				files[filepath.ToSlash(rel)] = true
			}
		}
	}
	return files, nil
}

func repoRoot() (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("locate module root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
