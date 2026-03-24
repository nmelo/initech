# Initech PRD

The "why" companion to `spec.md` (the what) and `systemdesign.md` (the how). Hard cap: 5000 lines.

---

## 1. Problem Statement

### 1.1 What Initech Solved

Nelson developed a local software development pattern where a tmux session becomes a virtual company. Each tmux window runs an autonomous Claude Code agent with a defined role. These agents coordinate through message-passing tools (gn/ga/gp/gm) and track work through beads (a git-backed issue tracker). The pattern shipped real software across four projects (beadbox, cobalt, nayutal_app, secure-infra).

The first problem was bootstrap: manually recreating project infrastructure caused fidelity loss and time waste. Initech v1 solved that. `initech init` produces a working project with correct CLAUDE.md files, git submodules, beads, and session configs. `initech up` starts the session. 10 commands, 11 role templates, 97 tests. The bootstrap problem is solved.

### 1.2 The Runtime Is the Bottleneck

Even with perfect setup, running a multi-agent session on tmux hits walls that can't be fixed by better configs:

**Unreliable coordination.** Messages go through tmux send-keys with no delivery guarantee. An agent issuing `gn` has no idea if the target received it. Silent failures corrupt the dispatch/report protocol the entire workflow depends on. An undelivered completion report means super doesn't know eng is done, which means qa doesn't get dispatched, which means the bead stalls for no reason.

**Blind resource consumption.** tmux has no concept of what's running inside its panes. A hung Claude instance and a productive one look identical. Memory pressure builds invisibly until the machine degrades, and the only remedy is manual triage. On a 36 GB laptop running 9 agents, memory exhaustion is a regular event, not an edge case.

**Observation doesn't scale.** Monitoring agents means tiling windows, bounded by screen size. On a 14" laptop, ~6 usable panes max. The information you actually need (active? stuck? idle? working on what?) doesn't require a full terminal view. The tmux window list at the bottom is wasted real estate that tells you window names and nothing else.

**Work is invisible to the runtime.** Beads track what work exists and who's assigned, but the runtime has no awareness of this. Super manually checks beads, manually monitors agents, manually dispatches. Completion is a silent event: an agent finishes, reports to super via ga, and super (if paying attention, if the message was delivered, if its context window hasn't compacted) dispatches new work. There's no system-level awareness that "eng1 is done and the queue has 3 ready beads." The orchestration lives entirely in super's context window, the most fragile storage medium in the system.

**Sessions are trapped on one machine.** All state is local process tree plus local files. Moving work between machines (e.g., from a 14" MacBook to a Ryzen workbench with 128 GB RAM) means manual rsync, re-setup, and lost context windows.

The through-line: tmux is a general-purpose terminal multiplexer being asked to do a job that requires application-level intelligence. It can't know about agent state, message delivery, resource pressure, work completion, or session portability because those concepts don't exist in its model.

### 1.3 Why Now

Initech v1 shipped all five phases. The bootstrap problem is solved. The runtime limitations are now the binding constraint on multi-agent productivity. The pattern is stable enough that building a purpose-built runtime is justified; we know exactly what the runtime needs to do because we've been working around its absence for months.

---

## 2. User

### 2.1 Primary User

Nelson. One person running multi-agent development sessions locally across two machines: a 14" MacBook Pro (36 GB, portable) and a Ryzen 9 workbench (128 GB, desktop). Uses Claude Code, beads, and the gastools (gn/ga/gp/gm). Technical proficiency: fluent in Go, Python, Java, C#; learning Rust. Domain: cybersecurity, IAM, zero trust.

### 2.2 Secondary Users (Future)

Other developers who adopt the multi-agent pattern. Not a priority for MVP, but the tool should not have Nelson-specific hardcoding (paths, repo URLs, role preferences). Everything project-specific lives in `initech.yaml`, not in the binary.

---

## 3. Success Criteria

### 3.1 Core Success

**Initech succeeds if:**

1. **Messages are guaranteed delivered or explicitly failed.** When an agent sends a message to another agent, initech confirms delivery or reports failure. No silent drops. Messages to suspended agents are queued and delivered on resume.

2. **Agent state is observable at a glance.** A status sidebar shows all agents with their state (active/idle/suspended/stuck), current bead, memory usage, and last activity. No window tiling required. Works on a 14" screen with 11 agents.

3. **Resource management is automatic.** Initech monitors memory per agent. Under pressure, it suspends least-recently-active agents. When a message targets a suspended agent, initech resumes it transparently with --continue before delivering.

4. **The runtime tracks bead assignments and detects work state changes.** Initech knows which bead each agent is working on via bd integration. It detects when an agent's output suggests completion (DONE comment pattern), stalling (no output for configurable threshold), or failure (error loops). It surfaces these as events, not something a human has to poll for.

5. **Work transitions trigger actionable alerts.** When an agent completes a bead, initech alerts the operator. When an agent stalls, initech flags it before a human would notice. When agents are idle and the backlog has ready work, initech surfaces the mismatch. Aggressive detection, conservative action: the operator decides what to dispatch.

6. **Sessions can be stopped, transferred, and resumed on another machine.** `initech migrate <target>` handles the transfer. Agent conversation history and bead state travel with it. 30-second downtime is acceptable.

7. **Zero tmux dependency.** Initech owns terminal multiplexing, PTY management, and process lifecycle directly. tmux and tmuxinator are not required.

### 3.2 Measurable Checks

- Agent-to-agent message delivery succeeds or returns explicit error within 5 seconds
- Status view renders 11 agents with live state on a 14" screen without scrolling
- Memory-pressured agent is auto-suspended and resumed transparently when messaged
- Bead completion detected within 60 seconds of agent's DONE comment
- Idle agent with ready backlog surfaced within 2 minutes
- Session migrated between Mac and Linux workbench; agents resume with --continue, no bead state lost
- Bootstrap still works: `initech init` + `initech up` produces a working session on first try

---

## 4. Non-Goals

Things initech explicitly does not do:

1. **Replace tmux or tmuxinator.** Initech generates tmuxinator configs and shells out to tmux. It does not reimplement session management.

2. **Replace beads.** Initech integrates with `bd` via CLI. It does not store or manage issues itself.

3. **Replace gastools.** Initech does not reimplement gn/ga/gp/gm. Agents and super continue to use those tools directly.

4. **Cloud or remote agents.** MVP is local-only. Agents run in local tmux windows on one machine.

5. **Multi-user coordination.** One person (Nelson) drives the session. No authentication, no access control, no concurrent human users.

6. **IDE integration.** No VS Code extension, no editor plugins. The interface is the terminal.

7. **Agent intelligence.** Initech does not make agents smarter. It makes them start with correct instructions (CLAUDE.md), correct permissions, and correct context. Intelligence comes from the role templates and beads, not from initech's code.

8. **Monitoring dashboard.** `initech status` prints a table. No web UI, no real-time updates, no graphs.

---

## 5. User Journeys

### 5.1 Install and Onboard

Nelson just wiped his machine, or a colleague wants to try the multi-agent pattern for the first time. They need to go from nothing to a working initech installation with all prerequisites satisfied.

```
$ brew tap nmelo/tap && brew install initech
$ initech version
initech v0.1.0 (darwin/arm64)

$ initech doctor

Checking prerequisites...

  tmux          3.4     /opt/homebrew/bin/tmux          ok
  tmuxinator    3.1.1   /opt/homebrew/bin/tmuxinator    ok
  claude        1.0.8   /Users/nelson/.claude/bin/claude ok
  git           2.43.0  /usr/bin/git                    ok
  bd            0.5.2   /opt/homebrew/bin/bd             ok
  gn            0.3.1   /opt/homebrew/bin/gn             ok
  gp            0.3.1   /opt/homebrew/bin/gp             ok
  gm            0.3.1   /opt/homebrew/bin/gm             ok

All prerequisites satisfied. Run 'initech init' in a project directory to get started.
```

If something is missing, doctor says exactly what and how to fix it:

```
$ initech doctor

Checking prerequisites...

  tmux          3.4     /opt/homebrew/bin/tmux          ok
  tmuxinator    -       -                               MISSING
  claude        1.0.8   /Users/nelson/.claude/bin/claude ok
  git           2.43.0  /usr/bin/git                    ok
  bd            -       -                               MISSING
  gn            0.3.1   /opt/homebrew/bin/gn             ok
  gp            0.3.1   /opt/homebrew/bin/gp             ok
  gm            -       -                               MISSING

2 issues found:

  tmuxinator: gem install tmuxinator
  bd, gm:     brew tap nmelo/tap && brew install bd gm
```

Doctor is also the first thing to run when something breaks mid-session. "Why is my agent not starting?" becomes "run initech doctor" instead of debugging PATH issues manually.

### 5.2 Start a New Project

Nelson has a new project idea. He wants to spin up a multi-agent team.

```
$ mkdir ~/Desktop/Projects/newproject && cd $_
$ initech init
Project name [newproject]: newproject
Code repo URL: git@github.com:nmelo/newproject.git
Roles [super,pm,eng1,eng2,qa1,qa2,shipper]: super,pm,arch,eng1,eng2,qa1,qa2,sec,shipper
Beads prefix [new]: np

Created:
  initech.yaml
  .beads/ (prefix: np)
  .gitignore
  CLAUDE.md
  AGENTS.md
  docs/prd.md
  docs/spec.md
  docs/systemdesign.md
  docs/roadmap.md
  super/CLAUDE.md
  pm/CLAUDE.md
  arch/CLAUDE.md
  eng1/CLAUDE.md + src/ (submodule)
  eng2/CLAUDE.md + src/ (submodule)
  qa1/CLAUDE.md + src/ (submodule)
  qa2/CLAUDE.md + src/ (submodule)
  sec/CLAUDE.md
  shipper/CLAUDE.md + src/ (submodule)
  ~/.config/tmuxinator/newproject.yml

Ready. Run 'initech up' to start.
```

Nelson reviews the generated CLAUDE.md files, makes project-specific tweaks (tech stack details, specific build commands, domain context), then starts.

### 5.3 Start the Day

```
$ cd ~/Desktop/Projects/newproject
$ initech up
Session 'newproject' started with 9 agents.

$ initech standup

## newproject Daily - 2026-02-15

### What's New
- np-a1f.3: Auth middleware (shipped)

### In Progress
- np-a1f.5: API endpoints (eng1)
- np-a1f.6: Client SDK (eng2)

### Next Up
- np-a1f.7: Integration tests
```

### 5.4 Check on the Team

```
$ initech status

Session: newproject (running, 9 agents, 14.2 GB total)

  Role      Claude  Bead                              Status          Mem
  super     yes     -                                 -            1.3 GB
  pm        yes     -                                 idle         1.1 GB
  arch      yes     -                                 idle         1.4 GB
  eng1      yes     np-a1f.5 (API endpoints)          in_progress  2.1 GB
  eng2      yes     np-a1f.6 (Client SDK)             in_progress  1.9 GB
  qa1       yes     np-a1f.4 (Data model)             in_qa        1.8 GB
  qa2       no      -                                 agent down        -
  sec       yes     -                                 idle         1.2 GB
  shipper   yes     -                                 idle         1.4 GB
```

### 5.5 Fix a Stuck Agent

```
$ initech restart qa2
Restarted qa2 in session 'newproject'.

$ initech restart qa2 --bead np-a1f.4
Restarted qa2 in session 'newproject'.
Dispatched: "[from initech] Restarted. Resume np-a1f.4."
```

### 5.6 Slim the Roster

Nelson is deep in implementation. The architect finished the system design two phases ago, security hasn't been needed since the threat model review, and the PM is idle between grooming cycles. Meanwhile, eng1, eng2, and qa1 are burning through memory with active Claude sessions, and Nelson has cobalt running in another tmux session. His machine is feeling it.

```
$ initech status

Session: newproject (running, 9 agents, 14.2 GB total)

  Role      Claude  Bead                              Status          Mem
  super     yes     -                                 -            1.3 GB
  pm        yes     -                                 idle         1.1 GB
  arch      yes     -                                 idle         1.4 GB
  eng1      yes     np-a1f.5 (API endpoints)          in_progress  2.1 GB
  eng2      yes     np-a1f.6 (Client SDK)             in_progress  1.9 GB
  qa1       yes     np-a1f.4 (Data model)             in_qa        1.8 GB
  qa2       no      -                                 agent down        -
  sec       yes     -                                 idle         1.2 GB
  shipper   yes     -                                 idle         1.4 GB

$ initech stop arch sec pm
Stopped arch in session 'newproject'.
Stopped sec in session 'newproject'.
Stopped pm in session 'newproject'.

$ initech status

Session: newproject (running, 6 agents, 3 stopped, 10.5 GB total)

  Role      Claude  Bead                              Status          Mem
  super     yes     -                                 -            1.3 GB
  pm        -       -                                 stopped           -
  arch      -       -                                 stopped           -
  eng1      yes     np-a1f.5 (API endpoints)          in_progress  2.1 GB
  eng2      yes     np-a1f.6 (Client SDK)             in_progress  1.9 GB
  qa1       yes     np-a1f.4 (Data model)             in_qa        1.8 GB
  qa2       no      -                                 agent down        -
  sec       -       -                                 stopped           -
  shipper   yes     -                                 idle         1.4 GB
```

Two weeks later, it's release time. Nelson brings back the shipper (already running) and needs security for a final review.

```
$ initech start sec
Started sec in session 'newproject'.

$ initech start sec --bead np-a1f.12
Started sec in session 'newproject'.
Dispatched: "[from initech] Security review for np-a1f.12."
```

The roster flexes with the work. Agents that aren't earning their memory get benched until they're needed.

### 5.7 End the Day

```
$ initech down
WARNING: eng1 has uncommitted changes in src/
WARNING: eng2 has uncommitted changes in src/
Use --force to stop anyway, or 'initech status' to review.

$ initech down --force
Session 'newproject' stopped.
```

---

## 6. Risks

### 6.1 Template Quality

The value of initech is proportional to the quality of the generated CLAUDE.md files. Bad templates produce bad agents. The templates encode institutional knowledge distilled from four projects' worth of iteration. If the templates are thin or generic, the tool saves time on directory creation but not on the hard part (getting agent behavior right).

**Mitigation:** Treat templates as first-class product. Review them with the same rigor as application code. Iterate templates based on real agent performance in real projects.

### 6.2 Config Schema Evolution

As initech matures, the config will grow. New fields break old configs if not handled carefully.

**Mitigation:** YAML naturally ignores unknown fields on read. Add a `version` field to the config for detecting major incompatibilities. Default values for all optional fields.

### 6.3 External Tool Dependencies

Initech depends on tmux, tmuxinator, bd, gn/ga/gp/gm, git, and claude. If any is missing or breaks, initech degrades.

**Mitigation:** Graceful degradation. Check for each tool before use. Print clear messages about what's missing and how to install it. Core scaffold (directories, CLAUDE.md files) works with only git.

### 6.4 Single-User Assumption

The design assumes one human driving the session. Multi-user or multi-machine scenarios are not addressed.

**Mitigation:** Not a risk for MVP. If needed later, the config-driven design (initech.yaml) provides a foundation for multi-machine configs.

---

## 7. Scope Boundaries

### 7.1 MVP Scope (Build This)

- `initech doctor` - prerequisite check with fix instructions
- `initech init` - interactive and config-driven project bootstrap
- `initech up` - start tmux session
- `initech down` - stop with safety warnings
- `initech status` - agent and bead status table
- `initech stop <role...>` - stop individual agents to free memory
- `initech start <role...>` - bring stopped agents back with optional bead dispatch
- `initech restart <role>` - kill and restart agent
- `initech standup` - morning standup from beads
- Role templates for all 11 well-known roles
- Tmuxinator generation (main + grid)
- Git submodule setup
- Beads initialization
- Homebrew distribution

### 7.2 Post-MVP (Build Later, If Needed)

- `initech dispatch` - formatted dispatch messages
- `initech patrol` - automated agent monitoring sweep
- `initech add-role` / `initech remove-role` - modify running project
- Custom role templates (plugin system)
- Remote agent support
- Metrics collection

### 7.3 Never Build

- Web UI
- Cloud hosting
- Multi-user auth
- Agent AI logic
- IDE plugins
