package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
)

// checkScaffold checks the artifact, independent of how its writers are coded.
// It covers a fresh default scaffold with every catalog role, not user-supplied
// overrides or arbitrary future conditional configurations.
// registered and exempt are passed IN rather than rebuilt here (ini-pacn).
// This check used to build its own registered set and never saw the exemption
// file at all, so a verb staged ahead of its command — with a valid TRIGGERed
// exemption, the sanctioned path ini-j0er exists to support — still reddened
// the build. It failed CLOSED, so nothing was unsafe; what was missing was the
// escape hatch, and a guard with no sanctioned path is a guard someone
// comments out. One caller now loads both sets once and hands them to every
// check, so the output check and the template census cannot disagree about
// which verbs are allowed.
func checkScaffold(root string, verbose bool, registered, exempt map[string]bool) error {
	dir, err := os.MkdirTemp("", "initech-templatecensus-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	names := make([]string, 0, len(roles.Catalog))
	for name := range roles.Catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Errorf("scaffold census: roles.Catalog is empty")
	}
	p := &config.Project{Name: "census-project", Root: dir, Roles: names}
	if _, err := scaffold.Run(p, scaffold.Options{}); err != nil {
		return fmt.Errorf("scaffold census: %w", err)
	}
	return checkScaffoldOutput(dir, root, p, verbose, registered, exempt)
}

// checkScaffoldOutput runs every check over an already-scaffolded directory.
// Split out of checkScaffold so a test can drive the WHOLE chain against a
// directory it prepared, rather than each check in isolation (ini-rg12).
//
// That split is not cosmetic. Testing a check function directly proves the
// check works; it proves nothing about whether anything calls it, and deleting
// the call from checkScaffold is invisible to such a test — verified by mutation
// while writing this. The directory seam also lets a test stage the one case
// this census exists for: a file on disk that scaffold's own write guard never
// saw, because some future writer used os.WriteFile directly.
func checkScaffoldOutput(dir, root string, p *config.Project, verbose bool, registered, exempt map[string]bool) error {
	files, err := readScaffoldOutput(dir)
	if err != nil {
		return err
	}
	count, err := checkOutputVerbs(files, registered, exempt)
	if err != nil {
		return err
	}
	if err := checkOutputPlaceholders(files); err != nil {
		return err
	}
	templates, err := templateConstants(filepath.Join(root, scanDir))
	if err != nil {
		return err
	}
	if err := checkTemplateCoverage(templates, files, p); err != nil {
		return err
	}
	if verbose {
		fmt.Printf("  scaffold: %d roles, %d templates, %d files, %d mentions\n", len(p.Roles), len(templates), len(files), count)
	}
	return nil
}

// readScaffoldOutput walks disk, not Run's returned path list: an additional
// writer need not remember to append its output to that list to be checked.
func readScaffoldOutput(dir string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("scaffold census: cannot inspect non-regular file %s", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read scaffold output: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("scaffold census: no output files; refusing an empty subject")
	}
	return files, nil
}

// checkOutputVerbs fails on a verb the scaffolded output teaches that this
// binary does not register AND no TRIGGERed exemption covers. The exemption
// is the staged-landing path: templates may teach a command that lands in a
// later commit, and the exemption goes stale — failing the census — the moment
// that command registers (ini-pacn, keeping ini-j0er's self-cleaning property).
func checkOutputVerbs(files map[string]string, registered, exempt map[string]bool) (int, error) {
	var failures []string
	count := 0
	for path, text := range files {
		for _, loc := range verbPattern.FindAllStringSubmatchIndex(text, -1) {
			count++
			verb := text[loc[2]:loc[3]]
			if !registered[verb] && !exempt[verb] {
				failures = append(failures, fmt.Sprintf("%s:%d: initech %s is not registered", path, 1+strings.Count(text[:loc[0]], "\n"), verb))
			}
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("scaffold census: no taught verbs; refusing an empty subject")
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return count, fmt.Errorf("scaffold output teaches unregistered commands:\n  %s", strings.Join(failures, "\n  "))
	}
	return count, nil
}

// checkOutputPlaceholders fails on any scaffolded file that still holds a "{{"
// template variable (ini-rg12). It reads the map readScaffoldOutput already
// produced rather than walking disk again, so it inherits that walk's property:
// a NEW writer is covered without remembering to register itself anywhere.
//
// The census project deliberately configures no tech_stack/build_cmd/test_cmd,
// which is the exact shape of every project `initech init` creates today — so
// this check exercises the unconfigured path, the one that shipped literal
// "{{tech_stack}}" into every agent's instructions.
func checkOutputPlaceholders(files map[string]string) error {
	var failures []string
	for path, text := range files {
		for _, left := range roles.UnrenderedPlaceholders(text) {
			failures = append(failures, fmt.Sprintf("%s: %s", path, left))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("scaffold output has unrendered template variables:\n  %s", strings.Join(failures, "\n  "))
	}
	return nil
}

// templateConstants derives the inventory from the package's *Template
// constants (the roles package naming contract), rather than a second role
// list. Type checking resolves concatenated constants and shared fragments.
// Parse/type errors refuse the check; there is no partial inventory fallback.
func templateConstants(dir string) (map[string]string, error) {
	pkg, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		return nil, fmt.Errorf("template inventory: %w", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	// Use module-aware export data: the default importer only searches GOPATH
	// and cannot resolve roles dependencies such as golang.org/x/term.
	conf := types.Config{Importer: importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		out, err := exec.Command("go", "list", "-export", "-f", "{{.Export}}", path).Output()
		if err != nil {
			return nil, fmt.Errorf("export data for %s: %w", path, err)
		}
		return os.Open(strings.TrimSpace(string(out)))
	})}
	checked, err := conf.Check(pkg.ImportPath, fset, files, nil)
	if err != nil {
		return nil, fmt.Errorf("template inventory: %w", err)
	}
	out := map[string]string{}
	for _, name := range checked.Scope().Names() {
		c, ok := checked.Scope().Lookup(name).(*types.Const)
		if !ok || !strings.HasSuffix(name, "Template") {
			continue
		}
		if c.Val().Kind() != constant.String {
			return nil, fmt.Errorf("template inventory: %s is not a string", name)
		}
		out[name] = constant.StringVal(c.Val())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("template inventory: no template constants found in %s", dir)
	}
	return out, nil
}

// checkTemplateCoverage verifies actual output, not just TemplateForRole's
// selection. A removed writer must fail even if its selector still returns
// the right template. Role templates must reach a catalog role's CLAUDE.md;
// document templates must reach at least one output file.
func checkTemplateCoverage(templates, files map[string]string, p *config.Project) error {
	if len(templates) == 0 {
		return fmt.Errorf("template coverage: empty template inventory")
	}
	var missing []string
	for name, tmpl := range templates {
		found := false
		for _, role := range p.Roles {
			// Rendered per role through the shared resolver, not through a
			// hand-built RenderVars: this check compares rendered text against
			// what scaffold.Run actually wrote, so a second copy of the
			// variable rules here would make the census disagree with the
			// scaffold and report every role template as unreached (ini-rg12).
			rendered := roles.Render(tmpl, scaffold.RenderVarsFor(p, p.Root, role))
			text, exists := files[role+"/CLAUDE.md"]
			if exists && text == roles.RenderString(rendered, "role_name", role) && rendered != "" {
				found = true
			}
		}
		if !strings.HasPrefix(tmpl, "# CLAUDE.md") {
			// Document templates are role-independent; scaffold.Run renders
			// them once with project name and root only.
			rendered := roles.Render(tmpl, roles.RenderVars{ProjectName: p.Name, ProjectRoot: p.Root})
			for _, text := range files {
				if text == rendered && rendered != "" {
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("template coverage: not reached in scaffold output: %s", strings.Join(missing, ", "))
	}
	return nil
}
