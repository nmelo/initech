package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// The app service types into panes through the commands' own code: initech
// keystroke's for a key, against the rig's socket, as the operator. The
// verdict and the words are what the command decided and printed, captured,
// never rebuilt; the dev-build guard applies, since the call is
// ipcCallSocket's.

// errNoCall is the caller a refusal check runs with: a key outside the table
// is refused before any call, so reaching it is a defect.
var errNoCall = errors.New("app-serve: a key check reached the TUI")

func noCall(tui.IPCRequest) (*tui.IPCResponse, error) { return nil, errNoCall }

// rigCaller is the IPC round trip to one rig's TUI.
func rigCaller(r appserve.Rig) ipcCaller {
	return func(req tui.IPCRequest) (*tui.IPCResponse, error) { return ipcCallSocket(r.Socket, req) }
}

// KeyRefusal is initech keystroke's own refusal line for a key outside its
// table, and false; "" and true for a key it takes. Nothing is called.
func (b *appBackend) KeyRefusal(pane, key string) (string, bool) {
	if tui.KeystrokeKnown(key) {
		return "", true
	}
	err := keystrokeVia(io.Discard, noCall, pane, "", key, false, tui.OperatorSender)
	if err == nil {
		err = errNoCall // the command took a key its table lacks: say so, never guess its line
	}
	return err.Error(), false
}

// Key presses key in pane of rig r as initech keystroke does, as the
// operator: delivered with the line it prints, or not delivered with the
// line it fails with.
func (b *appBackend) Key(r appserve.Rig, pane, key string) appserve.Typed {
	var out bytes.Buffer
	if err := keystrokeVia(&out, rigCaller(r), pane, "", key, false, tui.OperatorSender); err != nil {
		return appserve.Typed{Verdict: tui.DeliveryNotDelivered, Words: err.Error()}
	}
	return appserve.Typed{Verdict: tui.DeliveryDelivered, Words: strings.TrimRight(out.String(), "\n")}
}
