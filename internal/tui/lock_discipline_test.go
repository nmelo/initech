package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ini-psjt: the UI main loop must never wait on a pane's emulator lock. That
// is a property of every call site, present and future, so it is checked the
// way the rig census checks CI coverage -- by inventory, not by vigilance.
//
// This test parses every non-test file in the package and finds each call
// that takes the SafeEmulator's lock: a locking method called on a value named
// emu (p.emu, fp.emu, old.emu, a parameter named emu), a call to one of the
// blocking screen readers, or a reach for the unlocked embedded Emulator
// handle. Each must sit inside a function on the allowlist below, with the
// goroutine it runs on stated. A new main-loop caller fails here and has to
// be routed through screen_read.go; an allowlist entry whose function no
// longer locks fails too, so the list cannot rot.
//
// Convention the scan relies on: a SafeEmulator variable is named emu, and
// the unlocked handle a region hands out is named e. Aliasing a SafeEmulator
// under another name would hide a call from the scan; do not.

// lockedEmulatorMethods are SafeEmulator methods that take its lock.
var lockedEmulatorMethods = map[string]bool{
	"Width": true, "Height": true, "CellAt": true, "CellValueAt": true, "RowText": true,
	"ScrollbackLen": true, "ScrollbackCellAt": true, "ScrollbackCellValueAt": true,
	"CursorPosition": true, "IsAltScreen": true, "Render": true, "Touched": true,
	"Scrollback": true, "Write": true, "Resize": true, "SetCell": true, "Draw": true,
	"SetScrollbackSize": true, "ClearScrollback": true, "Lock": true, "TryRows": true,
}

// blockingScreenReaders wait for the lock by design; their callers are part
// of the inventory.
var blockingScreenReaders = map[string]bool{
	"emuRowsBlocking": true, "emulatorBottomTextBlocking": true,
}

// lockingCallAllowlist maps an enclosing function ("Type.method" or "func")
// to the goroutine it runs on and why waiting there is acceptable.
var lockingCallAllowlist = map[string]string{
	// The writer side. readLoop is the goroutine that holds the lock in
	// hover's cycle; it may wait on itself.
	"Pane.readLoop":                 "readLoop goroutine: the writer itself",
	"Pane.checkAltScreenTransition": "readLoop goroutine, under renderMu after Write",
	"Pane.resizeLocked":             "readLoop (alt-screen transition) and daemon Resize; the main loop uses resizeFromMainLoop",
	"Pane.ResizeExact":              "daemon control goroutine (headless sizing)",

	// The send path SHOULD wait for the screen: a send that cannot read the
	// composer must not guess. Never the main loop (sendMu, off-main).
	"emuRowsBlocking":            "the blocking primitive",
	"emulatorBottomTextBlocking": "the blocking primitive",
	"composerTailAt":             "send goroutine (injectText under sendMu)",
	"composerBlock":              "send goroutine (injectText under sendMu)",
	"paneShowsModalOnScreen":     "send goroutine (paneHasModal before a send)",
	"paneShowsIdleComposer":      "send goroutine (modal queue drain)",
	"Pane.isCodexReadyForSend":   "send goroutine (codex readiness poll)",
	"Pane.waitForCodexReady":     "send goroutine (codex readiness poll)",

	// The region owners: they take the lock with a bounded try and hand out
	// the unlocked handle. This is where the guarantee lives.
	"withEmulator":         "screen_read.go: bounded TryRLock",
	"Pane.withScreenWrite": "screen_read.go: bounded TryLock",

	// RemotePane's viewer emulator is written only on the main goroutine
	// (DrainData); no other goroutine ever holds its lock, so a locked read
	// there cannot wait.
	"RemotePane.writeEmu":   "main goroutine only; no cross-goroutine holder",
	"RemotePane.Render":     "main goroutine only; no cross-goroutine holder",
	"RemotePane.Resize":     "main goroutine only; no cross-goroutine holder",
	"RemotePane.sendResize": "main goroutine only; no cross-goroutine holder",
}

type lockingCall struct {
	file, fn, what string
	line           int
}

// scanLockingCalls returns every locking emulator call in the package's
// non-test sources with its enclosing function.
func scanLockingCalls(t *testing.T) []lockingCall {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []lockingCall
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				name = receiverTypeName(fd.Recv.List[0].Type) + "." + name
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					switch fun := x.Fun.(type) {
					case *ast.SelectorExpr:
						if lockedEmulatorMethods[fun.Sel.Name] && endsInEmu(fun.X) {
							out = append(out, lockingCall{file, name, "emu." + fun.Sel.Name + "()", fset.Position(x.Pos()).Line})
						}
					case *ast.Ident:
						if blockingScreenReaders[fun.Name] {
							out = append(out, lockingCall{file, name, fun.Name + "()", fset.Position(x.Pos()).Line})
						}
					}
				case *ast.SelectorExpr:
					if x.Sel.Name == "Emulator" && endsInEmu(x.X) {
						out = append(out, lockingCall{file, name, "emu.Emulator (unlocked handle)", fset.Position(x.Pos()).Line})
					}
				}
				return true
			})
		}
	}
	return out
}

func receiverTypeName(e ast.Expr) string {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}

// endsInEmu reports whether e is `emu` or `<anything>.emu`.
func endsInEmu(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name == "emu"
	case *ast.SelectorExpr:
		return x.Sel.Name == "emu"
	}
	return false
}

func TestLockDiscipline_EveryLockingEmulatorCallIsAllowlisted(t *testing.T) {
	calls := scanLockingCalls(t)
	if len(calls) == 0 {
		t.Fatal("scan found no locking emulator calls at all; the instrument is broken, not the code clean")
	}
	seen := map[string]bool{}
	var unlisted []string
	for _, c := range calls {
		seen[c.fn] = true
		if _, ok := lockingCallAllowlist[c.fn]; !ok {
			unlisted = append(unlisted, fmt.Sprintf("%s:%d %s in %s", c.file, c.line, c.what, c.fn))
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("locking emulator calls outside the allowlist (ini-psjt). The main loop may not wait on a pane: "+
			"route through withScreen/tryScreenRows in screen_read.go, or add the function to lockingCallAllowlist "+
			"with the goroutine it runs on:\n  %s", strings.Join(unlisted, "\n  "))
	}
	var stale []string
	for fn := range lockingCallAllowlist {
		if !seen[fn] {
			stale = append(stale, fn)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("allowlist entries with no locking call left in them (remove, so the list cannot rot): %v", stale)
	}
}

// The scan must see through the shapes the inventory relies on; a scanner
// that misses `p.emu.Height()` would pass an empty allowlist.
func TestLockDiscipline_ScanSeesTheShapesItClaimsTo(t *testing.T) {
	calls := scanLockingCalls(t)
	want := map[string]bool{
		"Pane.readLoop":       false, // p.emu.Write
		"emuRowsBlocking":     false, // emu.RowText on a parameter named emu
		"composerTailAt":      false, // emuRowsBlocking(...)
		"Pane.resizeLocked":   false, // p.emu.Emulator handle
		"RemotePane.writeEmu": false, // rp.emu.Write
	}
	for _, c := range calls {
		if _, ok := want[c.fn]; ok {
			want[c.fn] = true
		}
	}
	for fn, hit := range want {
		if !hit {
			t.Errorf("scan did not see the locking call in %s", fn)
		}
	}
}
