// templatecensus enforces that every `initech <verb>` an agent's role template
// teaches resolves to a command this binary actually registers (ini-j0er).
//
//	go run ./scripts/templatecensus
//
// # WHY THIS EXISTS
//
// On 2026-09-12 the role templates taught `initech post` in 11 places while
// cmd/post.go did not exist, post was registered nowhere, and the installed
// binary answered `unknown command "post"`. CI was 4/4 GREEN at that sha and
// would be green for any future instance: templates.go compiles,
// templates_test.go passes because it asserts the template TEXT, and nothing
// anywhere linked a taught verb to a registered command. The only thing that
// stopped it shipping was one engineer telling shipper about the coupling by
// hand. The next instance, with no message, ships — and what ships is a fleet
// of agents whose own CLAUDE.md tells them to run a command that errors.
//
// Same silent-omission class as its siblings: rigcensus (a rig nobody runs),
// testcensus (a test tagged off a platform), the target census (a make target
// nothing invokes). A hand-maintained correspondence with no artifact when it
// is wrong. The fix is the same one every time: DERIVE the answer from the
// inventory instead of trusting that someone remembered.
//
// # WHAT IS SCANNED, AND WHY IT IS NOT JUST THE BACKTICKED MENTIONS
//
// Every `initech <verb>` in every string constant of internal/roles — prose
// and code alike, no backtick filter.
//
// The first design counted only verbs inside backticks or fences, because
// that separates taught commands from prose cleanly in today's corpus. Shipper
// flagged the word "today": it leaves the census resting on a convention the
// census does not enforce, so the day someone writes "run initech post to
// publish" in bare prose, the extractor skips it, the verb is unregistered,
// and the check reports GREEN on exactly the defect it exists to catch — an
// instrument reading less than the surface it claims.
//
// Measured before widening, over the whole corpus: 23 distinct verbs are
// mentioned, ~50 of the mentions are bare, and every bare one is a real
// command ("If initech deliver is unavailable…", "initech add <role> is a
// SESSION operation") except a single English sentence that happened to read
// "before initech sees them", which was reworded rather than exempted. So
// reading the whole surface costs zero exemptions and removes the convention
// entirely. Scan everything; resolve everything.
//
// # SCAFFOLD OUTPUT
//
// The source census is supplemented by running the scaffold for roles.Catalog
// and walking every file it writes. This catches instructions from any writer,
// including static text that never passes through roles.Render (ini-zfbb).
// Named template constants must also appear in the generated output; otherwise
// a smaller fixture could silently omit the very instructions being checked.
// This replaces the former static scaffold guard, whose source-shape selectors
// repeatedly missed new writers. The output check runs in the CLI census, so
// make check-fast enforces it as well as make test.
//
// Deliberately NOT scanned: cobra help strings in cmd/. They mention initech
// verbs constantly and are shown to a HUMAN at a terminal, not rendered into
// an agent's instructions, so an unregistered verb there is a typo in help
// text rather than an agent told to run a command that errors.
//
// # WHERE THE REGISTERED SET COMES FROM
//
// cmd.RegisteredVerbs(), which walks the cobra command tree — not a grep for
// `Use:` lines. Aliases are commands: `hire` and `fire` resolve to add-agent
// and delete-agent, and a grep-based list reports them as failures. (A
// hand-typed list of registered commands produced a false positive on
// delete-agent within a minute of being written while this tool was designed.
// That is the whole argument in one data point.)
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nmelo/initech/cmd"
)

const (
	exemptionsPath = ".github/template-verb-exemptions.txt"
	scanDir        = "internal/roles"
)

// verbPattern matches a taught command: "initech " followed by a verb-shaped
// word. Deliberately not anchored to backticks — see the package comment.
var verbPattern = regexp.MustCompile(`\binitech ([a-z][a-z0-9-]*)`)

// mention is one `initech <verb>` occurrence, located for an error message
// that an author can act on without searching (this fires at commit time).
type mention struct {
	verb  string
	file  string
	line  int
	konst string
}

func main() {
	exemptions := flag.String("exemptions", exemptionsPath, "path to the exemption file")
	verbose := flag.Bool("v", false, "list every mention found")
	flag.Parse()

	if err := run(*exemptions, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "\ntemplatecensus: "+err.Error())
		os.Exit(1)
	}
}

func run(exemptionsFile string, verbose bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	mentions, err := scanMentions(filepath.Join(root, scanDir))
	if err != nil {
		return err
	}
	if len(mentions) == 0 {
		return fmt.Errorf("found no `initech <verb>` mentions in %s at all — the scanner is broken, not the templates clean", scanDir)
	}

	if err := checkScaffold(root, verbose); err != nil {
		return err
	}

	registered := map[string]bool{}
	for _, v := range cmd.RegisteredVerbs() {
		registered[v] = true
	}
	exempt, err := loadExemptions(filepath.Join(root, exemptionsFile))
	if err != nil {
		return err
	}

	var unresolved []mention
	taught := map[string]bool{}
	for _, m := range mentions {
		taught[m.verb] = true
		if verbose {
			fmt.Printf("  %s:%d %s (%s)\n", m.file, m.line, m.verb, m.konst)
		}
		if registered[m.verb] || exempt[m.verb] {
			continue
		}
		unresolved = append(unresolved, m)
	}

	// Stale exemptions fail, like every other census in this family: an
	// exemption that has done its job must be removed, and the tool is what
	// makes that happen rather than someone's memory. This is load-bearing
	// for the coupling that motivated the census -- when the post command
	// lands, its exemption goes stale and this fails until it is deleted.
	var stale []string
	for verb := range exempt {
		switch {
		case registered[verb]:
			stale = append(stale, fmt.Sprintf("%s — now a registered command; delete this line", verb))
		case !taught[verb]:
			stale = append(stale, fmt.Sprintf("%s — no template teaches it any more; delete this line", verb))
		}
	}
	sort.Strings(stale)

	if len(unresolved) > 0 || len(stale) > 0 {
		var b strings.Builder
		if len(unresolved) > 0 {
			fmt.Fprintf(&b, "%d template mention(s) of a command this binary does not register:\n", len(unresolved))
			for _, m := range unresolved {
				fmt.Fprintf(&b, "  %s:%d  initech %s  (in %s)\n", m.file, m.line, m.verb, m.konst)
			}
			b.WriteString("\nAn agent's CLAUDE.md must not teach a command that errors. Either register the\n")
			b.WriteString("command, fix the text, or — if the command is landing in another bead — add the\n")
			b.WriteString("verb to " + exemptionsFile + " with a TRIGGER saying when it goes away.\n")
		}
		if len(stale) > 0 {
			fmt.Fprintf(&b, "\n%d stale exemption(s) in %s:\n", len(stale), exemptionsFile)
			for _, s := range stale {
				fmt.Fprintf(&b, "  %s\n", s)
			}
		}
		return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
	}

	fmt.Printf("  OK: %d taught verb(s) across %d mention(s), all registered (%d exemption(s), all still applicable)\n",
		len(taught), len(mentions), len(exempt))
	return nil
}

// scanMentions parses every non-test Go file under dir and returns each
// `initech <verb>` occurrence inside a string constant.
//
// Constants, not rendered templates (the templates need no rendering to be
// read, and a census that required it would depend on the renderer it is
// meant to be independent of). Each string literal is scanned on its own, so
// a template built by concatenation — `... ` + OperatorInboxGuide + ` ...` —
// needs no reference resolution: the referenced constant is itself scanned
// where it is declared, which is also where an author would go to fix it.
//
// Go doc comments are not scanned, and that is deliberate: internal/roles has
// package comments containing the prose "initech projects", which is not a
// taught command. Reading the AST's constant values excludes comments by
// construction rather than by a filter someone has to maintain.
func scanMentions(dir string) ([]mention, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var out []mention
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				constName := "?"
				if len(vs.Names) > 0 {
					constName = vs.Names[0].Name
				}
				for _, val := range vs.Values {
					ast.Inspect(val, func(n ast.Node) bool {
						lit, ok := n.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							return true
						}
						text, err := strconv.Unquote(lit.Value)
						if err != nil {
							return true
						}
						base := fset.Position(lit.Pos()).Line
						for _, loc := range verbPattern.FindAllStringSubmatchIndex(text, -1) {
							out = append(out, mention{
								verb:  text[loc[2]:loc[3]],
								file:  filepath.Join(scanDir, name),
								line:  base + strings.Count(text[:loc[0]], "\n"),
								konst: constName,
							})
						}
						return true
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out, nil
}

// loadExemptions reads verb exemptions. Format, matching its siblings:
//
//	VERB  # why it is not registered yet. TRIGGER: when this line goes away.
//
// A TRIGGER is required: an exemption without one is a permanent hole nobody
// revisits, which is the thing these files exist to prevent.
func loadExemptions(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("read exemptions: %w", err)
	}
	out := map[string]bool{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		verb := strings.Fields(line)[0]
		if !strings.Contains(strings.ToUpper(line), "TRIGGER:") {
			return nil, fmt.Errorf("%s:%d: exemption for %q has no TRIGGER: — say when this line goes away, or it never will", path, i+1, verb)
		}
		out[verb] = true
	}
	return out, nil
}

func repoRoot() (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("locate module root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
