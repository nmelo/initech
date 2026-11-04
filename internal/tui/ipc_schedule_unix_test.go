//go:build linux || darwin

package tui

import (
	"testing"
	"time"
)

// TestCron_DeliveryObeysDoNotDisturb is AC5 by effect: the message goes to the
// send path, which holds it for a pane in do-not-disturb.
func TestCron_DeliveryObeysDoNotDisturb(t *testing.T) {
	p, _ := dyvPane(t, "sleep 600")
	p.name = "qa1"
	p.startDNDFor(time.Now(), 0)
	tui, _ := cronTUI(t, p)
	ok, why := tui.deliver("qa1", "[from eng1, schedule s1] standup", "eng1")
	if !ok {
		t.Fatalf("a held message read as not delivered: %q", why)
	}
	if n := p.QueuedMessageCount(); n != 1 {
		t.Fatalf("do-not-disturb did not hold the message: queue %d", n)
	}
}
