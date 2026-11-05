package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// The send path's timers run as written on a synctest bubble's clock in the
// send-path tests (ini-8yrl), so nothing there would notice a changed value.
// Each production value is pinned here by value, with no waiting.

func TestSendTimers_ConfirmEchoAndDrainKeepTheirValues(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want time.Duration
	}{
		{"submitConfirmWindow", submitConfirmWindow, 1000 * time.Millisecond},
		{"composerEchoWindow", composerEchoWindow, 1500 * time.Millisecond},
		{"queueDrainInterval", queueDrainInterval, 500 * time.Millisecond},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
}

// The stash pause is a literal inside sendPaneTextLocked, so it is read from
// the source: exactly one literal time.Sleep there, of 75 * time.Millisecond
// (its other sleeps are the named delays, pinned by value above and in
// submit_delay_test.go).
func TestSendTimers_TheStashPauseIs75ms(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "ipc.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "sendPaneTextLocked" {
			return true
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || selectorPath(call.Fun) != "time.Sleep" || len(call.Args) != 1 {
				return true
			}
			b, ok := call.Args[0].(*ast.BinaryExpr)
			if !ok {
				return true // a named delay, pinned elsewhere
			}
			lit, _ := b.X.(*ast.BasicLit)
			unit := selectorPath(b.Y)
			if lit != nil {
				found = append(found, lit.Value+" * "+unit)
			}
			return true
		})
		return false
	})
	if len(found) != 1 || found[0] != "75 * time.Millisecond" {
		t.Fatalf("sendPaneTextLocked sleeps %v, want exactly one literal, [75 * time.Millisecond]", found)
	}
}
