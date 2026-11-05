package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/fleetquota"
)

// ini-kb4c hookup: the bar's fleet quota is the poller's last reading, read
// on the machine ceiling's minute tick; a dash when unknown.

var q0 = time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC)

func usageFile(t *testing.T, util int, polled time.Time) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "usage.json")
	body := `{"acct-a":{"sevenDay":{"utilization":` + strconv.Itoa(util) + `},"polledAt":"` + polled.Format(time.RFC3339Nano) + `"}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func useQuotaPath(t *testing.T, path string) {
	t.Helper()
	orig := quotaPath
	t.Cleanup(func() { quotaPath = orig })
	quotaPath = func() (string, error) { return path, nil }
}

func TestQuotaText_PercentAndFourCells(t *testing.T) {
	for p, want := range map[int]string{0: "0% ░░░░", 30: "30% ▓░░░", 58: "58% ▓▓░░", 70: "70% ▓▓▓░", 100: "100% ▓▓▓▓"} {
		if got, ok := quotaText(fleetquota.Quota{Percent: p, Known: true}); !ok || got != want {
			t.Errorf("%d%%: %q %v, want %q", p, got, ok, want)
		}
	}
	if got, ok := quotaText(fleetquota.Quota{}); ok || got != "" {
		t.Errorf("unknown: %q %v, want a dash", got, ok)
	}
}

// The boot start reads the file and sets the bar's source.
func TestPanelQuota_TheStartReadsAndSetsTheBar(t *testing.T) {
	useQuotaPath(t, usageFile(t, 58, q0))
	tui := newTestTUI()
	tui.startPanelQuota(q0.Add(time.Minute))
	if tui.panelQuota == nil {
		t.Fatal("no quota source behind the bar")
	}
	if got, ok := tui.panelQuota.QuotaText(); !ok || got != "58% ▓▓░░" {
		t.Fatalf("QuotaText = %q %v", got, ok)
	}
}

// The minute tick reads it again: a new file value shows after the tick.
func TestPanelQuota_TheCeilingTickReadsIt(t *testing.T) {
	path := usageFile(t, 30, q0)
	useQuotaPath(t, path)
	tui := newTestTUI()
	tui.ceiling.reads = ceilingAt(t, 5).reads()
	tui.startPanelQuota(q0)
	body := `{"acct-a":{"sevenDay":{"utilization":70},"polledAt":"` + q0.Add(2*time.Minute).Format(time.RFC3339Nano) + `"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tui.refreshMachineCeiling(q0.Add(2 * time.Minute))
	waitIdle(t, &tui.ceiling.busy)
	if got, _ := tui.panelQuota.QuotaText(); got != "70% ▓▓▓░" {
		t.Fatalf("after the tick: %q, want 70%%", got)
	}
}

// waitIdle waits for a busy flag the code clears when its goroutine ends;
// it waits on that event, bounded, never a fixed sleep.
func waitIdle(t *testing.T, busy interface{ Load() bool }) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		for busy.Load() {
			runtime.Gosched()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tick's goroutine never finished")
	}
}

// Feed it nothing: no file is a dash on the bar.
func TestPanelQuota_NoFileIsADash(t *testing.T) {
	useQuotaPath(t, filepath.Join(t.TempDir(), "absent.json"))
	tui := newTestTUI()
	tui.startPanelQuota(q0)
	if _, ok := tui.panelQuota.QuotaText(); ok {
		t.Fatal("a missing file showed a number")
	}
}

// The package's tests never read the operator's usage file.
func TestPanelQuota_TestsReadNoRealFile(t *testing.T) {
	p, err := quotaPath()
	if err != nil {
		t.Fatal(err)
	}
	if home, _ := os.UserHomeDir(); home != "" && filepath.Dir(filepath.Dir(p)) == home {
		t.Fatalf("tests read %s", p)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatalf("the test quota path %s exists", p)
	}
}

// Run starts the quota beside the boot ceiling reading: read from the source,
// since Run is the whole TUI and no unit test drives it.
func TestQuotaCensus_RunStartsTheQuota(t *testing.T) {
	src, err := os.ReadFile("tui.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "tui.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Run" || fn.Recv != nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "startPanelQuota" {
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("Run never calls startPanelQuota: the bar's quota would stay a dash")
	}
}
