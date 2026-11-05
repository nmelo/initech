package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/machine"
	"github.com/nmelo/initech/internal/rignumbers"
	"github.com/nmelo/initech/internal/tui"
)

// ini-49q: status prints the machine ceiling's fleet line after the agent
// table, says when the machine is at its ceiling, carries the fleet fields
// in --json --numbers, and names a refused agent's words on its row.

func fleetAt(awake int, free, load float64) *rignumbers.Fleet {
	r := machine.Reading{Awake: awake, AwakeOK: true, FreeGB: free, FreeOK: true, Load: load, LoadOK: true, SwapGB: 2.3, SwapOK: true}
	c := machine.DefaultCeiling
	return tui.FleetFromReading(r, c, machine.Over(r, c))
}

func noStatusFleetRead(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := statusReadFleet
	t.Cleanup(func() { statusReadFleet = orig })
	statusReadFleet = func(time.Time) *rignumbers.Fleet { calls++; return nil }
	return &calls
}

func TestStatusFleet_TheLineFollowsTheTableAndSaysAtTheCeiling(t *testing.T) {
	var c countingRigRun
	c.install(t)
	calls := noStatusFleetRead(t)
	resp := paneReply(windowOneNumbers())
	resp.Fleet = fleetAt(31, 6.1, 34)
	out := runStatusWith(t, resp, false, false)
	// The rig line is the panel bar's left part (ini-9z0b), so it starts
	// with merges/h, no label.
	want := "  fleet 31 awake · 6.1 GB free · swap 2.3 GB · load 34 · ceiling 30 / 8 GB (default) / 30 · at the ceiling\n  merges/h "
	if !strings.Contains(out, want) {
		t.Errorf("status:\n%s\nwant the fleet line before the rig line:\n%q", out, want)
	}
	if *calls != 0 {
		t.Error("status read the machine itself with window 1's reading in hand")
	}
}

func TestStatusFleet_UnderTheCeilingTheLineSaysNoMore(t *testing.T) {
	var c countingRigRun
	c.install(t)
	noStatusFleetRead(t)
	resp := paneReply(windowOneNumbers())
	resp.Fleet = fleetAt(12, 20, 5)
	out := runStatusWith(t, resp, false, false)
	if !strings.Contains(out, "  fleet 12 awake · 20.0 GB free · swap 2.3 GB · load 5 · ceiling 30 / 8 GB (default) / 30\n") {
		t.Errorf("status:\n%s", out)
	}
}

func TestStatusFleet_WithNoReadingFromWindowOneStatusReadsTheMachine(t *testing.T) {
	var c countingRigRun
	c.install(t)
	calls := noStatusFleetRead(t)
	statusReadFleet = func(time.Time) *rignumbers.Fleet { *calls++; return fleetAt(3, 20, 5) }
	out := runStatusWith(t, paneReply(windowOneNumbers()), false, false)
	if *calls != 1 || !strings.Contains(out, "  fleet 3 awake") {
		t.Errorf("reads %d, status:\n%s", *calls, out)
	}
}

func TestStatusFleet_NumbersJSONCarriesTheFleetFields(t *testing.T) {
	var c countingRigRun
	c.install(t)
	noStatusFleetRead(t)
	resp := paneReply(windowOneNumbers())
	resp.Fleet = fleetAt(31, 6.1, 34)
	var rig struct {
		Numbers struct {
			Fleet *struct {
				Awake     *int     `json:"awake"`
				FreeGB    *float64 `json:"free_gb"`
				Load      *float64 `json:"load"`
				SwapGB    *float64 `json:"swap_gb"`
				AtCeiling bool     `json:"at_ceiling"`
				Ceiling   struct {
					Sessions   int    `json:"sessions"`
					FreeSource string `json:"free_memory_source"`
				} `json:"ceiling"`
			} `json:"fleet"`
		} `json:"numbers"`
	}
	if err := json.Unmarshal([]byte(runStatusWith(t, resp, true, true)), &rig); err != nil {
		t.Fatal(err)
	}
	f := rig.Numbers.Fleet
	if f == nil || f.Awake == nil || *f.Awake != 31 || f.FreeGB == nil || *f.FreeGB != 6.1 || f.Load == nil || *f.Load != 34 || !f.AtCeiling || f.Ceiling.Sessions != 30 || f.SwapGB == nil || *f.SwapGB != 2.3 || f.Ceiling.FreeSource != "default" {
		t.Errorf("--json --numbers fleet = %+v", f)
	}
}

func TestStatusFleet_ARefusedAgentsRowCarriesTheWords(t *testing.T) {
	var c countingRigRun
	c.install(t)
	noStatusFleetRead(t)
	words := "not started: the Mac is at its ceiling: 31 awake sessions, 6.1 GB free, load 34; ceiling 30 / 8 GB / 30"
	panes, _ := json.Marshal([]map[string]any{{"name": "eng1", "activity": "suspended", "alive": false, "visible": true, "ceiling": words}})
	out := runStatusWith(t, tui.IPCResponse{OK: true, Data: string(panes)}, false, false)
	if !strings.Contains(out, "["+words+"]") {
		t.Errorf("status:\n%s\nwant the refusal on eng1's row", out)
	}
}
