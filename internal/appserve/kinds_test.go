package appserve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ini-r0q.19 AC2: every message kind the service sends is named in the
// operator guide's conversation paragraph, and Kinds is exactly the Kind…
// constants transcript.go declares. Adding a kind without its guide line, or
// without listing it in Kinds, goes red.
func TestKinds_TheGuideNamesEveryKindTheServiceSends(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "transcript.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, sp := range g.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if !strings.HasPrefix(n.Name, "Kind") || i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					declared = append(declared, v)
				}
			}
		}
	}
	listed := append([]string(nil), Kinds...)
	sort.Strings(declared)
	sort.Strings(listed)
	if len(declared) == 0 || strings.Join(declared, ",") != strings.Join(listed, ",") {
		t.Fatalf("Kind constants %v, Kinds %v: every declared kind must be listed, and only those", declared, listed)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "operator-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	var para string
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "`GET /v1/conversation/") {
			para = ln
		}
	}
	if para == "" {
		t.Fatal("the guide has no conversation paragraph")
	}
	for _, k := range Kinds {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("the guide's conversation paragraph does not name the kind %q", k)
		}
	}
	if !strings.Contains(para, "every kind the service sends") || !strings.Contains(para, "plain text") {
		t.Error("the paragraph does not say these are every kind and what the app does with another")
	}
}
