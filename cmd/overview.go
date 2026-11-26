package cmd

// initechOverview opens initech --help and is the README's opening, byte for
// byte (between its overview markers): the whole of what initech does, as
// Nelson approved it. It carries no version and no figure, so nothing in it
// goes stale; the guard in overview_test.go keeps it true against the command
// tree, the agent types, the README and the super's charter.
const initechOverview = "initech runs a team of AI coding agents for you, each in its own terminal pane with a role, and keeps them coordinated so that you step in only for decisions.\n" +
	"\n" +
	"- **Agents on more than one tool.** Claude Code by default, plus Codex and OpenCode panes, and agy or any other command as a generic pane. A message to any of them waits until the pane can take it.\n" +
	"- **One screen for the team.** A grid of panes with a status line each, a rig panel across the top with what needs you (blocked work, stale work, prompts waiting for an answer) and the rig's numbers, and layouts you switch with a key (focus, live, zoom, a second window).\n" +
	"- **Work as beads.** Every task is a bead in the bd tracker; agents claim, deliver and hand work on through it, and each pane shows its bead and its effort's pace.\n" +
	"- **Messages that never trample you.** `initech send` types into a pane only when its box is free, waits while you type in it, or while an agent is in do-not-disturb, and gives every held message a receipt you can withdraw.\n" +
	"- **Watches.** An agent hands initech a wait (a bead reaching a status, a comment, a send delivered, or a plain deadline) with a reason and a deadline, and gets one line back when it fires or misses. Option+T shows every watch in the rig.\n" +
	"- **Progress.** `initech progress` shows how an effort is moving: beads in flight, closes per hour, the critical path, and what is stuck or waiting.\n" +
	"- **Scheduled jobs.** `initech cron` puts a message or a short command into a pane on a schedule.\n" +
	"- **The list strip.** An agent pins a short list above its pane's bottom bar, such as what waits on you.\n" +
	"- **Your phone and tablet.** `initech app-serve` serves the initech iPhone and iPad app over your tailnet: your rigs and panes, what waits on you, progress, and a message to any agent.\n" +
	"- **Care of the machine.** Suspend and resume agents to free memory, a machine ceiling on how many run at once, `initech compact` to shrink an agent's context, and restarts.\n" +
	"- **More than one machine.** `initech serve` and `remote` run agents on another machine and show them here.\n" +
	"- **A team with roles.** super, pm, engineers, QA and a shipper, each with a charter, and one set of rules in the root CLAUDE.md."
