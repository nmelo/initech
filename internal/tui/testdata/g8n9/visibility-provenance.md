The retained logs show rapid, repeated single-pane visibility changes, not one
atomic hide of all eight agents and not simply eight unrelated changes spread
over hours. They do **not** record successful hidden writes or the input that
caused them. Deliberate operator action cannot be established from this record.

This follow-up covers September 10 13:24 through September 11 16:25, 2026,
in the logs' `-04:00` timezone. `provenance-log-manifest.json` records the exact
log byte snapshots and hashes. `provenance-evidence.txt` preserves selected
verbatim records with original file/line references. Relevant production files
are identical between v2.12.0 and the investigation baseline `694985f`.

The first engineering-agent removal sequence is on September 10:

| Agent | Inferred removal from plan | `initech.log.1` layout line |
| --- | --- | --- |
| eng1 | 13:33:08.152 | 1106 |
| eng2 | 13:33:08.484 | 1116 |
| eng3 | 13:33:08.713 | 1128 |
| eng4 | 13:33:08.948 | 1136 |
| eng5 | 13:33:09.464 | 1144 |

At line 1104 the sampled plan contains all five plus growth/pm/super. The
successive layout counts are 8→7→6→5→4→3. Surviving agents' resize indices shift
left in the same sequence; line 1152 confirms only growth/pm/super remain.
These timestamps identify the replans that remove each agent, rather than a
logged call to SetHidden. They span 1.312 seconds, with separate intermediate
plans. A single atomic bulk mutation would not produce those intermediate
counts through the current planner.

The engineering agents subsequently return and disappear again:

- September 11 12:39:52.632–53.689: counts increase one at a time, 6→11.
  Resize records name eng1 through eng5; sampled plan line 33660 includes all
  five. The earlier removals therefore were not permanent.
- 13:16:24.910: eng4 disappears (layout line 36574; sampled absence at 36588).
  13:16:25.215: eng5 disappears (layout line 36590; sampled absence at 36618).
- 13:20:05.193/.502/06.173: eng1/eng2/eng3 disappear in order (layout lines
  37228/37250/37282); line 37302 confirms all three absent.
- At 14:40:18.256 the newer main's sampled plan includes eng1, while the
  viewer's interleaved frames are empty. By 14:40:22.777 that main's plan has
  removed eng1 again.
- At 14:41:39.966 the newer main's plan includes all five. A 12→5 sequence at
  14:41:50.578–52.577 removes super/pm and the engineering agents. The sampled
  plan at line 50327 contains none of them. The viewer remains empty throughout.

No qa1/qa2/qa3 pane appears in any sampled plan in `initech.log.1`. Their first
recorded entry into Hidden cannot be dated from that file: absence from a plan
alone does not prove a hidden flag, and there is no successful-write log.
In particular, the first retained plan at September 10 13:24 contains all five
engineering agents **but no QA agents**. The proposed interval does not have
a known-visible starting observation for QA.

The later log provides a clearer per-agent removal sequence for all eight.
At September 11 16:00:29.831 the main plans all 41 panes (line 936), confirmed
by the sampled full set at line 967. Between 16:00:32.700 and 16:00:49.071 it
steps down nearly one pane at a time, with occasional immediate reversals,
to one pane. The target-agent portion is:

| Agent | First removal in this sequence | Subsequent return / final observed removal | `initech.log` lines |
| --- | --- | --- | --- |
| eng1 | 16:00:45.669 | Returns 16:01:06.513; removed 16:02:09.286 | 3029, 3797, 5482 |
| eng2 | 16:00:45.935 | No later sampled return before 16:25 | 3055 |
| eng3 | 16:00:46.180 | No later sampled return before 16:25 | 3072 |
| eng4 | 16:00:46.409 | No later sampled return before 16:25 | 3093 |
| eng5 | 16:00:46.648 | Returns 16:00:46.904; removed 16:01:07.498 | 3109, 3126, 3836 |
| qa1 | 16:00:48.455 | No later sampled return before 16:25 | 3187 |
| qa2 | 16:00:48.735 | No later sampled return before 16:25 | 3204 |
| qa3 | 16:00:49.071 | No later sampled return before 16:25 | 3222 |

The resize indices and named surviving panes support these removals, not just
the total counts. For example, the eng5 removal at 46.648 leaves a complete
three-pane resize list qa1/qa2/qa3. Its return at 46.904 precedes a sampled
eng5/qa1/qa2/qa3 plan; the QA removals then leave eng5 alone. Later a two-pane
eng1/eng5 layout becomes eng1 alone, and finally the named three-pane
eng1/mktg/ae layout becomes mktg/ae. This is stronger evidence for visibility
toggles than a single focus-mode plan changing from one agent to another.

These are **plan-removal timestamps inferred to reflect hide/unhide**, not an
audit trail of persisted flag writes. The final captured fleet store agrees
that all eight are hidden. Its 16:25 mtime cannot date the hides: FleetState.save
serializes the entire store for protection, suspension and pin changes too.

The source-path inventory narrows attribution but cannot choose an input source:

| Path at v2.12.0 / 694985f | Can set hidden=true? | Available log evidence |
| --- | --- | --- |
| Agents modal Space → agentsToggleVisibility → toggleHidden → setHidden | Yes, one selected agent | Key events DEBUG only; no success log |
| Searching Agents modal Space → same path | Yes, one selected agent | Same |
| Overlay dot Button1 → toggleHidden → setHidden | Yes, one row | Mouse event DEBUG only; no success log |
| Viewer control `set_fleet_state` → applyFleetStateField → setHidden | Yes | INFO control-action records exist, but all 161 retained commands are `ping` |
| LoadFleetState / absent-store legacy import | Loads hidden values | No successful-load hidden-set log; unreadable-store warnings absent |
| Remove-agent cleanup → setHidden(false) | No, clears only | No removal records at the sequences |
| ClearHidden (modal A) | No, clears all | Success notice DEBUG only |
| Suspend, protect or pin save | Does not change the in-memory hidden map, but rewrites its current full contents | No full-state write audit |

`setHidden` uses the action string `hide <agent>` even for `hidden=false`, and
`broadcastSessionNotice` records that string only at DEBUG. FleetState.save
does not log successful writes. Both logs have zero DEBUG records and zero
matches for hide/hidden/fleet-display/input/mouse in the requested interval;
all eight startup records say `verbose=false`. Thus there is no recorded
keyboard-versus-mouse decision to recover. Repeated per-agent removals and
immediate reversals fit rapid UI toggles, including accidental input or an
input-handling defect. Cadence alone does not distinguish intent. Overlay
Button1 handling also does not, by itself, establish a fresh button-down edge
before toggling, so mouse event repetition remains a discriminating experiment,
not an established field cause.

A separate confound prevents treating all these frames as one authoritative
session. New main PIDs 49006, 71903, 69124 and 4458 report `bind: address already
in use` on port 9301 at September 10 14:01:31 and September 11 12:00:00,
12:27:26 and 16:00:02 respectively. Startup continues after that error. Those
TUIs still identify as window 1 and can mutate the shared fleet store, while
their nil window server cannot broadcast changes. A viewer can therefore
remain attached to the older server while a newer main shows different state.
The logs directly demonstrate bind failures and simultaneous differing plans;
which process last wrote each flag is not logged. This is an investigation
lead, not a reproduced multi-authority root cause.

To resolve attribution on the next occurrence, capture semantic hidden
transitions at the mutation boundary: agent, previous/new boolean, source
(modal/overlay/remote/load), PID/window/session, and persistence result. For
the old-versus-new-main hypothesis, identify the port-owning PID and every
writer's session, and assert a composed second-main startup either refuses
or joins the existing authority. Raw key logging alone is insufficient and
would collect unrelated input. No operator process was changed or instrumented
during this read-only trace.

The requested product follow-up must account for existing UI behavior:
agents_grid.go already renders hidden agents as `[ ]` with gray/italic names
(visible agents use `[x]`). They remain under their assigned monitor tier.
The unresolved product question is whether those marks sufficiently convey
“assigned here but hidden everywhere,” and what recovery interaction is clear
after the viewer's modal was removed. It would be inaccurate to file a defect
claiming no hidden mark exists.
