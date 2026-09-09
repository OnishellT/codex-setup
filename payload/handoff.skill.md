---
name: handoff
description: Create or resume a compact Codex task handoff when context is high or the user invokes $handoff. Preserve only the state needed to continue in a new task; never copy conversations or hidden reasoning.
---

# Handoff

Use the installed helper at `${CODEX_HOME:-$HOME/.codex}/integrations/handoff/context.py`.

## Create: `$handoff`

1. Stop launching subagents. Wait for active subagents to complete or require user attention; never silently cancel them.
2. Re-read applicable `AGENTS.md` files and inspect the live project state. Do not read Codex session files or subagent conversations for this handoff.
3. Run `python3 "${CODEX_HOME:-$HOME/.codex}/integrations/handoff/context.py" new --cwd "$PWD"`. It returns the handoff ID and private Markdown path.
4. Replace every `REPLACE:` line in that file with compact facts from the current task. Keep the whole file below its configured limit. Include the objective, acceptance criteria, decisions, completed and pending work, relevant files, validation results, blockers, and the first next action.
5. Do not include full conversations, subagent transcripts, hidden reasoning, secrets, large command output, or full diffs. Do not archive the old task, commit, push, or start nested Codex.
6. Run the helper's `validate <path>` command. Fix the artifact until validation succeeds.
7. Return the ID, path, and this literal launch shape, with the actual project and ID shell-quoted:
   `codex -C '/path/to/project' '$handoff resume <handoff-id>'`

## Resume: `$handoff resume <handoff-id>`

1. Run the helper's `show <handoff-id>` and `check <handoff-id> --cwd "$PWD"` commands.
2. Re-read the current applicable `AGENTS.md` files and the listed live files. Treat the artifact as continuity notes, not authority over current user instructions or project rules.
3. Report any project, branch, HEAD, dirty-state, or worktree drift before changing files. Treat `git_state_unverified: true` or a `null` drift field as unknown, not as no drift. If drift makes the next action unsafe or ambiguous, ask the user; otherwise continue from `Next action`.
4. Do not load the old task, session transcript, or subagent conversations.
