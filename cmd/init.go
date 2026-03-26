package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/git"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/nmelo/initech/internal/tmuxinator"
	"github.com/spf13/cobra"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap a new multi-agent project",
	Long: `Creates the project directory structure, role CLAUDE.md files, tmuxinator
config, project documents, and optionally initializes git and beads.

If initech.yaml exists in the current directory, it is loaded. Otherwise,
interactive prompts collect the project configuration.`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing files")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	runner := &iexec.DefaultRunner{}
	out := cmd.OutOrStdout()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Load or create config
	cfgPath := filepath.Join(wd, "initech.yaml")
	var p *config.Project

	if _, err := os.Stat(cfgPath); err == nil {
		p, err = config.Load(cfgPath)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Loaded existing initech.yaml")
	} else {
		p, err = interactiveSetup(wd)
		if err != nil {
			return err
		}
		if err := config.Write(cfgPath, p); err != nil {
			return err
		}
		fmt.Fprintln(out, "Created initech.yaml")
	}

	if err := config.Validate(p); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Scaffold project tree
	created, err := scaffold.Run(p, scaffold.Options{Force: initForce})
	if err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}

	// Initialize git
	if err := git.Init(runner, p.Root); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	// Add submodules for roles that need src
	for _, roleName := range p.Roles {
		def := roles.LookupRole(roleName)
		if !def.NeedsSrc || len(p.Repos) == 0 {
			continue
		}

		repoURL := p.Repos[0].URL
		// Check if role has a specific repo override
		if ov, ok := p.RoleOverrides[roleName]; ok && ov.RepoName != "" {
			for _, r := range p.Repos {
				if r.Name == ov.RepoName {
					repoURL = r.URL
					break
				}
			}
		}

		subPath := filepath.Join(roleName, "src")
		// Only add submodule if src dir doesn't already have a .git reference
		gitRef := filepath.Join(p.Root, subPath, ".git")
		if _, err := os.Stat(gitRef); err != nil {
			if err := git.AddSubmodule(runner, p.Root, repoURL, subPath); err != nil {
				fmt.Fprintf(out, "Warning: git submodule add for %s failed: %v\n", roleName, err)
			}
		}
	}

	// Initialize beads (graceful degradation)
	if _, err := runner.Run("which", "bd"); err == nil {
		if _, err := os.Stat(filepath.Join(p.Root, ".beads")); err != nil {
			if _, err := runner.RunInDir(p.Root, "bd", "init"); err != nil {
				fmt.Fprintf(out, "Warning: bd init failed: %v\n", err)
			} else {
				if p.Beads.Prefix != "" {
					runner.RunInDir(p.Root, "bd", "config", "set", "issue-prefix", p.Beads.Prefix)
				}
				fmt.Fprintln(out, "Initialized beads")
			}
		}
	} else {
		fmt.Fprintln(out, "Warning: bd not found, skipping beads initialization")
	}

	// Generate tmuxinator configs
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	tmuxDir := filepath.Join(home, ".config", "tmuxinator")
	if err := os.MkdirAll(tmuxDir, 0755); err != nil {
		return fmt.Errorf("create tmuxinator config dir: %w", err)
	}

	mainYAML, err := tmuxinator.Generate(p)
	if err != nil {
		return fmt.Errorf("generate tmuxinator config: %w", err)
	}
	mainPath := filepath.Join(tmuxDir, p.Name+".yml")
	if err := os.WriteFile(mainPath, mainYAML, 0644); err != nil {
		return fmt.Errorf("write tmuxinator config: %w", err)
	}

	gridYAML, err := tmuxinator.GenerateGrid(p)
	if err != nil {
		return fmt.Errorf("generate grid config: %w", err)
	}
	if gridYAML != nil {
		gridPath := filepath.Join(tmuxDir, p.Name+"-grid.yml")
		if err := os.WriteFile(gridPath, gridYAML, 0644); err != nil {
			return fmt.Errorf("write grid config: %w", err)
		}
	}

	// Initial commit
	if err := git.CommitAll(runner, p.Root, "initech: bootstrap "+p.Name); err != nil {
		fmt.Fprintf(out, "Warning: initial commit failed: %v\n", err)
	}

	// Summary
	fmt.Fprintln(out, "\nCreated:")
	for _, c := range created {
		fmt.Fprintf(out, "  %s\n", c)
	}
	fmt.Fprintf(out, "  ~/.config/tmuxinator/%s.yml\n", p.Name)
	if gridYAML != nil {
		fmt.Fprintf(out, "  ~/.config/tmuxinator/%s-grid.yml\n", p.Name)
	}
	fmt.Fprintf(out, "\nReady. Run 'initech' to start.\n")

	return nil
}

func interactiveSetup(wd string) (*config.Project, error) {
	reader := bufio.NewReader(os.Stdin)

	dirName := filepath.Base(wd)

	name := prompt(reader, "Project name", dirName)
	root := prompt(reader, "Project root", wd)
	repoURL := prompt(reader, "Code repo URL", "")

	// Detect existing agent workspaces before showing the role selector, so the
	// operator can adopt their existing directory structure without retyping names.
	detected := detectWorkspaces(root)
	useDetected := false
	if len(detected) > 0 {
		fmt.Printf("\nDetected existing agent workspaces:\n")
		for _, d := range detected {
			info := describeWorkspace(root, d)
			fmt.Printf("  %-14s %s\n", d+"/", info)
		}
		answer := prompt(reader, "\nUse detected workspaces as starting selection? [Y/n]", "Y")
		useDetected = strings.ToLower(answer) != "n"
	}

	// Build selector items: detected roles pre-checked, or Standard preset.
	var items []roles.SelectorItem
	if useDetected {
		items = buildSelectorItemsFromDetected(detected)
	} else {
		items = buildSelectorItems()
	}

	// Role selection: interactive checkbox UI. Loop until at least one role is
	// chosen (Esc/Ctrl+C aborts the whole init).
	var roleList []string
	for {
		selected, err := roles.RunSelector("Select agents for "+name, items)
		if err != nil {
			return nil, fmt.Errorf("role selection cancelled")
		}
		if len(selected) > 0 {
			roleList = selected
			break
		}
		fmt.Fprintln(os.Stderr, "Error: at least one role is required.")
	}

	prefix := prompt(reader, "Beads prefix", name[:min(3, len(name))])

	p := &config.Project{
		Name:  name,
		Root:  root,
		Roles: roleList,
		Beads: config.BeadsConfig{Prefix: prefix},
	}

	if repoURL != "" {
		repoName := filepath.Base(strings.TrimSuffix(repoURL, ".git"))
		p.Repos = []config.Repo{{URL: repoURL, Name: repoName}}
	}

	return p, nil
}

// roleSpec defines the display properties of one role in the selector.
type roleSpec struct {
	name  string
	group string
	desc  string
}

// selectorOrder defines the grouped display order for the role selector.
// Items appear in this order; group headers are inserted automatically when
// the group field changes.
var selectorOrder = []roleSpec{
	{"super",   "COORDINATORS", "Coordinator/dispatcher"},
	{"pm",      "PRODUCT",      "Product manager"},
	{"pmm",     "PRODUCT",      "Product marketing manager"},
	{"arch",    "PRODUCT",      "Architect"},
	{"eng1",    "ENGINEERS",    "Engineer"},
	{"eng2",    "ENGINEERS",    "Engineer"},
	{"eng3",    "ENGINEERS",    "Engineer"},
	{"qa1",     "QA",           "QA tester"},
	{"qa2",     "QA",           "QA tester"},
	{"shipper", "OPERATIONS",   "Release manager"},
	{"sec",     "OPERATIONS",   "Security reviewer"},
	{"ops",     "OPERATIONS",   "Operations/UX tester"},
	{"writer",  "OPERATIONS",   "Technical writer"},
	{"growth",  "OPERATIONS",   "Growth engineer"},
}

// standardPreset is the set of roles pre-checked in the selector by default.
// Matches the current default team: 7 agents covering the full dev loop.
var standardPreset = map[string]bool{
	"super": true, "pm": true,
	"eng1": true, "eng2": true,
	"qa1": true, "qa2": true,
	"shipper": true,
}

// buildSelectorItems constructs the []SelectorItem list for RunSelector from
// the static role order, deriving Tags from the roles catalog.
func buildSelectorItems() []roles.SelectorItem {
	items := make([]roles.SelectorItem, len(selectorOrder))
	for i, spec := range selectorOrder {
		def := roles.LookupRole(spec.name)
		tag := ""
		if def.Permission == roles.Supervised {
			tag = "supervised"
		} else if def.NeedsSrc {
			tag = "needs src"
		}
		items[i] = roles.SelectorItem{
			Name:        spec.name,
			Description: spec.desc,
			Group:       spec.group,
			Tag:         tag,
			Checked:     standardPreset[spec.name],
		}
	}
	return items
}

// detectWorkspaces scans root for subdirectories that contain a CLAUDE.md file.
// These are treated as existing agent workspaces. Hidden directories and known
// non-agent directories (docs, dist, node_modules) are skipped.
// os.ReadDir returns entries in lexicographic order, so the result is sorted.
func detectWorkspaces(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	skip := map[string]bool{
		"docs": true, "dist": true, "node_modules": true,
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skip[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, "CLAUDE.md")); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// describeWorkspace returns a parenthetical tag string summarising the
// contents of a detected workspace directory, e.g. "(CLAUDE.md, src/)".
func describeWorkspace(root, name string) string {
	tags := []string{"CLAUDE.md"}
	if _, err := os.Stat(filepath.Join(root, name, "src")); err == nil {
		tags = append(tags, "src/")
	}
	if _, err := os.Stat(filepath.Join(root, name, ".claude")); err == nil {
		tags = append(tags, ".claude/")
	}
	return "(" + strings.Join(tags, ", ") + ")"
}

// catalogContains reports whether name is a known role in selectorOrder.
func catalogContains(name string) bool {
	for _, spec := range selectorOrder {
		if spec.name == name {
			return true
		}
	}
	return false
}

// buildSelectorItemsFromDetected builds the selector item list like
// buildSelectorItems, but pre-checks roles that were detected on disk instead
// of using the Standard preset. Detected roles not in the catalog are appended
// as CUSTOM group items with "(detected)" as their description.
func buildSelectorItemsFromDetected(detected []string) []roles.SelectorItem {
	detectedSet := make(map[string]bool, len(detected))
	for _, d := range detected {
		detectedSet[d] = true
	}
	items := make([]roles.SelectorItem, len(selectorOrder))
	for i, spec := range selectorOrder {
		def := roles.LookupRole(spec.name)
		tag := ""
		if def.Permission == roles.Supervised {
			tag = "supervised"
		} else if def.NeedsSrc {
			tag = "needs src"
		}
		items[i] = roles.SelectorItem{
			Name:        spec.name,
			Description: spec.desc,
			Group:       spec.group,
			Tag:         tag,
			Checked:     detectedSet[spec.name],
		}
	}
	// Append detected roles that aren't in the catalog as CUSTOM group items.
	for _, d := range detected {
		if !catalogContains(d) {
			items = append(items, roles.SelectorItem{
				Name:        d,
				Description: "(detected)",
				Group:       "CUSTOM",
				Checked:     true,
			})
		}
	}
	return items
}

func prompt(reader *bufio.Reader, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}

	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}
