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

	"github.com/nmelo/initech/cmd"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
)

// checkScaffold checks the artifact, independent of how its writers are coded.
// It covers a fresh default scaffold with every catalog role, not user-supplied
// overrides or arbitrary future conditional configurations.
func checkScaffold(root string, verbose bool) error {
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
	files, err := readScaffoldOutput(dir)
	if err != nil {
		return err
	}
	registered := map[string]bool{}
	for _, v := range cmd.RegisteredVerbs() {
		registered[v] = true
	}
	count, err := checkOutputVerbs(files, registered)
	if err != nil {
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
		fmt.Printf("  scaffold: %d roles, %d templates, %d files, %d mentions\n", len(names), len(templates), len(files), count)
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

func checkOutputVerbs(files map[string]string, registered map[string]bool) (int, error) {
	var failures []string
	count := 0
	for path, text := range files {
		for _, loc := range verbPattern.FindAllStringSubmatchIndex(text, -1) {
			count++
			verb := text[loc[2]:loc[3]]
			if !registered[verb] {
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
	vars := roles.RenderVars{ProjectName: p.Name, ProjectRoot: p.Root}
	var missing []string
	for name, tmpl := range templates {
		rendered := roles.Render(tmpl, vars)
		found := false
		for _, role := range p.Roles {
			text, exists := files[role+"/CLAUDE.md"]
			if exists && text == roles.RenderString(rendered, "role_name", role) && rendered != "" {
				found = true
			}
		}
		if !strings.HasPrefix(tmpl, "# CLAUDE.md") {
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
