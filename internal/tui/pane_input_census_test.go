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
	"Pane.deliverClean":             "the send path: IPC, forward and drain goroutines",
	"Pane.FlushPaste":               "inside its own goroutine, never on the caller's",
	"Pane.closeOffMain":             "inside the teardown goroutine",
	"TUI.restartOffMain":            "the restart goroutine",
	"TUI.handleIPCStop":             "an IPC connection goroutine",
	"TUI.handleIPCInterrupt":        "an IPC connection goroutine",
	"TUI.injectText":                "an IPC connection goroutine",
	"keystrokePane":                 "an IPC connection goroutine",
	"Pane.maybeRetryWithheldSubmit": "readLoop and the maintenance sweep, with TryLock",
	"Daemon.interruptPane":          "a control stream goroutine",
	"Pane.Close":                    "off the main goroutine: quit, removals and failed starts (the census bans a pane Close on main)",
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

// releaseTerminalCallers are the only functions that may release a pane's
// terminal, and each holds sendMu while it does (ini-tca): a send in flight
// reads ptmx and emu, so a release beside it is a data race.
var releaseTerminalCallers = map[string]string{
	"Pane.Close":              "takes sendMu around it",
	"Pane.closeOffMain":       "inside the teardown goroutine, which takes sendMu",
	"Pane.closeHoldingSendMu": "its caller holds sendMu",
}

func TestCensus_TheTerminalIsReleasedOnlyUnderSendMu(t *testing.T) {
	seen := map[string]bool{}
	inventoryCalls(t, func(fn, file string, _ bool, call *ast.CallExpr) {
		if !strings.HasSuffix(selectorPath(call.Fun), "releaseOnce.Do") {
			return
		}
		seen[fn] = true
		if _, ok := releaseTerminalCallers[fn]; !ok {
			t.Errorf("%s (%s) releases a pane's terminal and is not on the list: it must hold sendMu", fn, file)
		}
	})
	for fn := range releaseTerminalCallers {
		if !seen[fn] {
			t.Errorf("%s is on the list and no longer releases the terminal: remove it", fn)
		}
	}
}

// TestCensus_CloseTakesSendMuAroundTheRelease reads Pane.Close itself: the
// lock is taken before the release, in its own body.
func TestCensus_CloseTakesSendMuAroundTheRelease(t *testing.T) {
	var lockAt, releaseAt token.Pos
	inventoryCalls(t, func(fn, _ string, inFuncLit bool, call *ast.CallExpr) {
		if fn != "Pane.Close" || inFuncLit {
			return
		}
		switch p := selectorPath(call.Fun); {
		case strings.HasSuffix(p, "sendMu.Lock"):
			lockAt = call.Pos()
		case strings.HasSuffix(p, "releaseOnce.Do"):
			releaseAt = call.Pos()
		}
	})
	if !lockAt.IsValid() || !releaseAt.IsValid() || lockAt > releaseAt {
		t.Fatal("Pane.Close does not take sendMu before it releases the terminal")
	}
}

// TestCensus_TheSendPathReadsThePTYThroughItsSnapshot: sendPaneTextLocked never
// reads the pane's ptmx field directly (ini-tca). It goes through ptyHandle or
// hasPTY, under ptyMu, and the writer owns every write.
func TestCensus_TheSendPathReadsThePTYThroughItsSnapshot(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ipc.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "sendPaneTextLocked" {
			continue
		}
		found = true
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "ptmx" {
				t.Errorf("sendPaneTextLocked reads %s.ptmx directly at %s: a close can clear it beside the send",
					selectorPath(s.X), fset.Position(s.Pos()))
			}
			return true
		})
	}
	if !found {
		t.Fatal("sendPaneTextLocked not found in ipc.go: fix the census")
	}
}

// ini-47vy: every message is cleaned before it is typed. The body of a
// message is written only by sendPaneTextLocked, which cleans it before its
// first write; only the send path's two entries reach it; and sendText, the
// entry every sender uses, cleans first so its verdict carries the count.
func TestCensus_EveryMessageIsCleanedBeforeItIsTyped(t *testing.T) {
	callers := map[string]map[string]bool{"sendPaneTextLocked": {}, "bracketedBody": {}, "Pane.writeBody": {}, "cleanMessage": {}}
	var cleanAt, firstWrite token.Pos
	inventoryCalls(t, func(fn, file string, _ bool, call *ast.CallExpr) {
		p := selectorPath(call.Fun)
		name := p
		if strings.HasSuffix(p, ".writeBody") {
			name = "Pane.writeBody"
		}
		if m, ok := callers[name]; ok {
			m[fn] = true
		}
		if fn == "sendPaneTextLocked" {
			if p == "cleanMessage" && cleanAt == 0 {
				cleanAt = call.Pos()
			}
			if name == "Pane.writeBody" && (firstWrite == 0 || call.Pos() < firstWrite) {
				firstWrite = call.Pos()
			}
		}
	})
	want := map[string]string{
		"sendPaneTextLocked": "Pane.deliverClean TUI.injectText",
		// FlushPaste is the operator's own paste into the focused pane: their
		// typing at the keyboard, not a message, and not cleaned.
		"bracketedBody":  "Pane.FlushPaste sendPaneTextLocked",
		"Pane.writeBody": "sendPaneTextLocked",
	}
	for callee, w := range want {
		var got []string
		for fn := range callers[callee] {
			got = append(got, fn)
		}
		sort.Strings(got)
		if g := strings.Join(got, " "); g != w {
			t.Errorf("%s is called from %q, want %q: a message body reaches a child only through sendPaneTextLocked", callee, g, w)
		}
	}
	for _, fn := range []string{"sendPaneTextLocked", "Pane.sendText"} {
		if !callers["cleanMessage"][fn] {
			t.Errorf("%s does not call cleanMessage", fn)
		}
	}
	if cleanAt == 0 || firstWrite == 0 || cleanAt > firstWrite {
		t.Errorf("sendPaneTextLocked cleans at %d and first writes the body at %d: it must clean before it writes", cleanAt, firstWrite)
	}
}
