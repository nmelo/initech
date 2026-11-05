package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ceilingSpawnExempt are the functions that may start an agent's process
// without asking the machine ceiling first, each with its reason. Adding one
// loosens the ceiling: it is not one engineer's call.
var ceilingSpawnExempt = map[string]string{
	// The wrapper itself: its one caller, resumePane, asks before calling it.
	"spawnPane": "the spawn wrapper; resumePane asks the gate before it",
	// The serve daemon runs a rig's agents headless on another machine for a
	// window elsewhere; the ceiling counts window 1s (ruling (2)).
	"RunDaemon":       "the serve daemon, outside the window-1 ceiling",
	"startPushedPane": "the serve daemon, outside the window-1 ceiling",
}

// spawnCalls are the calls that start an agent's process.
var spawnCalls = map[string]bool{"NewPane": true, "spawnPane": true}

// ceilingBypasses reads sources (file name to text) and returns every
// function that starts an agent's process before asking admitAgent and is
// not exempt, and every function that starts one at all.
func ceilingBypasses(t *testing.T, sources map[string]string) (bypass []string, seen map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	seen = map[string]bool{}
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, sources[name], 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			first, gate := token.NoPos, token.NoPos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				callee := ""
				switch c := call.Fun.(type) {
				case *ast.Ident:
					callee = c.Name
				case *ast.SelectorExpr:
					callee = c.Sel.Name
				}
				if spawnCalls[callee] && (first == token.NoPos || call.Pos() < first) {
					first = call.Pos()
				}
				if callee == "admitAgent" && (gate == token.NoPos || call.Pos() < gate) {
					gate = call.Pos()
				}
				return true
			})
			if first == token.NoPos {
				continue
			}
			seen[fn.Name.Name] = true
			if _, ok := ceilingSpawnExempt[fn.Name.Name]; ok {
				continue
			}
			if gate == token.NoPos || gate > first {
				bypass = append(bypass, fset.Position(first).String()+" in "+fn.Name.Name)
			}
		}
	}
	sort.Strings(bypass)
	return bypass, seen
}

// TestCeilingCensus_NoWakePathBypassesTheGate reads every non-test file of
// the package: every function that starts an agent's process (NewPane,
// spawnPane) calls admitAgent before the first such call, or is exempt
// above with its reason. A new start or wake path that skips the ceiling
// fails here.
func TestCeilingCensus_NoWakePathBypassesTheGate(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = string(src)
	}
	bypass, seen := ceilingBypasses(t, sources)
	for _, b := range bypass {
		t.Errorf("starts an agent without asking the machine ceiling first: %s", b)
	}
	// The census must have found the paths it guards, or it read nothing.
	for _, want := range []string{"resumePaneDrain", "handleIPCStart", "restartOffMain", "addPane", "Run"} {
		if !seen[want] {
			t.Errorf("census found no spawn in %s: it is not reading the package", want)
		}
	}
	for name := range ceilingSpawnExempt {
		if !seen[name] {
			t.Errorf("exempt %s no longer spawns: remove it from ceilingSpawnExempt", name)
		}
	}
}

// The census's positive twin: a spawn with no gate, and one whose gate
// comes after it, are both reported; one asked first is not.
func TestCeilingCensus_FiresOnALateGate(t *testing.T) {
	src := `package tui
func (t *TUI) noGate() { NewPane(PaneConfig{}, 1, 1) }
func (t *TUI) lateGate() { t.spawnPane(PaneConfig{}, 1, 1); t.admitAgent(nil, "a", "b") }
func (t *TUI) gated() { if t.admitAgent(nil, "a", "b") != nil { return }; NewPane(PaneConfig{}, 1, 1) }
`
	bypass, seen := ceilingBypasses(t, map[string]string{"x.go": src})
	got := strings.Join(bypass, "\n")
	if len(bypass) != 2 || !strings.Contains(got, "in noGate") || !strings.Contains(got, "in lateGate") {
		t.Fatalf("bypasses = %q, want noGate and lateGate", bypass)
	}
	if !seen["gated"] {
		t.Fatal("the gated spawn was not read at all")
	}
}
