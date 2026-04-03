package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/git"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/spf13/cobra"
)

var initForce bool

var (
	newInitRunner   = func() iexec.Runner { return &iexec.DefaultRunner{} }
	runRoleSelector = roles.RunSelector
	scaffoldRun     = scaffold.Run
	gitInit         = git.Init
	gitAddSubmodule = git.AddSubmodule
	gitCommitAll    = git.CommitAll
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap a new multi-agent project",
	Long: `Creates the project directory structure, role CLAUDE.md files, project
documents, and optionally initializes git and beads.

If initech.yaml exists in the current directory, it is loaded. Otherwise,
interactive prompts collect the project configuration.`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing files")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	runner := newInitRunner()
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
		fmt.Fprintln(out, color.Green("\u2713")+" Loaded existing "+color.Bold("initech.yaml"))
	} else {
		p, err = interactiveSetup(wd)
		if err != nil {
			return err
		}
		if err := config.Write(cfgPath, p); err != nil {
			return err
		}
		fmt.Fprintln(out, color.Green("\u2713")+" Created "+color.Bold("initech.yaml"))
	}

	if err := config.Validate(p); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Scaffold project tree
	progress := func(msg string) {
		// Colorize: "Creating path/file" -> green checkmark + blue filename
		if strings.HasPrefix(msg, "Creating ") {
			path := strings.TrimPrefix(msg, "Creating ")
			fmt.Fprintf(out, "  %s %s\n", color.Green("\u2713"), color.Blue(path))
		} else {
			fmt.Fprintf(out, "  %s\n", msg)
		}
	}
	fmt.Fprintln(out, "\n"+color.CyanBold("Scaffolding project..."))
	created, err := scaffoldRun(p, scaffold.Options{Force: initForce, Progress: progress})
	if err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}

	// Initialize git
	fmt.Fprintf(out, "  %s %s\n", color.Green("\u2713"), color.Dim("Initializing git repository"))
	if err := gitInit(runner, p.Root); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	// Add submodules for roles that need src (parallel clones).
	type cloneJob struct {
		role    string
		repoURL string
		subPath string
	}
	var jobs []cloneJob
	for _, roleName := range p.Roles {
		def := roles.LookupRole(roleName)
		if !def.NeedsSrc || len(p.Repos) == 0 {
			continue
		}

		repoURL := p.Repos[0].URL
		if ov, ok := p.RoleOverrides[roleName]; ok && ov.RepoName != "" {
			for _, r := range p.Repos {
				if r.Name == ov.RepoName {
					repoURL = r.URL
					break
				}
			}
		}

		subPath := filepath.Join(roleName, "src")
		gitRef := filepath.Join(p.Root, subPath, ".git")
		if _, err := os.Stat(gitRef); err != nil {
			jobs = append(jobs, cloneJob{role: roleName, repoURL: repoURL, subPath: subPath})
		}
	}

	if len(jobs) > 0 {
		label := fmt.Sprintf("Cloning repo into %d agent workspaces", len(jobs))
		fmt.Fprintf(out, "  %s... ", label)
		spinDone := make(chan struct{})
		go func() {
			spinner := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
			i := 0
			for {
				select {
				case <-spinDone:
					return
				default:
					fmt.Fprintf(out, "\r  %s... %c", label, spinner[i%len(spinner)])
					i++
					time.Sleep(80 * time.Millisecond)
				}
			}
		}()

		type cloneResult struct {
			role string
			err  error
		}
		results := make([]cloneResult, len(jobs))
		var wg sync.WaitGroup
		for i, job := range jobs {
			wg.Add(1)
			go func(idx int, j cloneJob) {
				defer wg.Done()
				results[idx] = cloneResult{
					role: j.role,
					err:  gitAddSubmodule(runner, p.Root, j.repoURL, j.subPath),
				}
			}(i, job)
		}
		wg.Wait()
		close(spinDone)

		var failures int
		for _, r := range results {
			if r.err != nil {
				fmt.Fprintf(out, "\r  %s %s: %v\n", color.Red("\u2717"), color.Red("clone failed for "+r.role), r.err)
				failures++
			}
		}
		if failures == 0 {
			fmt.Fprintf(out, "\r  %s %s\n", color.Green("\u2713"), label)
		}
	}

	// Initialize beads (graceful degradation, skip when disabled)
	if p.Beads.IsEnabled() {
		if _, err := runner.Run("which", "bd"); err == nil {
			if _, err := os.Stat(filepath.Join(p.Root, ".beads")); err != nil {
				if _, err := runner.RunInDir(p.Root, "bd", "init"); err != nil {
					fmt.Fprintf(out, "  %s %s: %v\n", color.Yellow("!"), color.Yellow("bd init failed"), err)
				} else {
					if p.Beads.Prefix != "" {
						runner.RunInDir(p.Root, "bd", "config", "set", "issue-prefix", p.Beads.Prefix)
					}
					fmt.Fprintf(out, "  %s %s\n", color.Green("\u2713"), color.Dim("Initialized beads"))
				}
			}
		} else {
			fmt.Fprintf(out, "  %s %s\n", color.Dim("-"), color.Dim("Skipping beads (bd not found)"))
		}
	} else {
		fmt.Fprintf(out, "  %s %s\n", color.Dim("-"), color.Dim("Beads disabled"))
	}

	// Initial commit
	if err := gitCommitAll(runner, p.Root, "initech: bootstrap "+p.Name); err != nil {
		fmt.Fprintf(out, "  %s %s: %v\n", color.Yellow("!"), color.Yellow("initial commit failed"), err)
	} else {
		fmt.Fprintf(out, "  %s %s\n", color.Green("\u2713"), color.Dim("Initial commit"))
	}

	// Summary box. Inner width 48 fits the longest next-step line.
	// Uses color.Pad for alignment since ANSI escapes break %-Ns.
	memGB := float64(len(p.Roles)) * 1.5
	bdr := color.Green
	border := bdr(strings.Repeat("\u2500", 50))
	row := func(s string) { fmt.Fprintf(out, "  %s %s %s\n", bdr("\u2502"), color.Pad(s, 48), bdr("\u2502")) }
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  %s%s%s\n", bdr("\u250c"), border, bdr("\u2510"))
	row(color.Bold(p.Name))
	row(color.Cyan(fmt.Sprintf("%d", len(p.Roles))) + " agents, " + color.Cyan(fmt.Sprintf("~%.0f GB", memGB)) + " estimated memory")
	row(color.Cyan(fmt.Sprintf("%d", len(created))) + " files created")
	row("")
	row(color.Bold("Next steps:"))
	row("  " + color.Cyan("1.") + " Edit " + color.Bold("docs/prd.md") + " (define your problem)")
	row("  " + color.Cyan("2.") + " Run " + color.Bold("'bd create'") + " to add your first task")
	row("  " + color.Cyan("3.") + " Run " + color.Bold("'initech'") + " to start your session")
	row("  " + color.Cyan("4.") + " Press " + color.Bold("backtick") + " for commands, " + color.Bold("?") + " for help")
	fmt.Fprintf(out, "  %s%s%s\n", bdr("\u2514"), border, bdr("\u2518"))

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
		selected, err := runRoleSelector("Select agents for "+name, items,
			"Each agent runs Claude Code in its own terminal pane. Pick roles for your team.")
		if err != nil {
			return nil, fmt.Errorf("role selection cancelled")
		}
		if len(selected) > 0 {
			roleList = selected
			break
		}
		fmt.Fprintln(os.Stderr, "Error: at least one role is required.")
	}

	// Beads opt-in prompt.
	useBeads := prompt(reader, "Use beads for issue tracking? (y/n)", "y")
	beadsEnabled := strings.HasPrefix(strings.ToLower(useBeads), "y")

	var beadsCfg config.BeadsConfig
	if beadsEnabled {
		prefix := prompt(reader, "Beads prefix", name[:min(3, len(name))])
		beadsCfg = config.BeadsConfig{Enabled: boolPtr(true), Prefix: prefix}
	} else {
		beadsCfg = config.BeadsConfig{Enabled: boolPtr(false)}
	}

	p := &config.Project{
		Name:  name,
		Root:  root,
		Roles: roleList,
		Beads: beadsCfg,
	}

	if repoURL != "" {
		repoName := filepath.Base(strings.TrimSuffix(repoURL, ".git"))
		p.Repos = []config.Repo{{URL: repoURL, Name: repoName}}
	}

	return p, nil
}

// roleSpec defines the display properties of one role in the selector.
type roleSpec struct {
	name    string
	group   string
	desc    string
	tooltip string
}

// selectorOrder defines the grouped display order for the role selector.
// Items appear in this order; group headers are inserted automatically when
// the group field changes.
var selectorOrder = []roleSpec{
	{"super", "COORDINATORS", "Coordinator/dispatcher", "Dispatches beads to agents and monitors progress. Supervised mode."},
	{"pm", "PRODUCT", "Product manager", "Writes PRDs, specs, and acceptance criteria for beads."},
	{"pmm", "PRODUCT", "Product marketing manager", "Positioning, messaging, and go-to-market strategy."},
	{"arch", "PRODUCT", "Architect", "Designs system architecture, API contracts, and cross-package interfaces."},
	{"eng1", "ENGINEERS", "Engineer", "Implements features and fixes. Gets a src/ clone of your repo."},
	{"eng2", "ENGINEERS", "Engineer", "Implements features and fixes. Gets a src/ clone of your repo."},
	{"eng3", "ENGINEERS", "Engineer", "Implements features and fixes. Gets a src/ clone of your repo."},
	{"qa1", "QA", "QA tester", "Validates bead acceptance criteria. Gets src/ and playbooks/."},
	{"qa2", "QA", "QA tester", "Validates bead acceptance criteria. Gets src/ and playbooks/."},
	{"shipper", "OPERATIONS", "Release manager", "Cuts releases, manages changelogs, and publishes to package registries."},
	{"sec", "OPERATIONS", "Security reviewer", "Reviews code for vulnerabilities, OWASP top 10, supply chain risks."},
	{"ops", "OPERATIONS", "Operations/UX tester", "Tests UX flows, accessibility, and operational readiness."},
	{"writer", "OPERATIONS", "Technical writer", "Writes docs, READMEs, and user guides."},
	{"growth", "OPERATIONS", "Growth engineer", "Analytics, onboarding funnels, and conversion optimization."},
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
			Tooltip:     spec.tooltip,
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
			Tooltip:     spec.tooltip,
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
		fmt.Printf("%s %s: ", color.Cyan(label), color.Dim("["+defaultVal+"]"))
	} else {
		fmt.Printf("%s: ", color.Cyan(label))
	}

	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

func boolPtr(b bool) *bool { return &b }
