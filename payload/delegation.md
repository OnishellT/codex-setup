# Shared personal/work delegation policy

## Eager, bounded native subagents

These instructions request proactive native Codex subagent delegation. Apply
them to ordinary tasks without waiting for the user to explicitly ask for agents.
Explicit user instructions not to delegate take precedence.

For the primary agent:

- At the start of a nontrivial task, identify useful independent work. When at
  least one bounded lane can run alongside your own work, delegate it early,
  before doing that lane yourself. Good lanes include repository exploration,
  an independent review, investigating a separate failure, or running checks.
- Use one or two subagents only when they add useful parallel work or independent
  verification. Do simple answers, tiny edits, and tightly sequential tasks
  directly; do not manufacture subtasks just to spawn agents.
- Keep decisions and final integration yourself. When Prewalk is enabled in
  Codex configuration, delegate the implementation's critical path to its
  executor. Continue useful work while agents run; do not duplicate their work.
- Use native subagent tools, not new user-visible app tasks or nested `codex`
  processes. Keep at most two subagents open concurrently and only one level of
  delegation. Tell every delegated agent not to spawn further agents.
- Let the configured Terra/medium defaults apply. Do not override the model or
  reasoning effort unless the task requires it, and explain a necessary override.
- Give each agent a narrow objective, relevant paths/context, clear ownership,
  constraints, and a completion condition. Request a concise result with evidence
  (file/line references, checks, findings, and blockers), not a full transcript.
  Prefer targeted context over copying the entire conversation.
- Default investigation and review agents to read-only work. For implementation,
  keep one writer per file; parallel writers must have explicitly disjoint file
  ownership or separate worktrees. Never revert another agent's or user's work.
- Delegation does not broaden authorization: an analysis-only request stays
  read-only, and agents inherit the task's approval and safety boundaries.
- Check agent results, resolve conflicts, and run appropriate final validation.
  Reuse an existing agent for a closely related follow-up instead of spawning a
  replacement unnecessarily. Close agents when their work is finished.
- Briefly tell the user what is being delegated and why. Report material results
  and validation in the final answer without exposing internal transcripts.

For a delegated agent:

- Complete only the assigned scope. Do not spawn subagents or nested Codex runs.
- Respect file ownership and concurrent changes. Return concise evidence and
  blockers to the primary agent; leave final integration and user communication
  to the primary agent.
