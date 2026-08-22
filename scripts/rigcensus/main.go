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
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
	verbose := flag.Bool("v", false, "list every gate and where it runs")
	flag.Parse()

	if err := run(*testDir, *workflow, *exemptions, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(testDir, workflow, exemptionsFile string, verbose bool) error {
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

	var uncovered []gate
	covered := map[string]string{}
	for _, g := range gates {
		if where, ok := coveredBy(g, steps); ok {
			covered[g.Env] = where
			continue
		}
		if _, ok := exempt[g.Env]; ok {
			covered[g.Env] = "EXEMPT: " + exempt[g.Env]
			continue
		}
		uncovered = append(uncovered, g)
	}

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

	if len(uncovered) == 0 && len(stale) == 0 {
		fmt.Printf("  OK: %d env-gated rigs, all run by CI or exempted (%d exemption(s), all still applicable)\n",
			len(gates), len(exempt))
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
	if len(stale) > 0 {
		fmt.Fprintf(&b, "\nSTALE EXEMPTION: %s\n", strings.Join(stale, ", "))
		fmt.Fprintf(&b, "Exempted in %s but no such gate exists any more. Remove the line.\n", exemptionsPath)
	}
	return fmt.Errorf("%s", b.String())
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
		envs, skips, calls := scanFunc(fn.Body)
		facts[fn.Name.Name] = &funcFacts{envs: envs, skips: skips, calls: calls}
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

// scanFunc reports the INITECH_* vars a function reads, whether it skips, and
// the functions it calls.
func scanFunc(body *ast.BlockStmt) (envs []string, skips bool, calls []string) {
	seen := map[string]bool{}
	called := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
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
		case "Getenv":
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
	return envs, skips, calls
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
