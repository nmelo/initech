// perf_marker.go is the in-app half of the efficiency rig's message-delivery
// reading (ini-pqdy.4): a send whose text carries an IQPERF-<n> token is
// stamped three times in the log -- accepted by the IPC handler, written to
// the pane's PTY, and seen coming back in the pane's output -- so delivery
// latency can be split into initech's own hops.
//
// COST WHEN UNUSED: one atomic load per PTY read. The token match runs only
// while a pane is armed, which only a send carrying the token does.
package tui

import (
	"bytes"
	"regexp"
	"strings"
)

const perfMarkerPrefix = "IQPERF-"

var perfMarkerRe = regexp.MustCompile(`IQPERF-[0-9]+`)

// perfMarkerToken returns the IQPERF-<n> token in text, or "".
func perfMarkerToken(text string) string {
	if !strings.Contains(text, perfMarkerPrefix) {
		return ""
	}
	return perfMarkerRe.FindString(text)
}

// perfMarkerArm holds an armed token and the tail of the previous read, so a
// token split across two PTY reads is still seen.
type perfMarkerArm struct {
	token []byte
	tail  []byte
}

// armPerfMarker arms this pane for token. Called BEFORE the write, so the echo
// cannot race ahead of the arming.
func (p *Pane) armPerfMarker(token string) {
	p.perfMarker.Store(&perfMarkerArm{token: []byte(token)})
}

// notePerfMarkerWritten stamps the moment a marked message's body reached the
// pane's PTY. Only a pane armed for the token in text logs anything.
func notePerfMarkerWritten(p *Pane, text string) {
	arm := p.perfMarker.Load()
	if arm == nil || !strings.Contains(text, string(arm.token)) {
		return
	}
	LogInfo("perf", "marker written", "pane", p.name, "marker", string(arm.token))
}

// checkPerfMarker is readLoop's hook: when armed and data (with the previous
// read's tail) contains the token, log the echo and disarm. readLoop is the
// only caller, so the tail needs no lock.
func (p *Pane) checkPerfMarker(data []byte) {
	arm := p.perfMarker.Load()
	if arm == nil {
		return
	}
	hay := data
	if len(arm.tail) > 0 {
		hay = append(append([]byte(nil), arm.tail...), data...)
	}
	if bytes.Contains(hay, arm.token) {
		if p.perfMarker.CompareAndSwap(arm, nil) {
			LogInfo("perf", "marker echoed", "pane", p.name, "marker", string(arm.token))
		}
		return
	}
	keep := len(arm.token) - 1
	if keep > len(hay) {
		keep = len(hay)
	}
	arm.tail = append(arm.tail[:0], hay[len(hay)-keep:]...)
}
