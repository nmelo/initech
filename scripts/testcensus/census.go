package main

// census.go holds the comparison logic, kept free of I/O so it can be tested
// directly. A guardrail whose own logic is untested is theater (ini-2x8.8).

import (
	"fmt"
	"sort"
	"strings"
)

// Census maps a platform (GOOS) to the set of TEST FILES the toolchain compiles
// for it, keyed by repo-relative path.
//
// FILES, not individual tests, because that is where the mechanism lives: build
// constraints are per-file, so a file dropping off a platform takes every test
// in it. Keying on files makes one exemption line describe one real decision
// ("this file drives a PTY") instead of scattering the same fact across the
// sixty test names that happen to live in it.
type Census map[string]map[string]bool

// Exemption declares that a test is allowed to be absent from some platforms.
//
// Reason is REQUIRED. An exemption without a stated reason is rejected rather
// than honoured: the whole failure mode being guarded against is a constraint
// nobody consciously chose, so an undocumented exemption reproduces the bug in
// the file meant to prevent it.
type Exemption struct {
	Key       string          // "package\tTestName"
	Platforms map[string]bool // GOOS values the test may be absent from
	Reason    string
	Line      int
}

// Problem is one undeclared cross-platform difference.
type Problem struct {
	Key     string
	Present []string // platforms where the test exists
	Absent  []string // platforms where it does not, and is not exempted
}

func (p Problem) String() string {
	return fmt.Sprintf("%s\n    compiled on: %s\n    ABSENT from: %s",
		p.Key, strings.Join(p.Present, ", "), strings.Join(p.Absent, ", "))
}

// Compare reports tests that exist on some platforms and not others without a
// declared exemption, plus exemptions that no longer apply.
//
// Stale exemptions are REPORTED rather than ignored. An exemption that has
// outlived its cause is a standing licence for a suite to vanish from a
// platform, which is precisely the hole this check exists to close -- and it is
// the form the hole takes AFTER someone has already fixed the underlying
// constraint.
func Compare(c Census, exemptions []Exemption) (problems []Problem, stale []Exemption) {
	if len(c) < 2 {
		return nil, nil
	}

	platforms := make([]string, 0, len(c))
	for p := range c {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)

	exBy := make(map[string]Exemption, len(exemptions))
	for _, e := range exemptions {
		exBy[e.Key] = e
	}
	used := make(map[string]map[string]bool) // key -> platforms the exemption actually covered

	all := make(map[string]bool)
	for _, tests := range c {
		for k := range tests {
			all[k] = true
		}
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var present, absent []string
		for _, p := range platforms {
			if c[p][k] {
				present = append(present, p)
				continue
			}
			if e, ok := exBy[k]; ok && e.Platforms[p] {
				if used[k] == nil {
					used[k] = map[string]bool{}
				}
				used[k][p] = true
				continue
			}
			absent = append(absent, p)
		}
		if len(absent) > 0 && len(present) > 0 {
			problems = append(problems, Problem{Key: k, Present: present, Absent: absent})
		}
	}

	for _, e := range exemptions {
		// Stale two ways: the test is gone entirely, or it now compiles on every
		// platform the exemption excused it from.
		if !all[e.Key] {
			stale = append(stale, e)
			continue
		}
		covered := used[e.Key]
		everUsed := false
		for p := range e.Platforms {
			if covered[p] {
				everUsed = true
				break
			}
		}
		if !everUsed {
			stale = append(stale, e)
		}
	}
	return problems, stale
}

// ParseExemptions reads the exemption file.
//
// Format, one per line:
//
//	<package> <TestName> <goos>[,<goos>...] # <reason>
//
// Blank lines and full-line comments are ignored. A missing or empty reason is
// an ERROR, not a warning.
func ParseExemptions(src string) ([]Exemption, []error) {
	var out []Exemption
	var errs []error

	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		body, reason, hasReason := strings.Cut(line, "#")
		reason = strings.TrimSpace(reason)
		if !hasReason || reason == "" {
			errs = append(errs, fmt.Errorf("line %d: exemption has no reason; an undocumented exemption is the bug this file exists to prevent: %q", lineNo, line))
			continue
		}

		fields := strings.Fields(body)
		if len(fields) != 2 {
			errs = append(errs, fmt.Errorf("line %d: want '<test file path> <goos>[,<goos>] # reason', got %q", lineNo, strings.TrimSpace(body)))
			continue
		}

		platforms := map[string]bool{}
		for _, p := range strings.Split(fields[1], ",") {
			if p = strings.TrimSpace(p); p != "" {
				platforms[p] = true
			}
		}
		if len(platforms) == 0 {
			errs = append(errs, fmt.Errorf("line %d: no platforms listed", lineNo))
			continue
		}

		out = append(out, Exemption{
			Key:       fields[0],
			Platforms: platforms,
			Reason:    reason,
			Line:      lineNo,
		})
	}
	return out, errs
}
