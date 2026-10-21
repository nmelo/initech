package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// paneListWrites counts, per production file in this package, the
// assignments whose left side is t.panes or an element of it (ini-813u).
func paneListWrites(t *testing.T) map[string]int {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	isPanes := func(e ast.Expr) bool {
		if ix, ok := e.(*ast.IndexExpr); ok {
			e = ix.X
		}
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "panes" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "t"
	}
	out := map[string]int{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok {
				for _, l := range as.Lhs {
					if isPanes(l) {
						out[name]++
					}
				}
			}
			return true
		})
	}
	return out
}

// AC 5, by inventory: no production file in the package writes t.panes
// except pane_list.go, so a new site cannot change the list without the
// window server hearing of it.
func TestPaneList_OnlyTheseFunctionsWriteThePaneList(t *testing.T) {
	writesIn := paneListWrites(t)
	if writesIn["pane_list.go"] == 0 {
		t.Fatal("the census found no writes even in pane_list.go: it is not reading the code")
	}
	for file, n := range writesIn {
		if file != "pane_list.go" {
			t.Errorf("%s writes t.panes directly %d time(s); go through replacePane, appendPane, removePaneAt, movePane, swapPanes or setRemotePanes", file, n)
		}
	}
}

// A peer update may change only other machines' panes: one that would drop or
// add a local pane is refused (it would bypass the window server), and one
// that changes only remote panes is applied.
func TestPaneList_APeerUpdateCannotChangeLocalPanes(t *testing.T) {
	local, other := testPane("eng1"), testPane("eng2")
	remote := &RemotePane{name: "eng1", host: "workbench"}
	tui := f6kgTUI(local, other)

	tui.setRemotePanes([]PaneView{local})
	if len(tui.panes) != 2 {
		t.Fatalf("an update dropping a local pane was applied: %d panes", len(tui.panes))
	}
	tui.setRemotePanes([]PaneView{local, other, testPane("eng3")})
	if len(tui.panes) != 2 {
		t.Fatalf("an update adding a local pane was applied: %d panes", len(tui.panes))
	}
	tui.setRemotePanes([]PaneView{other, remote, local})
	if len(tui.panes) != 3 || tui.panes[1] != PaneView(remote) {
		t.Fatalf("an update changing only remote panes was not applied: %v", tui.panes)
	}
}
