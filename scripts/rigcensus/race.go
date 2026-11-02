package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// raceFlagRe matches -race as its own go test flag, never as part of a longer
// word such as test-race or a -run value.
var raceFlagRe = regexp.MustCompile(`(^|\s)-race(\s|$)`)

// runRace is the race half of the census: every env-gated rig CI runs must also
// run under the race detector in a step the gate sees, or be named in the race
// exemption file with a reason and a trigger.
//
// WHY A SECOND PASS OVER THE SAME STEPS: a rig is gated, so a go test -race with
// no gate set skips it, and the race job's green says nothing about it. The
// detector and the gate have to be in one step, the same both-halves shape as
// env and selector, and nothing asserted that until this did.
//
// A rig exempted from CI altogether is outside this pass: that file already
// names it with a reason, and a rig that does not run cannot run under -race. A
// step behind a dispatch input does not count, because no nightly and no cut
// sees it; such a rig stays exempt here until the step joins the gate.
func runRace(testDir, workflow, exemptionsFile, raceExemptionsFile string, verbose bool) error {
	gates, err := findGates(testDir)
	if err != nil {
		return err
	}
	var gated []ciStep
	for _, wf := range strings.Split(workflow, ",") {
		steps, err := findCISteps(wf)
		if err != nil {
			return err
		}
		for _, s := range steps {
			if s.Race && !s.OnDemand {
				gated = append(gated, s)
			}
		}
	}
	notRun, err := readExemptions(exemptionsFile)
	if err != nil {
		return err
	}
	exempt, err := readExemptions(raceExemptionsFile)
	if err != nil {
		return err
	}

	var uncovered, redundant []string
	known := map[string]bool{}
	for _, g := range gates {
		known[g.Env] = true
		if _, skip := notRun[g.Env]; skip {
			continue
		}
		where, isRun := coveredBy(g, gated)
		reason, isExempt := exempt[g.Env]
		switch {
		case isRun && isExempt:
			redundant = append(redundant, fmt.Sprintf("%s (runs at %s)", g.Env, where))
		case isRun:
			if verbose {
				fmt.Printf("  %-24s race: %s\n", g.Env, where)
			}
		case isExempt:
			if verbose {
				fmt.Printf("  %-24s race: EXEMPT: %s\n", g.Env, reason)
			}
		default:
			uncovered = append(uncovered, g.Env)
		}
	}
	var stale []string
	for env := range exempt {
		if !known[env] {
			stale = append(stale, env)
		}
	}
	sort.Strings(uncovered)
	sort.Strings(redundant)
	sort.Strings(stale)

	if len(uncovered)+len(redundant)+len(stale) == 0 {
		fmt.Printf("  OK: every rig CI runs is run under -race by the gate or exempted (%d race exemption(s))\n", len(exempt))
		return nil
	}
	var b strings.Builder
	if len(uncovered) > 0 {
		fmt.Fprintf(&b, "\nENV-GATED RIG NOT RUN UNDER -race BY THE GATE: %s\n", strings.Join(uncovered, ", "))
		fmt.Fprintf(&b, "A go test -race without the gate set skips the rig. Set the gate and select the\n")
		fmt.Fprintf(&b, "rig in a -race step that no dispatch input holds back, or name it in %s\n", raceExemptPath)
		fmt.Fprintf(&b, "with the reason and a trigger.\n")
	}
	if len(redundant) > 0 {
		fmt.Fprintf(&b, "\nRACE EXEMPTION FOR A RIG THE GATE RUNS UNDER -race: %s\nRemove the line from %s.\n",
			strings.Join(redundant, ", "), raceExemptPath)
	}
	if len(stale) > 0 {
		fmt.Fprintf(&b, "\nSTALE RACE EXEMPTION: %s\nNo such gate exists any more. Remove the line from %s.\n",
			strings.Join(stale, ", "), raceExemptPath)
	}
	return fmt.Errorf("%s", b.String())
}
