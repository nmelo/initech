package roles

// Fleet files the scaffold creates beside the charters (ini-vby5.13): where a
// rule's history, the fleet's reference material and its repeated
// procedures live, so the charters stay short and carry no history.

// FleetLessonsTemplate is the scaffolded fleet/lessons.md.
const FleetLessonsTemplate = `# Lessons

Hard-won lessons behind the rules. Read the matching section before claiming something is verified. Charters carry no history; it lives here.
`

// FleetReferenceTemplate is the scaffolded fleet/reference.md: commands,
// where to find the QA pairs, and the operator inbox guide (moved out of the
// root to keep it under its line cap). The pairs themselves stay in the root's QA
// pairs section, the copy that hiring keeps current.
const FleetReferenceTemplate = `# Fleet reference

Commands, the QA pairs and who owns which area.

## QA pairs

The table is in the root CLAUDE.md, QA pairs section; hiring or firing an engineer or QA rewrites it there.

## Who owns which area

(Fill in as areas settle: area, owning agent.)

## Commands

` + "```" + `bash
bd ready                                 # unblocked work
bd list                                  # all beads
bd show <id>                             # bead details and comments
bd update <id> --status <status>         # transition a bead
bd comments add <id> --author <you> '...'  # PLAN, DONE, verdicts
initech send <agent> '[from <you>] ...'  # message an agent
initech peek <agent>                     # read an agent's terminal
initech status                           # every agent, its activity and bead
initech patrol                           # peek at all agents at once
initech deliver <id>                     # move a bead one step and report
` + "```" + `
` + OperatorInboxGuide

// SkillsReadmeTemplate is the scaffolded skills/README.md.
const SkillsReadmeTemplate = `# Skills

Shared skills, one per repeated multi-step procedure, written and maintained by super; link them into each agent that uses them. Tell super when a step is wrong or missing.
`
