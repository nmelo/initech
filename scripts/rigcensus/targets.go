package main

// Makefile-target census (ini-nvpg): a target that nothing invokes is the
// Makefile's version of a gated rig nobody runs.
//
// THIRD INSTANCE OF ONE CLASS IN A WEEK, which is why this is a second
// inventory for the same tool rather than a new idea. `make test-race` existed
// -- someone wrote it deliberately -- and sat in no gate and not even in
// .PHONY while 7 data races accumulated behind it (ini-4cfl). INITECH_9GVN and
// INITECH_9IMX existed as rigs nothing ran (ini-0lko). The release-asset list
// existed and could not see two shipped artifacts (ini-ojvm). In every case the
// CAPABILITY was written and the INVOCATION was left to memory.
//
// The rule here matches the rig side exactly: derive coverage from the
// inventory, fail when something is uncovered and undeclared, and make an
// exemption state a TRIGGER so "run by hand" names when and by whom.

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultMakefile      = "Makefile"
	targetExemptionsPath = ".github/make-target-exemptions.txt"
)

// targetRe matches a rule line: "name:" or "name: prereqs".
var targetRe = regexp.MustCompile(`^([a-zA-Z0-9_.][a-zA-Z0-9_.-]*):([^=]*)$`)

// makeCallRe matches a make invocation at the START of a recipe command,
// after the tab and any @ or - prefix.
//
// ANCHORED ON PURPOSE. hooks-check ECHOES the string "make install-hooks" as
// advice to the developer, and a scan that matched "make x" anywhere in a
// recipe would read that help text as an invocation -- marking install-hooks
// covered because a message mentions it. A checker that counts documentation
// as coverage is the exact failure this tool exists to detect, one level up.
var makeCallRe = regexp.MustCompile(`^[\t ]*[@-]*\s*(?:\$\(MAKE\)|make)\s+([a-zA-Z0-9_.-]+)`)

// makeInvokeRe finds "make <target>" in a CI step or shell script, where it IS
// a real invocation rather than prose.
var makeInvokeRe = regexp.MustCompile(`(?:^|[;&|\n]|\brun:\s*)\s*(?:\$\(MAKE\)|make)\s+([a-zA-Z0-9_.-]+)`)

type makeTarget struct {
	Name    string
	Prereqs []string
	Calls   []string // targets invoked from this target's recipe
	Line    int
}

// parseMakefile returns every rule in the file.
func parseMakefile(path string) (map[string]*makeTarget, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	out := map[string]*makeTarget{}
	var cur *makeTarget
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "\t") {
			if cur != nil {
				if m := makeCallRe.FindStringSubmatch(line); m != nil {
					cur.Calls = append(cur.Calls, m[1])
				}
			}
			continue
		}
		m := targetRe.FindStringSubmatch(line)
		if m == nil {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
				cur = nil
			}
			continue
		}
		name := m[1]
		// targetRe's first character class ACCEPTS '.', so ".PHONY: a b c"
		// matches and would be inventoried as a target named ".PHONY" whose
		// prerequisites are every phony name. It is a DECLARATION, not a rule.
		// This guard is load-bearing: removing it makes the census report
		// .PHONY itself as an uninvoked target.
		if strings.HasPrefix(name, ".") {
			cur = nil
			continue
		}
		t := &makeTarget{Name: name, Line: i + 1}
		for _, p := range strings.Fields(m[2]) {
			t.Prereqs = append(t.Prereqs, p)
		}
		out[name] = t
		cur = t
	}
	return out, nil
}

// findMakeInvokers returns the targets invoked from outside the Makefile --
// CI workflows and shell scripts. These are the ROOTS of coverage.
func findMakeInvokers(paths []string) (map[string][]string, error) {
	roots := map[string][]string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments describe; they do not invoke. `# Also runs in `make
			// check`` must not make check covered.
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, m := range makeInvokeRe.FindAllStringSubmatch(line, -1) {
				roots[m[1]] = append(roots[m[1]], path)
			}
		}
	}
	return roots, nil
}

// reachable expands the roots over prerequisites and recipe calls.
func reachable(targets map[string]*makeTarget, roots map[string][]string) map[string]string {
	covered := map[string]string{}
	var walk func(name, why string)
	walk = func(name, why string) {
		if _, done := covered[name]; done {
			return
		}
		t, ok := targets[name]
		if !ok {
			return // a file target or an external name; not our inventory
		}
		covered[name] = why
		for _, p := range t.Prereqs {
			walk(p, "prerequisite of "+name)
		}
		for _, c := range t.Calls {
			walk(c, "invoked by "+name)
		}
	}
	names := make([]string, 0, len(roots))
	for n := range roots {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		walk(n, "invoked by "+strings.Join(roots[n], ", "))
	}
	return covered
}

// runTargetCensus fails when a Makefile target exists that nothing invokes and
// no exemption declares.
func runTargetCensus(makefile string, invokerPaths []string, exemptionsFile string, verbose bool) error {
	targets, err := parseMakefile(makefile)
	if err != nil {
		return err
	}
	roots, err := findMakeInvokers(invokerPaths)
	if err != nil {
		return err
	}
	exempt, err := readExemptions(exemptionsFile)
	if err != nil {
		return err
	}
	covered := reachable(targets, roots)

	var uncovered []string
	names := make([]string, 0, len(targets))
	for n := range targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if why, ok := covered[n]; ok {
			if verbose {
				fmt.Printf("  %-24s %s\n", n, why)
			}
			continue
		}
		if reason, ok := exempt[n]; ok {
			if verbose {
				fmt.Printf("  %-24s EXEMPT: %s\n", n, reason)
			}
			continue
		}
		uncovered = append(uncovered, n)
	}

	var stale []string
	for name := range exempt {
		if _, ok := targets[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)

	if len(uncovered) == 0 && len(stale) == 0 {
		fmt.Printf("  OK: %d make targets, all invoked or exempted (%d exemption(s), all still applicable)\n",
			len(targets), len(exempt))
		return nil
	}

	var b strings.Builder
	if len(uncovered) > 0 {
		fmt.Fprintf(&b, "\nMAKE TARGET NOT INVOKED BY ANYTHING.\n\n")
		fmt.Fprintf(&b, "These targets exist but no CI job, script, or other target reaches them.\n")
		fmt.Fprintf(&b, "That is how `make test-race` sat in no gate while 7 data races accumulated\n")
		fmt.Fprintf(&b, "behind it: the capability was written and the invocation was left to memory.\n\n")
		for _, n := range uncovered {
			fmt.Fprintf(&b, "  %s (%s:%d)\n", n, makefile, targets[n].Line)
		}
		fmt.Fprintf(&b, "\nFix by EITHER wiring it into a gate or another target, OR declaring it in\n")
		fmt.Fprintf(&b, "%s with a TRIGGER saying when a human runs it.\n", exemptionsFile)
	}
	if len(stale) > 0 {
		fmt.Fprintf(&b, "\nSTALE TARGET EXEMPTION: %s\n", strings.Join(stale, ", "))
		fmt.Fprintf(&b, "Exempted in %s but no such target exists any more. Remove the line.\n", exemptionsFile)
	}
	return fmt.Errorf("%s", b.String())
}
