// rigcensus enforces that every env-gated rig in the test suite is actually RUN
// by CI, or was exempted out loud (ini-0lko).
//
//	go run ./scripts/rigcensus
//
// # WHY THIS EXISTS
//
// The composed-rigs job names its rigs twice: once in an `env:` block and once
// in a `-run` selector. Both lists are hand-maintained, and a rig is only run
// when BOTH mention it. Miss either and the job reports green while the rig
// never executes -- which is not a hypothetical:
//
//   - INITECH_9IMX, the rig that caught the wake-delivery regression which
//     would otherwise have shipped in v2.11.1, had NEVER been in CI.
//   - INITECH_9GVN was written, gated, and never wired.
//   - INITECH_35AK was wired into `env:` by its author and left out of the
//     selector, so the job would have reported green while never running it.
//     A human caught that one by reading the diff.
//
// This is the same silent-omission class the project bought three times in one
// week (networkSink, idleWithBeadThreshold, fleetNum): a hand-maintained list
// that must be remembered, with no artifact when it is not. The fix is the same
// one test-census applies to cross-platform file parity -- DERIVE the answer
// from the inventory instead of trusting a list.
//
// # WHAT COUNTS AS A GATE
//
// A test function that reads INITECH_* and calls t.Skip when it is unset. That
// shape is the gate; a bare Getenv with no Skip is a config knob
// (INITECH_RIG_ARTIFACTS picks an output directory) and is correctly ignored.
//
// THE GATE IS OFTEN NOT IN THE TEST FUNCTION. requireNotifProbe(t) is a helper
// that reads the env and skips on the test's behalf, and the first version of
// this tool -- which scanned only Test function bodies -- went blind to it and
// reported OK. It found its own gap only because the stale-exemption check
// fired on a line I had written for a gate it could not see. So the scan
// follows calls to a fixpoint within the file: a test is gated by whatever its
// helpers gate it by. A checker narrower than its own claim manufactures
// exactly the false confidence it was built to remove.
//
// The shape does NOT distinguish a rig from a helper subprocess: a test that
// re-execs itself as a child (INITECH_FOLDBACK_HELPER) skips exactly the same
// way. That ambiguity is not resolved by cleverness -- it is resolved by the
// exemption file, where a human states which it is and why. An exemption is a
// decision with a reason attached; a heuristic is a guess that fails silently
// the first time someone writes a gate that does not match it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultTestDir  = "internal/tui"
	defaultWorkflow = ".github/workflows/ci.yml"
	exemptionsPath  = ".github/rig-census-exemptions.txt"
	quarantinePath  = ".github/rig-quarantine.txt"
	gateEnvPrefix   = "INITECH_"
)

// gate is one env-gated rig: the variable and the tests it gates.
type gate struct {
	Env   string
	Tests []string
	File  string
}

// ciStep is one workflow step's coverage claim: the env it sets and the test
// selector it runs.
type ciStep struct {
	Job      string
	Name     string
	Env      map[string]string
	Selector string // the -run value; empty means "runs everything"
	HasRun   bool
}

func main() {
	testDir := flag.String("dir", defaultTestDir, "directory of gated tests")
	workflow := flag.String("workflow", defaultWorkflow, "CI workflow to read coverage from")
	exemptions := flag.String("exemptions", exemptionsPath, "path to the exemption file")
	quarantineF := flag.String("quarantine", quarantinePath, "path to the quarantine file")
	verbose := flag.Bool("v", false, "list every gate and where it runs")
	flag.Parse()

	if err := run(*testDir, *workflow, *exemptions, *quarantineF, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Second inventory, same rule (ini-nvpg): a target nothing invokes is a
	// gated rig nobody runs.
	if err := runTargetCensus(defaultMakefile,
		[]string{*workflow, ".github/workflows/release.yml", "scripts/hooks/pre-commit"},
		targetExemptionsPath, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(testDir, workflow, exemptionsFile, quarantineFile string, verbose bool) error {
	gates, err := findGates(testDir)
	if err != nil {
		return err
	}
	steps, err := findCISteps(workflow)
	if err != nil {
		return err
	}
	exempt, err := readExemptions(exemptionsFile)
	if err != nil {
		return err
	}
	quar, err := readQuarantine(quarantineFile)
	if err != nil {
		return err
	}
	var problems []string

	var uncovered []gate
	covered := map[string]string{}
	for _, g := range gates {
		where, isRun := coveredBy(g, steps)
		q, isQuar := quar[g.Env]
		_, isExempt := exempt[g.Env]

		if isQuar && isExempt {
			problems = append(problems, fmt.Sprintf("%s is BOTH exempted and quarantined. Those "+
				"are different states -- exempt means CI does not run it, quarantined means CI "+
				"runs it and ignores the result -- and a rig cannot be both. Pick one.", g.Env))
			continue
		}

		if isQuar {
			// A quarantined rig that is not actually wired is just an
			// exemption with a bead stapled to it. The whole point of the
			// state is that the rig RUNS and its failure stays visible.
			if !isRun {
				problems = append(problems, fmt.Sprintf("%s is quarantined but NO CI step runs it. "+
					"Quarantine means WIRED, RUNNING, NOT GATING -- if it does not run, its "+
					"failure is invisible and the entry is an exemption in disguise. Wire it "+
					"(env block AND -run selector, in a step that does not gate) or exempt it "+
					"honestly.", g.Env))
				continue
			}
			open, known, detail := beadStateFn(q.Bead)
			switch {
			case !known:
				covered[g.Env] = fmt.Sprintf("QUARANTINED against %s (%s) — runs at %s, does not gate. "+
					"BEAD STATE NOT VERIFIED HERE: %s", q.Bead, detail, where, detail)
			case !open:
				problems = append(problems, fmt.Sprintf("%s is quarantined against %s, which is %s. "+
					"A quarantine outlives its reason the moment its bead does -- either the rig "+
					"is fixed and the entry goes, or the bead should not be closed.",
					g.Env, q.Bead, detail))
			default:
				covered[g.Env] = fmt.Sprintf("QUARANTINED against %s (%s) — runs at %s, does not gate",
					q.Bead, detail, where)
			}
			continue
		}

		if isRun {
			covered[g.Env] = where
			continue
		}
		if isExempt {
			covered[g.Env] = "EXEMPT: " + exempt[g.Env]
			continue
		}
		uncovered = append(uncovered, g)
	}

	// A quarantine entry for a gate that no longer exists is stale for the
	// same reason a stale exemption is: it states a decision about something
	// that is not there, and the next reader trusts it.
	for env := range quar {
		found := false
		for _, g := range gates {
			if g.Env == env {
				found = true
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("%s is quarantined but no such gate exists any "+
				"more. Remove the line from %s.", env, quarantinePath))
		}
	}
	sort.Strings(problems)

	if verbose {
		names := make([]string, 0, len(covered))
		for k := range covered {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Printf("  %-24s %s\n", n, covered[n])
		}
	}

	// A stale exemption is its own defect: it claims a decision about something
	// that no longer exists, and the next reader trusts it. Same rule
	// test-census applies to its own list.
	var stale []string
	for env := range exempt {
		found := false
		for _, g := range gates {
			if g.Env == env {
				found = true
				break
			}
		}
		if !found {
			stale = append(stale, env)
		}
	}
	sort.Strings(stale)

	if len(uncovered) == 0 && len(stale) == 0 && len(problems) == 0 {
		fmt.Printf("  OK: %d env-gated rigs, all run by CI, exempted or quarantined "+
			"(%d exemption(s), %d quarantine(d), all still applicable)\n",
			len(gates), len(exempt), len(quar))
		return nil
	}

	var b strings.Builder
	if len(uncovered) > 0 {
		fmt.Fprintf(&b, "\nENV-GATED RIG NOT RUN BY CI.\n\n")
		fmt.Fprintf(&b, "These rigs exist and are gated, but no CI step both SETS their env and\n")
		fmt.Fprintf(&b, "SELECTS their tests -- so they never execute, and the job reports green\n")
		fmt.Fprintf(&b, "without them. That is how INITECH_9IMX went un-run for its whole life\n")
		fmt.Fprintf(&b, "while catching a regression by hand.\n\n")
		for _, g := range uncovered {
			fmt.Fprintf(&b, "  %s (%s)\n", g.Env, g.File)
			for _, t := range g.Tests {
				fmt.Fprintf(&b, "      %s\n", t)
			}
		}
		fmt.Fprintf(&b, "\nFix by EITHER wiring it into %s -- the env block AND the -run selector,\n", defaultWorkflow)
		fmt.Fprintf(&b, "both, since either alone is a green job that runs nothing -- OR declaring\n")
		fmt.Fprintf(&b, "it in %s with a REASON, so the next reader finds a decision\n", exemptionsPath)
		fmt.Fprintf(&b, "instead of an accident.\n")
	}
	for _, pr := range problems {
		fmt.Fprintf(&b, "\nQUARANTINE PROBLEM: %s\n", pr)
	}
	if len(stale) > 0 {
		fmt.Fprintf(&b, "\nSTALE EXEMPTION: %s\n", strings.Join(stale, ", "))
		fmt.Fprintf(&b, "Exempted in %s but no such gate exists any more. Remove the line.\n", exemptionsPath)
	}
	return fmt.Errorf("%s", b.String())
}

// quarantine is a rig that RUNS but does not GATE, because it fails for a
// KNOWN product reason that has its own open bead.
//
// IT IS NOT A LOOSER EXEMPTION AND MUST NOT BORROW ITS VOCABULARY. An exempt
// rig does not run at all; a quarantined one runs on every push, prints its
// failure into the log, and is held off the build's verdict. The two answer
// different questions and collapsing them would turn the exemption file into
// the place failing rigs go to be forgotten -- which is the precise thing that
// file exists to prevent.
//
// The property that makes quarantine honest rather than a hole is that it
// CANNOT OUTLIVE ITS TICKET SILENTLY: every entry names a bead, and the census
// fails when that bead is missing or closed. A plain temporary exemption has no
// such property, which is why one was refused here (super's ruling, ini-0lko).
type quarantine struct {
	Env    string
	Bead   string
	Reason string
}

// beadRe matches the bead id a quarantine entry must name.
var beadRe = regexp.MustCompile(`BEAD:\s*([a-z]+-[a-z0-9]+)`)

// readQuarantine parses "ENV  # BEAD: <id> reason" lines.
func readQuarantine(path string) (map[string]quarantine, error) {
	out := map[string]quarantine{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		env, reason, found := strings.Cut(line, "#")
		env = strings.TrimSpace(env)
		reason = strings.TrimSpace(reason)
		if env == "" {
			continue
		}
		if !found || reason == "" {
			return nil, fmt.Errorf("%s: quarantine %q has no reason", path, env)
		}
		m := beadRe.FindStringSubmatch(reason)
		if m == nil {
			return nil, fmt.Errorf("%s: quarantine %q names no bead. Every quarantine entry must "+
				"carry \"BEAD: <id>\" -- a quarantine without a ticket is an exemption wearing a "+
				"different word, and it is the ticket that stops it outliving its reason", path, env)
		}
		out[env] = quarantine{Env: env, Bead: m[1], Reason: reason}
	}
	return out, nil
}

// findBeadsDir walks up from the working directory looking for a .beads
// directory. Returns "" when there is none.
func findBeadsDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, ".beads")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// beadStateFn is the bead lookup, swappable so tests can exercise the
// closed/missing/unverifiable branches without a beads database present.
var beadStateFn = beadState

// beadState asks bd whether the named bead exists and is still open.
//
// known=false means bd could not be consulted at all (it is not installed --
// CI runners have no beads database, which lives outside this repo). That is
// reported out loud rather than passed silently: an unverifiable check that
// prints "OK" is worse than one that says it did not run. The enforcing venue
// is `make check`, which every commit passes through via the pre-commit hook.
func beadState(id string) (open, known bool, detail string) {
	if _, err := exec.LookPath("bd"); err != nil {
		return false, false, "bd not on PATH"
	}
	cmd := exec.Command("bd", "show", id, "--json")
	// The beads database lives ABOVE this repo (the agent workspace owns it,
	// the source checkout does not), so bd's own upward discovery stops at the
	// git root and finds nothing. Point it at the first .beads we find walking
	// up. Without this the check fails to RUN and, read carelessly, a failure
	// to run looks exactly like a failing bead.
	if dir := findBeadsDir(); dir != "" {
		cmd.Env = append(os.Environ(), "BEADS_DIR="+dir)
	}
	out, err := cmd.Output()
	if err != nil {
		// bd exists but could not answer. That is NOT evidence the bead is
		// closed or missing -- it is evidence we did not measure. Reporting it
		// as a bad bead would be the "couldn't measure" / "measured bad"
		// conflation this tool exists to stop elsewhere.
		return false, false, "bd could not answer (" + err.Error() + ")"
	}
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) == 0 {
		return false, true, "no such bead"
	}
	st := rows[0].Status
	return st != "closed", true, st
}

// findGates parses every _test.go in dir and returns the gates it finds.
func findGates(dir string) ([]gate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	byEnv := map[string]*gate{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		// ParseFile ignores build constraints, which is what we want: a rig
		// hidden behind //go:build !windows is still a rig CI must run
		// somewhere. Whether it can run on a given PLATFORM is test-census's
		// question, not this one.
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for name, gates := range gatesByTest(f) {
			for _, env := range gates {
				g := byEnv[env]
				if g == nil {
					g = &gate{Env: env, File: e.Name()}
					byEnv[env] = g
				}
				g.Tests = append(g.Tests, name)
			}
		}
	}
	out := make([]gate, 0, len(byEnv))
	for _, g := range byEnv {
		sort.Strings(g.Tests)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Env < out[j].Env })
	return out, nil
}

// funcFacts is what one function does on its own.
type funcFacts struct {
	envs  []string
	skips bool
	calls []string
	// condSkipCalls are functions called inside the CONDITION of an if whose
	// BODY skips (ini-9jch): `if readsEnv() != "1" { t.Skip(...) }` where
	// readsEnv is a separate helper that only returns the value. This is the
	// structural signal that a call's return value feeds THIS skip, without
	// requiring readsEnv to skip itself -- the shape a plain "does F skip"
	// flag on F cannot express.
	condSkipCalls []string
}

// gatesByTest resolves each Test function's gates, following calls to helpers
// in the same file until the answer stops changing.
func gatesByTest(f *ast.File) map[string][]string {
	facts := map[string]*funcFacts{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		envs, skips, calls, condSkipCalls := scanFunc(fn.Body)
		facts[fn.Name.Name] = &funcFacts{envs: envs, skips: skips, calls: calls, condSkipCalls: condSkipCalls}
	}

	// A function called inside a skip-guarding IF-CONDITION is known, by that
	// structural position, to feed the skip decision -- so its reachable envs
	// are folded into the enclosing function's OWN envs directly, regardless
	// of whether that helper skips itself. This is deliberately narrower than
	// "credit everything reachable from a skipping test": it only pulls in
	// envs from a call that sits where a skip predicate sits, so a config
	// knob read elsewhere in the same function (INITECH_RIG_ARTIFACTS picking
	// an output directory, not gating anything) is untouched. The first,
	// broader version of this fix DID credit everything reachable and it
	// mis-flagged three real config knobs against the actual repo inventory
	// (INITECH_9IMX_SHELL, INITECH_RIG_ARTIFACTS, INITECH_X5OB_ARTIFACTS) --
	// pinned in TestGatesByTest_UnrelatedHelperOutsideTheConditionStaysAKnob
	// so that regression cannot come back silently.
	for _, fx := range facts {
		for _, c := range fx.condSkipCalls {
			fx.envs = append(fx.envs, allReachableEnvs(c, facts, map[string]bool{})...)
		}
	}

	// Skipping propagates through calls: a test that calls requireNotifProbe
	// skips because the helper does.
	for changed := true; changed; {
		changed = false
		for _, fx := range facts {
			if fx.skips {
				continue
			}
			for _, c := range fx.calls {
				if cal, ok := facts[c]; ok && cal.skips {
					fx.skips = true
					changed = true
					break
				}
			}
		}
	}

	// A function contributes its envs only if it (or something it calls) skips.
	gatesOf := map[string][]string{}
	var resolve func(name string, seen map[string]bool) []string
	resolve = func(name string, seen map[string]bool) []string {
		fx, ok := facts[name]
		if !ok || seen[name] {
			return nil
		}
		seen[name] = true
		var out []string
		if fx.skips {
			out = append(out, fx.envs...)
		}
		for _, c := range fx.calls {
			out = append(out, resolve(c, seen)...)
		}
		return out
	}
	for name := range facts {
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		uniq := map[string]bool{}
		var envs []string
		for _, e := range resolve(name, map[string]bool{}) {
			if !uniq[e] {
				uniq[e] = true
				envs = append(envs, e)
			}
		}
		if len(envs) > 0 {
			sort.Strings(envs)
			gatesOf[name] = envs
		}
	}
	return gatesOf
}

// allReachableEnvs walks every function reachable from name and returns their
// combined envs, UNCONDITIONALLY -- unlike resolve() in gatesByTest, it does
// not require each function on the path to skip itself. Only called from a
// condSkipCalls entry, whose caller has already established (structurally,
// by the call's position inside a skip-guarding if-condition) that this walk
// is relevant; it is not applied to a test's whole call tree, which is what
// keeps a config knob read by an unrelated helper from being credited too.
func allReachableEnvs(name string, facts map[string]*funcFacts, seen map[string]bool) []string {
	fx, ok := facts[name]
	if !ok || seen[name] {
		return nil
	}
	seen[name] = true
	out := append([]string(nil), fx.envs...)
	for _, c := range fx.calls {
		out = append(out, allReachableEnvs(c, facts, seen)...)
	}
	return out
}

// bodySkips reports whether a block calls t.Skip/Skipf/SkipNow anywhere
// within it, including nested statements.
func bodySkips(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Skip", "Skipf", "SkipNow":
			found = true
		}
		return true
	})
	return found
}

// scanFunc reports the INITECH_* vars a function reads, whether it skips, the
// functions it calls, and which of those calls sit inside the CONDITION of an
// if whose body skips.
func scanFunc(body *ast.BlockStmt) (envs []string, skips bool, calls []string, condSkipCalls []string) {
	seen := map[string]bool{}
	called := map[string]bool{}
	condCalled := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok && ifs.Body != nil && bodySkips(ifs.Body) {
			// THE THREE-HOP SHAPE (ini-9jch): `if readsEnv() != "1" {
			// t.Skip(...) }` where readsEnv is a separate helper that only
			// returns the value. readsEnv never skips itself, so the
			// per-function skip flag elsewhere in this file cannot see it --
			// this is the one place that structural position (called where a
			// skip predicate sits) stands in for real dataflow analysis.
			var conds []ast.Node
			if ifs.Cond != nil {
				conds = append(conds, ifs.Cond)
			}
			if ifs.Init != nil {
				conds = append(conds, ifs.Init)
			}
			for _, cnode := range conds {
				ast.Inspect(cnode, func(cn ast.Node) bool {
					if call, ok := cn.(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok && !condCalled[id.Name] {
							condCalled[id.Name] = true
							condSkipCalls = append(condSkipCalls, id.Name)
						}
					}
					return true
				})
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			if !called[id.Name] {
				called[id.Name] = true
				calls = append(calls, id.Name)
			}
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Skip", "Skipf", "SkipNow":
			skips = true
		case "Getenv", "LookupEnv":
			if len(call.Args) != 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.HasPrefix(v, gateEnvPrefix) {
				return true
			}
			if !seen[v] {
				seen[v] = true
				envs = append(envs, v)
			}
		}
		return true
	})
	sort.Strings(envs)
	return envs, skips, calls, condSkipCalls
}

// runSelectorRe pulls the -run value out of a step's shell command.
var runSelectorRe = regexp.MustCompile(`-run\s+'([^']*)'|-run\s+"([^"]*)"|-run\s+(\S+)`)

// findCISteps reads the workflow and returns every step that runs go test.
func findCISteps(path string) ([]ciStep, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []ciStep
	jobs := make([]string, 0, len(wf.Jobs))
	for j := range wf.Jobs {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	for _, j := range jobs {
		for _, s := range wf.Jobs[j].Steps {
			if !strings.Contains(s.Run, "go test") && !strings.Contains(s.Run, "make test") {
				continue
			}
			st := ciStep{Job: j, Name: s.Name, Env: s.Env}
			if m := runSelectorRe.FindStringSubmatch(s.Run); m != nil {
				st.HasRun = true
				for _, g := range m[1:] {
					if g != "" {
						st.Selector = g
						break
					}
				}
			}
			// -short skips every gated rig by construction, so a step that
			// passes it cannot cover one however its env is set.
			if strings.Contains(s.Run, "-short") {
				continue
			}
			out = append(out, st)
		}
	}
	return out, nil
}

// coveredBy reports whether some CI step both sets the gate's env and selects
// at least one of its tests. BOTH, because either alone runs nothing.
func coveredBy(g gate, steps []ciStep) (string, bool) {
	for _, s := range steps {
		if _, ok := s.Env[g.Env]; !ok {
			continue
		}
		if !s.HasRun {
			return fmt.Sprintf("%s / %s (no -run: runs everything)", s.Job, s.Name), true
		}
		re, err := regexp.Compile(s.Selector)
		if err != nil {
			continue
		}
		for _, t := range g.Tests {
			if re.MatchString(t) {
				return fmt.Sprintf("%s / %s", s.Job, s.Name), true
			}
		}
	}
	return "", false
}

// readExemptions parses "ENV  # reason" lines.
func readExemptions(path string) (map[string]string, error) {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		env, reason, found := strings.Cut(line, "#")
		env = strings.TrimSpace(env)
		if env == "" {
			continue
		}
		reason = strings.TrimSpace(reason)
		if !found || reason == "" {
			return nil, fmt.Errorf("%s: exemption %q has no reason. An exemption without a reason is "+
				"an accident with a comment character in it", path, env)
		}
		// super's condition on the ini-0lko ruling, enforced rather than
		// remembered: "dropped" decays into "forgotten" the moment a line says
		// only WHY CI skips it and not WHEN a human runs it instead. Requiring
		// the trigger in the parser means the next person to drop a rig cannot
		// leave that half out quietly.
		if !strings.Contains(reason, "TRIGGER:") {
			return nil, fmt.Errorf("%s: exemption %q states no run trigger. Add \"TRIGGER: <when, and who>\" "+
				"to the reason -- a dropped rig with no trigger is a forgotten one", path, env)
		}
		out[env] = reason
	}
	return out, nil
}
