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

// ini-7rd: nothing on the main goroutine blocks on a pane, and one place writes
// to a child. Both are properties of every call site, present and future, so
// they are checked by inventory, as lock_discipline_test.go checks the
// emulator's lock: each site must be on a list with its goroutine stated, a new
// one fails here, and an entry whose function no longer has the site fails too.

// sendMuTakers lists every function that takes a pane's send lock, and why it
// is not the main goroutine. A site inside a func literal counts as off main
// only when the entry says so.
var sendMuTakers = map[string]string{
	"Pane.sendText":                 "the send path: IPC, forward and drain goroutines",
	"Pane.FlushPaste":               "inside its own goroutine, never on the caller's",
	"Pane.closeOffMain":             "inside the teardown goroutine",
	"TUI.restartOffMain":            "the restart goroutine",
	"TUI.handleIPCStop":             "an IPC connection goroutine",
	"TUI.handleIPCInterrupt":        "an IPC connection goroutine",
	"TUI.injectText":                "an IPC connection goroutine",
	"keystrokePane":                 "an IPC connection goroutine",
	"Pane.maybeRetryWithheldSubmit": "readLoop and the maintenance sweep, with TryLock",
	"Daemon.interruptPane":          "a control stream goroutine",
}

// goroutineOnlySendMuTakers must take the lock only inside a func literal: their
// own body runs on the main goroutine.
var goroutineOnlySendMuTakers = map[string]bool{"Pane.FlushPaste": true, "Pane.closeOffMain": true}

// mainPathFuncs run on the main goroutine, or are called straight from it. None
// may take sendMu, write to a child or close a pane in its own body.
var mainPathFuncs = []string{
	"parkPaneSuspended", "TUI.parkPaneRecorded", "TUI.suspendAgent", "TUI.restartPane",
	"TUI.handleTopKey", "Pane.FlushPaste", "Pane.SendKey", "Pane.SendPaste", "Pane.ForwardMouse",
	"Pane.ForwardWheel", "Pane.closeOffMain", "Pane.beginClose", "Pane.markClosing",
	"Pane.responseLoop", "Pane.writeInput",
}

// funcKey names a function declaration: Recv.Name, or Name.
func funcKey(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	t := d.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + d.Name.Name
	}
	return d.Name.Name
}

// inventoryCalls calls visit for every call in the package's non-test files,
// with the enclosing declared function and whether a func literal is between
// them.
func inventoryCalls(t *testing.T, visit func(fn, file string, inFuncLit bool, call *ast.CallExpr)) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			key := funcKey(fd)
			var walk func(n ast.Node, lit bool)
			walk = func(n ast.Node, lit bool) {
				ast.Inspect(n, func(c ast.Node) bool {
					if c == n {
						return true
					}
					switch v := c.(type) {
					case *ast.FuncLit:
						walk(v.Body, true)
						return false
					case *ast.CallExpr:
						visit(key, f, lit, v)
					}
					return true
				})
			}
			walk(fd.Body, false)
		}
	}
}

// selectorPath renders a.b.c for a call's function, or "" for anything else.
func selectorPath(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if x := selectorPath(v.X); x != "" {
			return x + "." + v.Sel.Name
		}
	}
	return ""
}

func TestCensus_OnlyTheDrainWritesToAChild(t *testing.T) {
	var sites []string
	inventoryCalls(t, func(fn, file string, _ bool, call *ast.CallExpr) {
		p := selectorPath(call.Fun)
		if strings.HasSuffix(p, "ptmx.Write") || strings.HasSuffix(p, "ptmx.WriteString") {
			sites = append(sites, fn)
		}
		// The drain's own write goes through a local copy of the field.
		if p == "ptmx.Write" && fn != "Pane.writeChild" {
			t.Errorf("%s (%s) writes to a child directly: every write goes through the input queue", fn, file)
		}
	})
	sort.Strings(sites)
	if got := strings.Join(sites, " "); got != "Pane.writeChild" {
		t.Fatalf("child write sites are %q, want exactly Pane.writeChild", got)
	}
}

func TestCensus_NothingOnTheMainGoroutineTakesSendMu(t *testing.T) {
	seen := map[string]bool{}
	inventoryCalls(t, func(fn, file string, inFuncLit bool, call *ast.CallExpr) {
		p := selectorPath(call.Fun)
		if !strings.HasSuffix(p, "sendMu.Lock") && !strings.HasSuffix(p, "sendMu.TryLock") {
			return
		}
		seen[fn] = true
		if _, ok := sendMuTakers[fn]; !ok {
			t.Errorf("%s (%s) takes sendMu and is not on the list: say which goroutine it runs on, "+
				"and it must not be the main one", fn, file)
		}
		if goroutineOnlySendMuTakers[fn] && !inFuncLit {
			t.Errorf("%s takes sendMu in its own body, which runs on the main goroutine", fn)
		}
	})
	for fn := range sendMuTakers {
		if !seen[fn] {
			t.Errorf("%s is on the list and no longer takes sendMu: remove it", fn)
		}
	}
}

func TestCensus_TheMainPathsDoNotBlockOnAPane(t *testing.T) {
	found := map[string]bool{}
	mainPath := map[string]bool{}
	for _, f := range mainPathFuncs {
		mainPath[f] = true
	}
	inventoryCalls(t, func(fn, file string, inFuncLit bool, call *ast.CallExpr) {
		if !mainPath[fn] {
			return
		}
		found[fn] = true
		if inFuncLit {
			return
		}
		p := selectorPath(call.Fun)
		for _, banned := range []string{"sendMu.Lock", "ptmx.Write", "ptmx.Close", "goWg.Wait", "cmd.Wait", "sendMu.TryLock"} {
			if strings.HasSuffix(p, banned) {
				t.Errorf("%s calls %s in its own body: on the main goroutine that can freeze the rig", fn, p)
			}
		}
		// A pane's blocking Close, by any receiver name.
		if strings.HasSuffix(p, ".Close") && (strings.Contains(p, "pane") || strings.HasPrefix(p, "p.Close") || strings.HasPrefix(p, "fp.")) {
			t.Errorf("%s calls %s in its own body: use closeOffMain", fn, p)
		}
	})
	for _, fn := range mainPathFuncs {
		if !found[fn] {
			t.Errorf("%s is on the main-path list and was not found, or calls nothing: fix the list", fn)
		}
	}
}
