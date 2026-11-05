package appserve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// TailnetRetryFirst and TailnetRetryCap bound the wait for a tailnet address:
// the first retry comes TailnetRetryFirst after the first miss, each wait
// doubles, and none is longer than TailnetRetryCap. Over ten minutes with no
// address that is 14 tries.
const (
	TailnetRetryFirst = 2 * time.Second
	TailnetRetryCap   = 60 * time.Second
)

// TailnetWaitLine is the one line the service logs when it starts waiting
// for a tailnet address. Nothing is logged per try.
const TailnetWaitLine = "initech app-serve: no tailnet address on this Mac; waiting for Tailscale"

// ErrWaitStopped is WaitForTailnet's answer when its stop channel closed
// (the service is being replaced by a new build) before an address appeared.
var ErrWaitStopped = errors.New("the wait for a tailnet address was stopped")

// WaitForTailnet returns the address find gives, waiting for it while find
// answers ErrNoTailnet. It tries at once; on the first ErrNoTailnet it logs
// TailnetWaitLine and calls onWait (when not nil), once each, then tries again
// on after's timer from TailnetRetryFirst, doubling to TailnetRetryCap. It
// never picks an address itself, so it can never bind wider than find allows.
// Any other error from find is returned as it is. ctx ending returns
// ctx.Err(); stop closing returns ErrWaitStopped; a nil stop never closes.
func WaitForTailnet(ctx context.Context, stop <-chan struct{}, find func() (net.IP, error),
	after func(time.Duration) <-chan time.Time, log io.Writer, onWait func()) (net.IP, error) {
	wait := TailnetRetryFirst
	waiting := false
	for {
		ip, err := find()
		if err == nil {
			return ip, nil
		}
		if !errors.Is(err, ErrNoTailnet) {
			return nil, err
		}
		if !waiting {
			waiting = true
			fmt.Fprintln(log, TailnetWaitLine)
			if onWait != nil {
				onWait()
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-stop:
			return nil, ErrWaitStopped
		case <-after(wait):
		}
		wait = min(wait*2, TailnetRetryCap)
	}
}

// ReportTailnetWait runs notify with TailnetWaitLine under the tailnet wait's
// own one-per-hour guard, so a service that loses and regains Tailscale often
// notifies at most hourly and never silences the restart, folder or upgrade
// notices. The caller logs the line (WaitForTailnet does).
func ReportTailnetWait(stateDir, notify string, log io.Writer) {
	notifyGuarded(stateDir, TailnetNotifiedAtFile, notify, TailnetWaitLine, log, time.Now)
}
