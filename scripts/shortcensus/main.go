// shortcensus enforces that every testing.Short() use in the repo is a skip
// guard and nothing else (ini-p2ve.2).
//
//	go run ./scripts/shortcensus [-v]
//
// # WHY THIS EXISTS
//
// CI runs the test suite once, without -short, in the full-suite shards. It
// used to run it a second time with -short in build-and-test, and that run
// was dropped because it repeated work the shards already do: every
// testing.Short() use in the repo was `if testing.Short() { t.Skip(...) }`,
// so -short only removed tests and never changed one, and full-suite-census
// proves every test ran exactly once.
//
// That reasoning holds only while every use stays a skip. A use that makes a
// test LIGHTER under -short (`n := 1000; if testing.Short() { n = 10 }`) has
// a variant only make check runs; a negated one (`if !testing.Short() {
// t.Skip() }`) has a test that runs ONLY under -short, so no CI job runs it
// at all. This census turns the assumption into a check, so the dropped run
// can never hide either one.
//
// # WHAT COUNTS AS A SKIP GUARD
//
// A reference to testing.Short, under whatever name the file imports
// "testing", is accepted only as a call that is the whole condition of an if
// statement, or one operand of a top-level || chain in it, where the if has
// no init statement and no else, and its body is exactly one call to
// X.Skip, X.Skipf or X.SkipNow. With ||, -short can only add skips. Every
// other reference is red and named by file and line: a negation, an &&, an
// else, a body that does anything besides skip, a use outside an if, and
// testing.Short taken as a value. A dot import of "testing" is red too, since
// a bare Short() could not be told apart from another function. So is a tree
// with no use at all: the census would be reading nothing.
//
// Scanned: every .go file under the current directory, skipping testdata,
// vendor and directories starting with "." or "_" (the go tool ignores them).
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// result is one file's audit: the accepted uses and the problems, each as
// "file:line: ...".
type result struct {
	accepted, problems []string
}

// audit checks one file's source. src is passed to parser.ParseFile, so a
// test can feed a string; nil reads path.
func audit(fset *token.FileSet, path string, src any) (result, error) {
	var r result
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return r, err
	}
	name := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p != "testing" {
			continue
		}
		name = "testing"
		if imp.Name != nil {
			name = imp.Name.Name
		}
	}
	at := func(n ast.Node) string { return fset.Position(n.Pos()).String() }
	switch name {
	case "":
		return r, nil
	case ".":
		r.problems = append(r.problems, at(f)+`: dot import of "testing": a bare Short() cannot be audited`)
		return r, nil
	case "_":
		return r, nil
	}
	isShort := func(e ast.Expr) (*ast.SelectorExpr, bool) {
		s, ok := e.(*ast.SelectorExpr)
		if !ok || s.Sel.Name != "Short" {
			return nil, false
		}
		id, ok := s.X.(*ast.Ident)
		return s, ok && id.Name == name
	}
	allowed := map[*ast.SelectorExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Init != nil || ifs.Else != nil || !onlySkips(ifs.Body) {
			return true
		}
		for _, op := range orOperands(ifs.Cond) {
			call, ok := op.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				continue
			}
			if s, ok := isShort(call.Fun); ok {
				allowed[s] = true
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		s, ok := isShort(e)
		if !ok {
			return true
		}
		if allowed[s] {
			r.accepted = append(r.accepted, at(s)+": skip guard")
		} else {
			r.problems = append(r.problems, at(s)+": "+name+".Short is used other than as a skip guard (if "+name+".Short() { t.Skip(...) }, alone or in an || chain, no else, nothing else in the body)")
		}
		return true
	})
	return r, nil
}

// orOperands flattens a top-level || chain, through parentheses, into its
// operands; any other expression is its own single operand.
func orOperands(e ast.Expr) []ast.Expr {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return orOperands(x.X)
	case *ast.BinaryExpr:
		if x.Op == token.LOR {
			return append(orOperands(x.X), orOperands(x.Y)...)
		}
	}
	return []ast.Expr{e}
}

// onlySkips reports whether body is exactly one call to X.Skip, X.Skipf or
// X.SkipNow.
func onlySkips(body *ast.BlockStmt) bool {
	if len(body.List) != 1 {
		return false
	}
	es, ok := body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Skip", "Skipf", "SkipNow":
		return true
	}
	return false
}

// goFiles lists the .go files under root the go tool would see.
func goFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if path != root && (n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// census audits every file under root and returns the accepted uses and the
// problems, including the empty-tree one.
func census(root string) (result, error) {
	var all result
	files, err := goFiles(root)
	if err != nil {
		return all, err
	}
	fset := token.NewFileSet()
	for _, p := range files {
		r, err := audit(fset, p, nil)
		if err != nil {
			return all, err
		}
		all.accepted = append(all.accepted, r.accepted...)
		all.problems = append(all.problems, r.problems...)
	}
	if len(all.accepted)+len(all.problems) == 0 {
		all.problems = append(all.problems, fmt.Sprintf("no testing.Short() use found in %d .go file(s) under %s: the census is reading nothing", len(files), root))
	}
	return all, nil
}

func main() {
	verbose := flag.Bool("v", false, "list every accepted use")
	flag.Parse()
	r, err := census(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "shortcensus:", err)
		os.Exit(2)
	}
	if *verbose {
		for _, a := range r.accepted {
			fmt.Println("  " + a)
		}
	}
	if len(r.problems) > 0 {
		for _, p := range r.problems {
			fmt.Println("FAIL:", p)
		}
		fmt.Printf("FAIL: %d testing.Short() use(s) are not skip guards. CI runs no -short job, so a lighter or short-only variant would never run there.\n", len(r.problems))
		os.Exit(1)
	}
	fmt.Printf("  OK: %d testing.Short() use(s), every one a skip guard\n", len(r.accepted))
}
