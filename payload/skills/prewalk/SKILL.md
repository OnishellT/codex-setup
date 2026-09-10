---
name: prewalk
description: >
  Automatically load and follow the full Prewalk policy for nontrivial
  implementation requests that change executable behavior and need tests,
  likely touch multiple files, or add or modify features, CLI, APIs,
  persistence, schemas, dependencies, integrations, or delegation. Skip it for
  questions, explanations, analysis, read-only plans without implementation
  authorization, and obvious one-line changes without behavioral impact.
---

# Automatic Prewalk

Explicit user instructions take precedence when they conflict with this policy.

Prewalk is enabled for the primary orchestrator. Apply it automatically to
nontrivial implementation tasks; the user does not need to request it.
For questions, analysis-only requests, and tiny or tightly coupled edits without
a useful bounded delegation, work directly. A request
for a plan does not authorize implementation.

When implementing a new project, locate applicable project rules first. Before
writing project files or delegating writers, run
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/worktrees.py prepare --repo <absolute-project>`.
This preparation is automatic for implementation, never for questions, read-only
reviews or planning-only requests. Read its result: Git may already belong to a
parent repository; reuse the reported root, never create a nested repository.
For an empty new project it initializes Git, writes a minimal .gitignore and
commits only that file as the baseline. It uses the configured Git identity;
never invent an identity, change global Git configuration, add remotes or push.
Do this before scaffolding, even if only one worker is needed. Existing files,
secrets, staged changes and custom .gitignore files must never be swept into an
automatic initial commit. If preparation refuses a nonempty directory, inspect
the contents and request authorization for a specific initial baseline; do not
use `git add .` or bypass the helper with an indiscriminate commit. For missing
identity or permissions, report the exact blocker and follow the approval/fallback
rules below. Preparation is not proof that the checkout is ready for worktrees;
`create` still validates the clean committed baseline and all other prerequisites.

When `quality` is true or absent in the Prewalk settings, configure Qlty immediately
after Git preparation and before creating worktrees. Inspect the current project,
its language manifests and existing lint/type/test configuration, then run
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/quality.py setup --repo <absolute-repo>`.
The helper preserves and validates an existing `.qlty/qlty.toml`; otherwise it
uses Qlty's project detection, validates the result and commits only the generated
`.qlty` configuration so every worktree starts from the same clean quality
baseline. Review that generated configuration against the actual project and
adjust it explicitly if detection missed an existing project tool. Never enable
AI fixes or weaken project rules. For a truly empty repository the initial config
can only provide Qlty's language-independent maintainability checks; repeat the
setup review once language manifests exist. If Qlty or its required network/cache
is unavailable, request the normal native approval for the exact helper command
if needed; never broaden permissions. Report quality setup as blocked rather than
claiming deterministic analysis. The helper disables Qlty telemetry and upgrade
checks for every run.

Before changing code, research the relevant flow, applicable AGENTS.md and
AGENTS.override.md, existing patterns, interfaces and tests. Make short worker
contracts: objective, acceptance criteria, interface/behavior, evidence to reuse,
owned files and exclusions, dependencies, checks, and assigned worktree/base SHA.
Specify constraints, not line-by-line code. Keep architecture and final integration
in the primary; delegate coding to native `prewalk_executor` workers. Do not
duplicate their implementation. Resolve ambiguities or revise a bad contract
instead of letting a worker silently redesign the feature.

For nontrivial read-only research with at least two independent lanes, start at
least two explorers concurrently and leave raw searches, logs and documentation
inspection in their threads. The primary should gather only enough initial facts
to define narrow contracts, then synthesize their concise evidence. Use one
explorer only when another lane would be dependent, trivial, unsafe, unauthorized
or no slot is available, and report that reason. Do not split one tightly coupled
question merely to reach an agent count.

Use the configured `explorer` for normal read-only discovery and bulk lookups.
Reserve `fallback_explorer` for exhausted Spark quota as described below;
choose `critical_explorer` for ambiguous, security-sensitive
or high-impact investigation. For implementation, use `prewalk_executor`; retry
with `fallback_executor` only after explicit Spark quota exhaustion as described
below. A failed implementation, test failure or
weak result must be corrected or replanned, not silently retried on a different
model. These are explicit routing roles because Codex has no configured model
fallback list.

When Spark quota is explicitly exhausted, retry the unfinished assignment once
with the configured fallback role (preset: `gpt-5.6-luna` at `medium`) after
confirming the worker is terminal. Preserve
partial work, create a fresh Prewalk worktree and manifest, do not use usage
resets, and reuse that role for remaining fallback assignments in that run. Use Spark
again on a new run; never switch for tests, poor results, network errors, or
generic rate limits. If the configured fallback fails, stop; request reload/new task for cached
fallback roles rather than bypassing their fixed model.

Read `{{CODEX_HOME_SHELL}}/integrations/prewalk/settings.json` before planning
parallel writers. These are setup settings, not native Codex config keys:
`worktrees` enables worktree execution; `max_workers` caps workers (1 through 4),
also respecting Codex's concurrency limit. Choose the smallest count that covers
independent contracts; do not fill slots for tightly coupled work. With worktrees enabled and at least two
independent implementation contracts, use
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/worktrees.py create --repo <absolute-repo> --workers <count> --max-workers <configured-limit>`.
By default sessions live in the private `$CODEX_HOME/worktrees/prewalk` root.
Read its JSON manifest and assign each worker its own absolute path and branch.
The helper requires a clean committed baseline. Never stash, discard, commit
unrelated user changes, or weaken permissions to satisfy it. If prerequisites
fail, use the existing approval flow for the specific required Git operation
when available, never a blanket permission change. If unavailable or denied,
explain the reason and stop rather than writing in the shared checkout. Even a
single writer must use one isolated worktree and manifest.

For every writer spawn, include the absolute manifest path and the assigned
worker worktree path in the contract/prompt. The installed writer guard refuses
shared-checkout writes and reserves each worker once; if a worker fails or the
model changes, create a fresh worktree and manifest rather than reusing it.
Include a standalone JSON line inside the message (not extra spawn arguments)
so paths containing spaces remain unambiguous:
`{"manifest":"/absolute/session/manifest.json","worktree":"/absolute/session/worker-1"}`.
Before each spawn, register its exact native `task_name` from the planner's shell:
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/writer_guard.py register --manifest <absolute-manifest> --worker worker-1 --task <task_name>`.
For a fallback writer add `--role fallback_executor`. The helper uses the native
`CODEX_THREAD_ID` of the parent. Do not invent or override it. V2 encrypts spawn
messages, so this registration lets the hook check the assignment without reading
the conversation. Missing or mismatched registration is a blocker, not a reason
to disable the hook.

Send workers only their contracts, necessary project rules and source references;
use `fork_turns="none"` (legacy API: `fork_context=false`). Select the role and
let its configured native model apply; do not override it at spawn or start
another Codex process. Use at most four agents and one delegation level. Independent
workers may run together; dependent contracts wait for required results. Finish
planning/exploration agents before occupying all available slots with writers.
Keep one writer per owned file even across worktrees when possible. Agree shared
interfaces first. Every tool call must use the assigned absolute worktree path:
a previous `cd` does not relocate later tools. Account for local untracked/ignored
project instructions, and isolate test ports, databases and mutable resources;
worktrees only separate files. Workers must not start another Prewalk cycle.

Every implementation contract must include the project's native tests plus the
Qlty scan when `quality` is enabled. From the assigned absolute worktree run
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/quality.py scan --repo <absolute-worktree> --upstream <base-sha> --output <absolute-report-outside-repo.json>`.
Commit the approved worker changes before this scan; verify the worktree is
clean afterward and treat a missing, failed, or malformed report as blocked.
Do not use `qlty fmt`, `--fix`, `--ai` or `--unsafe`. Fix material findings in the
worker and rerun the scan; a missing, failed or malformed scan is not a pass.

After workers reach terminal completed state, inspect delivered commits, file
ownership and checks; reject changes outside the contract. Use
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/worktrees.py integrate --manifest <manifest>`
to combine completed worker commits sequentially in the integration worktree.
Before the first cherry-pick the helper rejects any file path changed by more than
one worker, so no worker silently wins. Replan or integrate that shared path
serially; never bypass the overlap check.
Conflicts or failures preserve all work; resolve them explicitly, never force an
automatic winner. Run aggregate tests there, not just per-worker tests. Corrections
after integration must target integrated code/base, not stale worker branches.
Run the same Qlty scan in the integration worktree against the manifest base and
give its normalized JSON report to the reviewer together with native test results.

For every nontrivial implementation change, before final verification and the
final response, obtain an adversarial review from the native
`engineering_reviewer` role when it is available. This applies even when the
primary implements directly because delegation was not useful. Do not use the
executor as reviewer. First wait for all writers to reach completed state and
for the diff to be stable. Always spawn a NEW reviewer with `fork_turns="none"`
(legacy API: `fork_context=false`), never with inherited turns. Send only the
original request and accepted clarifications, plan/acceptance criteria, applicable
project rules, source/test locations, exact diff base and deterministic Qlty/native
test reports. Do not send implementer
conversations, rationales, summaries or transcripts. The reviewer inspects actual
code and checks both request-versus-plan and request/plan-versus-code. It is
read-only and must not repair changes or query session history. Its PreToolUse
hook blocks detected conversation access; this is a practical guard, not an OS
security boundary. Ensure Prewalk hooks are enabled and trusted via Codex `/hooks`
before claiming a protected review. The hook uses native `agent_type` metadata to
target the reviewer; if that metadata is unavailable, protection is unverified. Never
bypass hook trust or claim the guard ran if Codex skipped it.

If the reviewer role, hooks or native review tools are unavailable, say that a
protected review was not performed; never invent one. Address material findings
through the executor or primary; adjudicate disputed findings with evidence, then
obtain a fresh-context re-review of
the affected changes and their interactions, including any edits after review.
Do not treat an incomplete review as a pass. Keep at most four
agents and one delegation level, release completed agents without cancelling a
running one, and avoid infinite cycles over stylistic preferences. If critical
findings remain unresolved, report them and do not claim the work is complete.

Before final verification and your final response, use native agent wait/status
tools to confirm every delegated agent has reached its terminal completed state.
A report, message, successful test, or wait wake-up is not proof of completion.
If an agent is still running, keep waiting; do not finish the parent turn or
cancel that agent merely because its files or report are already available.
Handle failed or interrupted agents explicitly instead of declaring success.
After completion, avoid unnecessary follow-ups that would restart the agent.

After review and aggregate checks, use
`python3 {{CODEX_HOME_SHELL}}/integrations/prewalk/worktrees.py finish --manifest <manifest>`
to fast-forward the original checkout only if its branch/base and clean state
are unchanged. Do not push. Preserve session worktrees and branches for recovery;
report their location. No automatic destructive cleanup. On interruption or
resume, inspect the manifest and existing progress instead of creating duplicates.

When an executor is blocked, resolve the specific issue or revise its assignment.
Do not silently broaden scope, change models, or change permissions. On
resume or interruption, inspect existing progress and changes before reassigning
work. Before completion, inspect the diff and validation evidence, run any
necessary final checks, and report the outcome. If native delegation is
unavailable, explain that limitation and continue directly within the same scope.
