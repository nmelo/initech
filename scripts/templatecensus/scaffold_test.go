package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nmelo/initech/cmd"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
)

func TestScaffoldOutput_WalksAdditionalFilesAndReportsVerbs(t *testing.T) {
	dir := t.TempDir()
	// No extension/path allowlist, including files outside docs and CLAUDE.md.
	for path, text := range map[string]string{
		"CLAUDE.md":                      "Run initech status",
		"docs/onboarding.md":             "Welcome\nRun initech notarealverb",
		"new-writer/nested/instructions": "initech anothermissingverb",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := readScaffoldOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("read %d files, want all 3", len(files))
	}
	count, err := checkOutputVerbs(files, map[string]bool{"status": true}, nil)
	if count != 3 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	for _, want := range []string{"docs/onboarding.md:2: initech notarealverb", "new-writer/nested/instructions:1: initech anothermissingverb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v does not name %s", err, want)
		}
	}
	count, err = checkOutputVerbs(files, map[string]bool{"status": true, "notarealverb": true, "anothermissingverb": true}, nil)
	if count != 3 || err != nil {
		t.Fatalf("registered output: count=%d err=%v", count, err)
	}
}

func TestScaffoldOutput_RefusesEmptyOrUnreadableSubjects(t *testing.T) {
	if _, err := readScaffoldOutput(t.TempDir()); err == nil {
		t.Fatal("empty output passed")
	}
	if _, err := readScaffoldOutput(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root passed")
	}
	if _, err := checkOutputVerbs(map[string]string{"CLAUDE.md": "no commands"}, nil, nil); err == nil {
		t.Fatal("zero mentions passed")
	}
}

func TestTemplateInventory_ResolvesConstantsAndNamesUnreachedTemplates(t *testing.T) {
	dir := t.TempDir()
	src := "package roles\nconst fragment = `initech status`\nconst FirstTemplate = `# CLAUDE.md\n` + fragment\nconst ForgottenTemplate = `never rendered`\n"
	if err := os.WriteFile(filepath.Join(dir, "new_templates.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	templates, err := templateConstants(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 2 || templates["FirstTemplate"] != "# CLAUDE.md\ninitech status" {
		t.Fatalf("inventory=%v", templates)
	}
	p := &config.Project{Roles: []string{"first"}}
	files := map[string]string{"first/CLAUDE.md": templates["FirstTemplate"]}
	if err := checkTemplateCoverage(templates, files, p); err == nil || !strings.Contains(err.Error(), "ForgottenTemplate") {
		t.Fatalf("missing template: %v", err)
	}
	files["docs/new.md"] = templates["ForgottenTemplate"]
	if err := checkTemplateCoverage(templates, files, p); err != nil {
		t.Fatal(err)
	}
	delete(files, "first/CLAUDE.md")
	files["docs/wrong-place.md"] = templates["FirstTemplate"]
	if err := checkTemplateCoverage(templates, files, p); err == nil || !strings.Contains(err.Error(), "FirstTemplate") {
		t.Fatalf("role template never handed to role: %v", err)
	}
}

func TestTemplateInventory_RefusesEmptyAndInvalidPackages(t *testing.T) {
	for _, src := range []string{"package roles", "package roles\nconst BadTemplate = missing", "package roles\nconst BadTemplate = 3"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "templates.go"), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := templateConstants(dir); err == nil {
			t.Fatalf("accepted %s", src)
		}
	}
}

func TestTemplateCoverage_CatalogRolesReachEveryRealTemplate(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	templates, err := templateConstants(filepath.Join(root, scanDir))
	if err != nil {
		t.Fatal(err)
	}
	p := &config.Project{Name: "coverage", Root: t.TempDir()}
	for name := range roles.Catalog {
		p.Roles = append(p.Roles, name)
	}
	if _, err := scaffold.Run(p, scaffold.Options{}); err != nil {
		t.Fatal(err)
	}
	files, err := readScaffoldOutput(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTemplateCoverage(templates, files, p); err != nil {
		t.Fatal(err)
	}
	// Remove each catalog output in turn. Shared engineer/QA templates still
	// reach other roles; templates with a single role must fail by constant name.
	for _, role := range p.Roles {
		tmpl := scaffold.TemplateForRole(role)
		unique := true
		for _, other := range p.Roles {
			if other != role && scaffold.TemplateForRole(other) == tmpl {
				unique = false
			}
		}
		if !unique {
			continue
		}
		path := role + "/CLAUDE.md"
		saved := files[path]
		delete(files, path)
		err := checkTemplateCoverage(templates, files, p)
		files[path] = saved
		for name, text := range templates {
			if text == tmpl && (err == nil || !strings.Contains(err.Error(), name)) {
				t.Errorf("removed %s: missing %s was not reported: %v", path, name, err)
			}
		}
	}
	t.Logf("catalog=%d templates=%d files=%d", len(p.Roles), len(templates), len(files))
}

// ── the staged-landing path (ini-pacn) ──────────────────────────────
//
// shipper's repro, reviewing ini-zfbb: a verb staged ahead of its command,
// carrying a valid TRIGGERed exemption, still reddened the build because this
// check ran before the exemptions loaded and built its own registered set. It
// failed CLOSED — nothing unsafe — but it removed the only sanctioned way
// past the guard, and a guard with no sanctioned path is one somebody
// comments out. The G-before-B case (templates taught `post` before
// cmd/post.go existed) is exactly this, and it happened today.

func TestCheckOutputVerbs_AStagedVerbWithAnExemptionPasses(t *testing.T) {
	files := map[string]string{"docs/spec.md": "run initech futureverb to do the thing"}
	count, err := checkOutputVerbs(files, map[string]bool{"status": true}, map[string]bool{"futureverb": true})
	if err != nil {
		t.Fatalf("a staged verb with a valid exemption reddened the build: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestCheckOutputVerbs_TheSameStagedVerbWithoutAnExemptionFailsNamingTheFile(t *testing.T) {
	files := map[string]string{"docs/spec.md": "run initech futureverb to do the thing"}
	_, err := checkOutputVerbs(files, map[string]bool{"status": true}, nil)
	if err == nil {
		t.Fatal("an unregistered, unexempted verb passed — the guard's whole subject")
	}
	if !strings.Contains(err.Error(), "docs/spec.md") || !strings.Contains(err.Error(), "futureverb") {
		t.Errorf("error %q must name the file and the verb", err)
	}
}

// An exemption covers ONE verb, not the check: a second unregistered verb in
// the same output still fails.
func TestCheckOutputVerbs_AnExemptionDoesNotSilenceOtherVerbs(t *testing.T) {
	files := map[string]string{"docs/spec.md": "initech futureverb then initech alsomissing"}
	_, err := checkOutputVerbs(files, map[string]bool{}, map[string]bool{"futureverb": true})
	if err == nil || !strings.Contains(err.Error(), "alsomissing") {
		t.Fatalf("err = %v, want the unexempted verb still named", err)
	}
}

// The empty-subject refusals survive the new parameter: an exemption map must
// not turn "I read nothing" into a pass.
func TestCheckOutputVerbs_StillRefusesAnEmptySubjectWithExemptions(t *testing.T) {
	if _, err := checkOutputVerbs(map[string]string{"CLAUDE.md": "no commands"}, nil, map[string]bool{"futureverb": true}); err == nil {
		t.Error("no taught verbs at all passed while an exemption was present")
	}
}

// The ordering itself, asserted through checkScaffold on the real tree: with
// the exemption list threaded in, a staged verb is tolerated; the same call
// with no exemptions is the shipped behaviour. This is the cell that reds if
// someone moves the exemption load back below checkScaffold.
func TestCheckScaffold_HonoursTheExemptionListItIsGiven(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("module root unavailable")
	}
	registered := map[string]bool{}
	for _, v := range cmd.RegisteredVerbs() {
		registered[v] = true
	}
	if err := checkScaffold(root, false, registered, map[string]bool{"futureverb": true}); err != nil {
		t.Fatalf("checkScaffold rejected the tree while carrying an exemption: %v", err)
	}
	if err := checkScaffold(root, false, registered, nil); err != nil {
		t.Fatalf("checkScaffold rejected the clean tree with no exemptions: %v", err)
	}
}

// TestCheckOutputPlaceholders_FailsNamingTheFileAndLine is ini-rg12's census
// half. The check reads the map readScaffoldOutput already built, so it must
// behave on a hand-made map exactly as it does on a real scaffold.
func TestCheckOutputPlaceholders_FailsNamingTheFileAndLine(t *testing.T) {
	err := checkOutputPlaceholders(map[string]string{
		"eng1/CLAUDE.md": "## Tech Stack\n\n{{tech_stack}}\n",
		"qa1/CLAUDE.md":  "## Tech Stack\n\nGo 1.25\n",
	})
	if err == nil {
		t.Fatal("checkOutputPlaceholders accepted output holding {{tech_stack}}")
	}
	for _, want := range []string{"eng1/CLAUDE.md", "line 3", "tech_stack"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "qa1/CLAUDE.md") {
		t.Errorf("error = %q, want the clean file left out", err.Error())
	}
}

// TestCheckOutputPlaceholders_AcceptsFullyRenderedOutput is the other
// direction: a check that failed on everything would pass the test above while
// making the census useless.
func TestCheckOutputPlaceholders_AcceptsFullyRenderedOutput(t *testing.T) {
	if err := checkOutputPlaceholders(map[string]string{
		"eng1/CLAUDE.md": "## Tech Stack\n\nGo 1.25\n\nBuild: `make build`\n",
		"docs/prd.md":    "# demo PRD\n\nfunc f() { x := 1 }\n",
	}); err != nil {
		t.Errorf("checkOutputPlaceholders rejected clean output: %v", err)
	}
}

// TestCheckScaffoldOutput_FailsOnAPlaceholderThatBypassedTheWriteGuard drives
// the WHOLE census chain, not the placeholder check in isolation: it proves
// checkScaffoldOutput actually calls it. The two tests above would all pass
// with the call deleted from the chain — confirmed by mutation, which is why
// this one exists.
//
// The staged file is written with os.WriteFile, deliberately bypassing
// scaffold's writeFile guard. That is the exact scenario this census covers
// that the guard cannot: a future writer that does not go through the
// chokepoint. A placeholder from a normal writer never reaches disk at all,
// so staging one through writeFile would be impossible by construction.
func TestCheckScaffoldOutput_FailsOnAPlaceholderThatBypassedTheWriteGuard(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("module root unavailable")
	}
	registered := map[string]bool{}
	for _, v := range cmd.RegisteredVerbs() {
		registered[v] = true
	}
	names := make([]string, 0, len(roles.Catalog))
	for name := range roles.Catalog {
		names = append(names, name)
	}
	sort.Strings(names)

	dir := t.TempDir()
	p := &config.Project{Name: "census-project", Root: dir, Roles: names}
	if _, err := scaffold.Run(p, scaffold.Options{}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	// Standing control: without the staged file the same chain passes, so a
	// failure below is attributable to the placeholder and not to the fixture.
	if err := checkScaffoldOutput(dir, root, p, false, registered, nil); err != nil {
		t.Fatalf("clean scaffold output rejected: %v", err)
	}

	rogue := filepath.Join(dir, names[0], "NOTES.md")
	if err := os.WriteFile(rogue, []byte("# Notes\n\nDeploy: {{deploy_cmd}}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err = checkScaffoldOutput(dir, root, p, false, registered, nil)
	if err == nil {
		t.Fatal("checkScaffoldOutput passed output holding an unrendered placeholder")
	}
	if !strings.Contains(err.Error(), "deploy_cmd") {
		t.Errorf("error = %q, want it to name the offending variable", err.Error())
	}
	if !strings.Contains(err.Error(), "NOTES.md") {
		t.Errorf("error = %q, want it to name the offending file", err.Error())
	}
}
