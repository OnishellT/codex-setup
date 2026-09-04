# Automatic Prewalk

Prewalk is enabled for the primary orchestrator. Apply it automatically to
nontrivial implementation tasks; the user does not need to request it.
For questions, analysis-only requests, and tiny or tightly coupled edits without
a useful bounded delegation, work directly. A request
for a plan does not authorize implementation.

Before changing code, understand the relevant flow and make a short actionable
plan. When implementation is a bounded assignment and the primary can do useful
independent validation or integration preparation, delegate it to the native
`prewalk_executor` agent with the
goal, relevant evidence, owned files, constraints, and acceptance checks. Send
only the context needed for that assignment. Let the role select its model;
do not override it at spawn or start another Codex process.

Keep architecture decisions and final integration in the primary agent; the
executor may own the implementation's critical path. Do not duplicate its work.
Use a second agent only for useful independent exploration or review. Preserve
the configured concurrency limit and one writer per file. Delegated agents
must execute their assignment, not start another Prewalk cycle.

Before final verification and your final response, use native agent wait/status
tools to confirm every delegated agent has reached its terminal completed state.
A report, message, successful test, or wait wake-up is not proof of completion.
If an agent is still running, keep waiting; do not finish the parent turn or
cancel that agent merely because its files or report are already available.
Handle failed or interrupted agents explicitly instead of declaring success.
After completion, avoid unnecessary follow-ups that would restart the agent.

When an executor is blocked, resolve the specific issue or revise its assignment.
Do not silently broaden scope, switch providers, or change permissions. On
resume or interruption, inspect existing progress and changes before reassigning
work. Before completion, inspect the diff and validation evidence, run any
necessary final checks, and report the outcome. If native delegation is
unavailable, explain that limitation and continue directly within the same scope.
