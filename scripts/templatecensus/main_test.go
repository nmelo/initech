package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The census's own tests. The MISS cases matter more than the hit case: a
// verb removed from the CLI reddens this tool by definition (the lookup is a
// set membership test), while a mention the scanner never SAW is silent —
// which is the failure mode the whole tool exists to remove. So most of what
// follows plants mentions in shapes the extractor could plausibly skip.

// writeConstFile writes a Go file declaring one string const with body, and
// returns its directory.
func writeConstFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	// An interpreted string literal, not a raw one: the bodies under test
	// contain backticks and fenced blocks, which cannot appear inside a raw
	// literal — the real templates splice them (` + "`" + `) for the same
	// reason. strconv.Quote handles every shape uniformly.
	src := "package roles\n\nconst SomeTemplate = " + strconv.Quote(body) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "templates.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func verbsFound(t *testing.T, body string) map[string]int {
	t.Helper()
	ms, err := scanMentions(writeConstFile(t, body))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	out := map[string]int{}
	for _, m := range ms {
		out[m.verb]++
	}
	return out
}

// THE MISS CASES. Each is a shape where a narrower extractor reports green on
// a real defect.
func TestScanMentions_FindsAVerbInEveryShapeItCanAppearIn(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"backticked", "run `initech post` to publish"},
		{"bare prose", "run initech post to publish"},
		{"inside a fenced block", "```\ninitech post \"hi\"\n```"},
		{"with flags", "initech post --chime --default \"x\""},
		{"first thing on a line", "initech post\nnext line"},
		{"after a shell prompt", "$ initech post \"hi\""},
		{"in a markdown table cell", "| post it | initech post <text> | yes |"},
		{"mid-sentence with punctuation", "If initech post is unavailable, say so."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := verbsFound(t, tc.body)["post"]; got != 1 {
				t.Errorf("scanner found %d mentions of post in %q, want 1 — a mention it cannot see is a defect it reports green on", got, tc.body)
			}
		})
	}
}

// A wrapped line is the one shape the scanner genuinely cannot join, so it is
// pinned as a KNOWN limit rather than left for someone to discover: "initech"
// at the end of one line and the verb at the start of the next reads as prose
// to the regex. Recorded here so a future reader knows it was considered.
func TestScanMentions_DoesNotJoinAcrossALineWrap(t *testing.T) {
	if got := verbsFound(t, "run initech\npost to publish")["post"]; got != 0 {
		t.Fatalf("scanner joined a wrapped mention (%d); if that changed deliberately, update this test and the package comment", got)
	}
}

func TestScanMentions_ReportsFileAndLineAndConst(t *testing.T) {
	dir := writeConstFile(t, "line one\nline two\nrun initech post here")
	ms, err := scanMentions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d mentions, want 1", len(ms))
	}
	// const declared on line 3; the body's third line is line 5.
	if ms[0].line != 5 {
		t.Errorf("line = %d, want 5 — this fires at commit time, so it must name where to look", ms[0].line)
	}
	if ms[0].konst != "SomeTemplate" {
		t.Errorf("const = %q, want SomeTemplate", ms[0].konst)
	}
	if !strings.HasSuffix(ms[0].file, "templates.go") {
		t.Errorf("file = %q, want it to name the file", ms[0].file)
	}
}

// Go doc comments are not constant values: internal/roles has package
// comments reading "initech projects", which is prose about the product and
// not a taught command. Scanning the AST's constants excludes them by
// construction; this pins that.
func TestScanMentions_IgnoresDocComments(t *testing.T) {
	dir := t.TempDir()
	src := "// Package roles is for initech projects.\npackage roles\n\n// initech nonsense in a doc comment.\nconst X = `nothing here`\n"
	if err := os.WriteFile(filepath.Join(dir, "doc.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	ms, err := scanMentions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 {
		t.Errorf("scanned doc comments: %+v", ms)
	}
}

// ── the real corpus, not a fixture ──────────────────────────────────
//
// The fixtures above prove the extractor's shapes; only the real corpus
// proves it agrees with reality. Shipper's review asks for exactly this.

func TestScanMentions_AgreesWithAPlainGrepOfTheRealTemplates(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("module root unavailable")
	}
	ms, err := scanMentions(filepath.Join(root, scanDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 100 {
		t.Fatalf("only %d mentions in the real templates — the scanner is reading less than the corpus", len(ms))
	}
	// An independent count, derived differently, as an exact accounting
	// identity: every mention a plain grep of the file sees is either in a
	// constant (the scanner must find it) or in a comment (the scanner must
	// not). grep == ast + comments, with no slack — so a mention the scanner
	// MISSES fails here, and so does one it wrongly starts picking up.
	path := filepath.Join(root, scanDir, "templates.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`\binitech ([a-z][a-z0-9-]*)`)
	grepCount := len(pattern.FindAllString(string(raw), -1))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, raw, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	commentCount := 0
	for _, cg := range f.Comments {
		commentCount += len(pattern.FindAllString(cg.Text(), -1))
	}
	astCount := 0
	for _, m := range ms {
		if strings.HasSuffix(m.file, "templates.go") {
			astCount++
		}
	}
	if astCount+commentCount != grepCount {
		t.Errorf("accounting does not close for templates.go: grep sees %d mentions, the scanner found %d in constants and %d live in comments (%d unaccounted). A mention in neither bucket is one the census is blind to.",
			grepCount, astCount, commentCount, grepCount-astCount-commentCount)
	}
}

// ── the lookup ──────────────────────────────────────────────────────

func TestRun_PassesOnTheRealTree(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("module root unavailable")
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	if err := run(exemptionsPath, false); err != nil {
		t.Fatalf("census fails on the tree as committed: %v", err)
	}
}

func TestLoadExemptions_RefusesAnExemptionWithoutATrigger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ex.txt")
	if err := os.WriteFile(path, []byte("post  # landing later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExemptions(path); err == nil || !strings.Contains(err.Error(), "TRIGGER") {
		t.Fatalf("err = %v, want a refusal naming TRIGGER — an exemption without one is a permanent hole", err)
	}
}

func TestLoadExemptions_AcceptsATriggeredExemptionAndSkipsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ex.txt")
	body := "# a comment\n\npost  # child B registers it. TRIGGER: delete when B lands.\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	ex, err := loadExemptions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex) != 1 || !ex["post"] {
		t.Errorf("exemptions = %v, want just post", ex)
	}
}

// ── the scope assumption ────────────────────────────────────────────

// TestScaffold_RendersOnlyRolesTemplates makes this census's scope
// assumption fail loudly instead of widening in silence: the census scans
// internal/roles only, which is sound exactly while every agent-facing file
// initech writes comes from a roles.* constant.
//
// ASSERTED AS A POSITIVE — every template the scaffold renders IS a roles.*
// selector, and anything else offends whatever its type. The first version of
// this guard asked the negative ("does this look like a template literal?"),
// and shipper mutated it before believing it: it killed a long string LITERAL
// and MISSED a package-level CONST, because an *ast.Ident matched neither the
// roles.* branch nor the 40-character literal branch and fell through clean.
// Shipper took that end to end — agent-facing text from a local const,
// teaching an unregistered verb, passed this guard, passed the verb census,
// and passed make check-fast. The guard covered the form nobody writes and
// missed the one everybody writes: a multi-kilobyte template is always a
// const, which is how internal/roles itself is written (ini-35a1).
//
// A positive assertion has no threshold to tune and no type list to keep
// current, so it cannot be escaped by a form nobody thought of — which is
// exactly how the negative one was escaped. It is deliberately stricter than
// today's code: a legitimate local helper reds too, and for text that reaches
// agents that is the right default. Such an entry needs an exemption with a
// TRIGGER, like every other census in this family.
func TestScaffold_RendersOnlyRolesTemplates(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("module root unavailable")
	}
	path := filepath.Join(root, "internal/scaffold/scaffold.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	offenders, err := scaffoldTemplateOffenders(fset, f, path)
	if err != nil {
		t.Fatalf("the guard could not read its subject: %v", err)
	}
	for _, off := range offenders {
		t.Errorf("the scaffold renders agent-facing text that does NOT come from internal/roles:\n  %s\n\n"+
			"The template-verb census scans internal/roles only, so text sourced elsewhere is never checked for\n"+
			"unregistered verbs — an agent can be handed a command that does not exist. Move the text into\n"+
			"internal/roles, or widen scanDir in this package and say so in the package comment.", off)
	}
}

// scaffoldTemplateOffenders returns every template the scaffold renders that
// is not a roles.* template, located for the error message.
//
// THE SUBJECT IS THE OPERATION, NOT A NAME, and that is the third correction
// in this guard's history (ini-35a1). Its subject was narrowed three times by
// one mistake in different clothes:
//
//	value check    a negative predicate missed a local const (an *ast.Ident
//	               matched no branch) — inverted to "not roles.* offends"
//	site selector  an entry was recognised by a string-literal FILENAME, so a
//	               const filename skipped the pair — changed to position
//	subject        sawTable was satisfied by finding A table named
//	               docTemplates, which is not the claim "every table the
//	               scaffold renders from": a SECOND table alongside the real
//	               one compiled, vetted, and passed guard, census and
//	               check-fast while writing an unregistered verb into a file
//	               an agent reads (shipper's S4)
//
// Each fix was correct and each left the next reachable, because the guard
// kept naming its subject by a syntactic pattern and every pattern has an
// outside. So the subject is now every argument to a roles.Render call in
// this file — complete by definition, since the scaffold cannot render a
// template without calling Render — and a template this resolver cannot
// follow is REPORTED, never skipped. There is no name left to rename.
//
// Resolution, matching how the scaffold names templates today:
//
//	roles.X                     the template itself
//	dt.template, dt ranged      every entry of the ranged table, WHATEVER the
//	                            table is called — which is why a second table
//	                            cannot hide
//	x := TemplateForRole(...)   checked at that function's own returns
//	anything else               reported as unfollowable
//
// Still ONE FILE, by design and stated in the package comment: a second
// WRITER in another package is invisible here and is ini-zfbb's subject.
func scaffoldTemplateOffenders(fset *token.FileSet, f *ast.File, path string) ([]string, error) {
	var out []string
	at := func(n ast.Node) string { return fmt.Sprintf("%s:%d", path, fset.Position(n.Pos()).Line) }

	checkTemplate := func(e ast.Expr, what string) {
		if sel, ok := e.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "roles" {
				return
			}
		}
		out = append(out, fmt.Sprintf("%s %s is %s, not a roles.* template", at(e), what, exprKind(e)))
	}

	// Identifiers assigned from TemplateForRole, checked at its own returns.
	fromTemplateForRole := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range as.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok || i >= len(as.Lhs) {
				continue
			}
			if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "TemplateForRole" {
				if id, ok := as.Lhs[i].(*ast.Ident); ok {
					fromTemplateForRole[id.Name] = true
				}
			}
		}
		return true
	})

	// PASS 1: ranges whose variable feeds roles.Render. Scoped to each
	// range's OWN body rather than a map keyed by the variable's name: every
	// loop here calls its variable dt, and a name-keyed map made a second
	// loop resolve to the first loop's table.
	resolved := map[ast.Node]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		rs, ok := n.(*ast.RangeStmt)
		if !ok || rs.Value == nil {
			return true
		}
		loopVar, ok := rs.Value.(*ast.Ident)
		if !ok {
			return true
		}
		src, ok := rs.X.(*ast.Ident)
		if !ok {
			return true
		}
		var lit *ast.CompositeLit
		ast.Inspect(f, func(m ast.Node) bool {
			as, ok := m.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == src.Name && i < len(as.Rhs) {
					if cl, ok := as.Rhs[i].(*ast.CompositeLit); ok {
						lit = cl
					}
				}
			}
			return true
		})
		if lit == nil {
			return true
		}
		feeds := false
		ast.Inspect(rs.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !isRolesRender(call) {
				return true
			}
			if arg, ok := call.Args[0].(*ast.SelectorExpr); ok {
				if base, ok := arg.X.(*ast.Ident); ok && base.Name == loopVar.Name {
					feeds = true
					resolved[call] = true
				}
			}
			return true
		})
		if !feeds {
			return true
		}
		for _, elt := range lit.Elts {
			pair, ok := elt.(*ast.CompositeLit)
			if !ok || len(pair.Elts) != 2 {
				out = append(out, fmt.Sprintf("%s an entry of %s is not a {filename, template} pair; this guard cannot read it", at(elt), src.Name))
				continue
			}
			checkTemplate(pair.Elts[1], "an entry of "+src.Name)
		}
		return true
	})

	// PASS 2: every roles.Render call, resolved or reported.
	renders := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || !isRolesRender(call) {
			return true
		}
		renders++
		if resolved[call] {
			return true
		}
		switch a := call.Args[0].(type) {
		case *ast.SelectorExpr:
			if pkg, ok := a.X.(*ast.Ident); ok && pkg.Name == "roles" {
				return true
			}
		case *ast.Ident:
			if fromTemplateForRole[a.Name] {
				return true
			}
		}
		out = append(out, fmt.Sprintf("%s roles.Render is handed %s, which this guard cannot follow to a roles.* template; teach this guard to follow it rather than leaving it unchecked", at(call.Args[0]), exprKind(call.Args[0])))
		return true
	})

	// TemplateForRole's returns.
	sawTemplateForRole := false
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name.Name != "TemplateForRole" {
			continue
		}
		sawTemplateForRole = true
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, r := range ret.Results {
				checkTemplate(r, "a TemplateForRole return")
			}
			return true
		})
	}

	// REFUSE RATHER THAN PASS WITHOUT A SUBJECT. Silence cannot distinguish
	// "nothing to report" from "I could not look".
	switch {
	case renders == 0:
		return nil, fmt.Errorf("no roles.Render calls found in %s: this guard checks the templates the scaffold renders, and it cannot find any. If rendering moved, point this guard at where it went — do not delete it, which is what passing silently would amount to", path)
	case !sawTemplateForRole:
		return nil, fmt.Errorf("no TemplateForRole function found in %s: the role CLAUDE.md path is as agent-facing as the docs path, and this guard just lost sight of it", path)
	}
	return out, nil
}

// isRolesRender reports whether a call is roles.Render(...).
func isRolesRender(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Render" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "roles"
}

// scaffoldTableName is a fixture convenience only: the guard finds tables
// through the range that feeds roles.Render, never by name, so renaming the
// real table changes nothing.
const scaffoldTableName = "docTemplates"

// exprKind names what an offending expression IS, so the failure says why it
// was rejected rather than only that it was.
func exprKind(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return "the identifier " + x.Name + " (a local const or var)"
	case *ast.BasicLit:
		return "a string literal"
	case *ast.SelectorExpr:
		return "a selector on another package"
	case *ast.CallExpr:
		return "a function call"
	default:
		return fmt.Sprintf("a %T", e)
	}
}

// parseScaffoldSource runs the guard over planted source text.
func parseScaffoldSource(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "scaffold.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	offenders, err := scaffoldTemplateOffenders(fset, f, "scaffold.go")
	if err != nil {
		t.Fatalf("guard could not read the fixture: %v", err)
	}
	return offenders
}

// parseScaffoldSourceErr is parseScaffoldSource for the cases where the guard
// is EXPECTED to refuse: it returns the error instead of failing the test.
func parseScaffoldSourceErr(t *testing.T, src string) error {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "scaffold.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	_, gerr := scaffoldTemplateOffenders(fset, f, "scaffold.go")
	return gerr
}

// scaffoldFixture builds a scaffold-shaped source whose docTemplates table
// carries one planted entry beside a legitimate roles.* one.
func scaffoldFixture(entry, extra string) string {
	return scaffoldFixtureNamed(`"probe.md"`, entry, extra)
}

// scaffoldFixtureNamed builds the same fixture with the FILENAME element
// under test too, since the filename's shape is what E1 escaped through.
func scaffoldFixtureNamed(filename, entry, extra string) string {
	// Faithful to the real scaffold: a table, a range over it, and a
	// roles.Render call taking the range variable's template — which is the
	// operation the guard anchors on.
	return "package scaffold\n\n" + extra + "\n\nfunc Run() {\n" +
		"\t" + scaffoldTableName + " := []struct{ filename, template string }{\n" +
		"\t\t{" + filename + ", " + entry + "},\n" +
		"\t\t{\"prd.md\", roles.PRDTemplate},\n\t}\n" +
		"\tfor _, dt := range " + scaffoldTableName + " {\n\t\t_ = roles.Render(dt.template, vars)\n\t}\n}\n" +
		"\nfunc TemplateForRole(name string) string {\n\treturn roles.EngTemplate\n}\n"
}

// Every form agent-facing text can enter the scaffold by without coming from
// internal/roles. The package-level const referenced by identifier is
// shipper's M3 and the form anyone would actually write; the short literal is
// M2, which the 40-character floor used to wave through. The threshold is
// gone, not raised.
func TestScaffoldGuard_RejectsEveryNonRolesTemplateForm(t *testing.T) {
	long := `"` + strings.Repeat("x", 80) + `"`
	for _, tc := range []struct{ name, entry, extra string }{
		{"a package-level const by identifier (shipper's M3)", "localProbeTemplate",
			`const localProbeTemplate = "Run initech notarealverb to publish."`},
		{"a short string literal (M2, under the old 40-char floor)", `"short text"`, ""},
		{"a long string literal (M1, the only form the old guard caught)", long, ""},
		{"a package-level var", "localProbeVar", `var localProbeVar = "text"`},
		{"a function call", "buildTemplate()", `func buildTemplate() string { return "text" }`},
		{"a selector on another package", "othertmpl.Doc", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offenders := parseScaffoldSource(t, scaffoldFixture(tc.entry, tc.extra))
			if len(offenders) != 1 {
				t.Fatalf("got %d offenders, want exactly 1 (the planted entry): %v", len(offenders), offenders)
			}
			if !strings.Contains(offenders[0], "scaffold.go:") {
				t.Errorf("offender %q does not name file and line", offenders[0])
			}
		})
	}
}

// The positive side: roles.* entries and returns pass, in both places the
// scaffold names a template.
func TestScaffoldGuard_AcceptsRolesTemplatesInBothPlaces(t *testing.T) {
	src := scaffoldFixture("roles.SpecTemplate", "")
	if offenders := parseScaffoldSource(t, src); len(offenders) != 0 {
		t.Errorf("roles.* entries were rejected: %v", offenders)
	}
}

// A TemplateForRole return that is not a roles.* template offends too: the
// role CLAUDE.md path is as agent-facing as the docs path.
func TestScaffoldGuard_RejectsANonRolesTemplateForRoleReturn(t *testing.T) {
	src := "package scaffold\n\nconst localRole = \"Run initech notarealverb.\"\n\n" +
		"func Run() {\n\tdocs := []struct{ filename, template string }{\n\t\t{\"prd.md\", roles.PRDTemplate},\n\t}\n" +
		"\tfor _, dt := range docs {\n\t\t_ = roles.Render(dt.template, vars)\n\t}\n}\n\n" +
		"func TemplateForRole(name string) string {\n\tif name == \"super\" {\n\t\treturn localRole\n\t}\n\treturn roles.EngTemplate\n}\n"
	offenders := parseScaffoldSource(t, src)
	if len(offenders) != 1 || !strings.Contains(offenders[0], "TemplateForRole return") {
		t.Fatalf("offenders = %v, want exactly the local return", offenders)
	}
}

// Ordinary string returns elsewhere in the file are NOT templates: a
// file-wide positive assertion would flag writeFile's returns, and a guard
// that cries wolf on unrelated code gets disabled.
func TestScaffoldGuard_IgnoresOrdinaryStringReturnsElsewhere(t *testing.T) {
	src := scaffoldFixture("roles.SpecTemplate", "") +
		"\nfunc writeFile(dir string) (string, error) {\n\treturn \"/tmp/some/path\", nil\n}\n\n" +
		"func RenderRootCLAUDE() string {\n\treturn \"# a root file\"\n}\n"
	if offenders := parseScaffoldSource(t, src); len(offenders) != 0 {
		t.Errorf("ordinary string returns were flagged as templates: %v", offenders)
	}
}

// THE GUARD MUST REFUSE TO PASS WITHOUT ITS SUBJECT. Silence that reads as
// "nothing wrong" while meaning "I could not look" is this whole thread's
// failure mode, applied to the guard itself.
func TestScaffoldGuard_RefusesWhenItCannotFindWhatItChecks(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"nothing renders any more",
			"package scaffold\n\nfunc Run() {\n\tdocs := []struct{ filename, template string }{\n\t\t{\"prd.md\", roles.PRDTemplate},\n\t}\n\t_ = docs\n}\n\nfunc TemplateForRole(n string) string { return roles.EngTemplate }\n",
			"no roles.Render calls found",
		},
		{
			"TemplateForRole was renamed",
			"package scaffold\n\nfunc Run() {\n\tdocs := []struct{ filename, template string }{\n\t\t{\"prd.md\", roles.PRDTemplate},\n\t}\n\tfor _, dt := range docs {\n\t\t_ = roles.Render(dt.template, vars)\n\t}\n}\n",
			"no TemplateForRole function found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := parseScaffoldSourceErr(t, tc.src)
			if err == nil {
				t.Fatal("the guard passed while unable to find what it checks — the exact silence it exists to prevent")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// RENAMING THE TABLE IS A NON-EVENT now, which is the point of anchoring on
// the operation: the table is found through the range that feeds roles.Render,
// so what the variable is called never mattered.
func TestScaffoldGuard_ATableRenameChangesNothing(t *testing.T) {
	src := "package scaffold\n\nconst localProbe = \"Run initech notarealverb.\"\n\n" +
		"func Run() {\n\twhateverTheyCallItNow := []struct{ filename, template string }{\n" +
		"\t\t{\"probe.md\", localProbe},\n\t}\n" +
		"\tfor _, dt := range whateverTheyCallItNow {\n\t\t_ = roles.Render(dt.template, vars)\n\t}\n}\n" +
		"\nfunc TemplateForRole(n string) string { return roles.EngTemplate }\n"
	offenders := parseScaffoldSource(t, src)
	if len(offenders) != 1 || !strings.Contains(offenders[0], "localProbe") {
		t.Fatalf("offenders = %v, want the local template caught in a differently-named table", offenders)
	}
}

// SHIPPER'S S4: a SECOND table alongside the real one, in the same function,
// rendered by the same call shape. sawTable was an existence check standing in
// for a completeness claim; the render anchor makes the subject complete by
// definition, so a second table cannot hide behind the first.
func TestScaffoldGuard_RejectsASecondTableAlongsideTheRealOne(t *testing.T) {
	src := "package scaffold\n\nconst onboarding = \"Welcome aboard. Run initech notarealverb to get started.\"\n\n" +
		"func Run() {\n" +
		"\tdocTemplates := []struct{ filename, template string }{\n\t\t{\"prd.md\", roles.PRDTemplate},\n\t}\n" +
		"\tfor _, dt := range docTemplates {\n\t\t_ = roles.Render(dt.template, vars)\n\t}\n" +
		"\textraDocs := []struct{ filename, template string }{\n\t\t{\"onboarding.md\", onboarding},\n\t}\n" +
		"\tfor _, e := range extraDocs {\n\t\t_ = roles.Render(e.template, vars)\n\t}\n}\n" +
		"\nfunc TemplateForRole(n string) string { return roles.EngTemplate }\n"
	offenders := parseScaffoldSource(t, src)
	if len(offenders) != 1 {
		t.Fatalf("got %d offenders, want 1 — a second doc table must not hide behind the first: %v", len(offenders), offenders)
	}
	if !strings.Contains(offenders[0], "onboarding") || !strings.Contains(offenders[0], "extraDocs") {
		t.Errorf("offender %q should name the template and the table it came from", offenders[0])
	}
}

// A template named in a way the resolver cannot follow is REPORTED, not
// skipped: the resolver's limits must be visible, or the next unanticipated
// shape passes exactly as every earlier one did.
func TestScaffoldGuard_ReportsATemplateItCannotFollow(t *testing.T) {
	src := "package scaffold\n\nfunc Run() {\n\t_ = roles.Render(pickATemplate(), vars)\n}\n" +
		"\nfunc TemplateForRole(n string) string { return roles.EngTemplate }\n"
	offenders := parseScaffoldSource(t, src)
	if len(offenders) != 1 || !strings.Contains(offenders[0], "cannot follow") {
		t.Fatalf("offenders = %v, want the unfollowable argument reported", offenders)
	}
}

// An entry that is not a readable pair is reported rather than skipped.
func TestScaffoldGuard_ReportsAnEntryItCannotRead(t *testing.T) {
	src := "package scaffold\n\nfunc Run() {\n\tdocs := []struct{ a, b, c string }{\n\t\t{\"prd.md\", \"x\", \"y\"},\n\t}\n" +
		"\tfor _, dt := range docs {\n\t\t_ = roles.Render(dt.template, vars)\n\t}\n}\n" +
		"\nfunc TemplateForRole(n string) string { return roles.EngTemplate }\n"
	offenders := parseScaffoldSource(t, src)
	if len(offenders) != 1 || !strings.Contains(offenders[0], "cannot read it") {
		t.Fatalf("offenders = %v, want one entry reported as unreadable", offenders)
	}
}
