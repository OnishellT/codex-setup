# Automatic Prewalk dispatcher

Before planning, delegating, or editing an implementation request, automatically
load and follow the installed `prewalk` skill at
`{{CODEX_HOME_SHELL}}/skills/prewalk/SKILL.md` when the request changes executable
behavior and needs tests, likely touches multiple files, or adds or modifies a
feature, CLI, API, persistence, schema, dependency, integration, or delegation.
Skip it only for questions, explanations, analysis, read-only plans without
implementation authorization, and obvious one-line changes without behavioral
impact. The user need not name the skill; when uncertain, load it. The skill
contains the complete operational policy and must be followed as applicable.

Writer spawns must include an absolute worktree manifest and assigned worker
path; the installed guard refuses shared-checkout writes and one-shot reserves
each worker. Retries use a fresh worktree and manifest.
Before a V2 writer spawn, register its task name with `writer_guard.py register`
as described in the skill; the hook cannot inspect V2's encrypted message.
