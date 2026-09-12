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

// The exemption shipped today must name the bead that removes it, so the
// coupling is readable in the tree and not only in a person's memory.
func TestExemptionsFile_PostExemptionNamesItsRemovalTrigger(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("module root unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(root, exemptionsPath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "post ") {
		t.Fatal("the post exemption is gone; if child B landed, delete it and this test with it")
	}
	for _, want := range []string{"TRIGGER:", "child B"} {
		if !strings.Contains(text, want) {
			t.Errorf("the post exemption does not mention %q", want)
		}
	}
}

// ── the scope assumption ────────────────────────────────────────────

// TestScaffold_RendersOnlyRolesTemplates makes this census's scope
// assumption fail loudly instead of widening in silence: the census scans
// internal/roles only, which is sound exactly while every agent-facing file
// initech writes comes from a roles.* constant.
//
// internal/scaffold is the writer, so this parses it and asserts every
// template it hands to roles.Render (and every template TemplateForRole
// returns) is a roles.* selector rather than a locally-defined string. Add a
// local template to the scaffold — a prompt, a generated doc, a new file for
// agents — and this reds, pointing at the census that would otherwise have
// gone blind to it. (shipper's review note on ini-j0er: the next person needs
// to meet the assumption AT THE CODE, not when a taught verb ships
// unchecked.)
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

	// Every string-valued expression that names a template: the elements of
	// the docTemplates literal and TemplateForRole's returns. Both are
	// selector expressions today (roles.PRDTemplate, roles.SuperTemplate...).
	var offenders []string
	checkTemplateExpr := func(e ast.Expr, what string) {
		sel, ok := e.(*ast.SelectorExpr)
		if ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "roles" {
				return
			}
		}
		// Anything that is not roles.Something and looks like template text.
		if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING && len(lit.Value) > 40 {
			offenders = append(offenders, fmt.Sprintf("%s:%d %s is a locally-defined template string", path, fset.Position(e.Pos()).Line, what))
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			// The docTemplates table: {filename, template} pairs.
			for _, elt := range x.Elts {
				if cl, ok := elt.(*ast.CompositeLit); ok && len(cl.Elts) == 2 {
					checkTemplateExpr(cl.Elts[1], "a docTemplates entry")
				}
			}
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				checkTemplateExpr(r, "a TemplateForRole return")
			}
		}
		return true
	})

	if len(offenders) > 0 {
		t.Errorf("the scaffold renders agent-facing text that does NOT come from internal/roles:\n  %s\n\n"+
			"The template-verb census scans internal/roles only, so text sourced elsewhere is never checked for\n"+
			"unregistered verbs. Either move it into internal/roles, or widen scanDir in this package and say so\n"+
			"in the package comment.", strings.Join(offenders, "\n  "))
	}
}
