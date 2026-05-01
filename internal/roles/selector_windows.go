//go:build windows

// Windows stub for the interactive role selector. The full implementation in
// selector.go uses Unix terminal-mode APIs that aren't available on Windows.
// `initech init` is interactive bootstrapping; for the wintest-win use case we
// only need `initech serve`, so this stub satisfies the link without bringing
// the full selector across.

package roles

import "errors"

type SelectorItem struct {
	Name        string
	Description string
	Group       string
	Tag         string
	Tooltip     string
	Checked     bool
}

var ErrCancelled = errors.New("selection cancelled")

// RunSelector returns ErrCancelled on Windows. `initech init` interactive flow
// is not supported on this platform — use a pre-written initech.yaml.
func RunSelector(title string, items []SelectorItem, subtitle ...string) ([]string, error) {
	return nil, errors.New("interactive role selector not available on Windows; pre-write initech.yaml or run init from Linux/macOS")
}
