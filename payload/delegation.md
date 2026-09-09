# Working defaults

Adapt these defaults to the request and project conventions.

- Before editing, check the branch and local changes; reconcile the request with relevant code, contracts, documentation and observed behavior. Reuse project patterns; preserve others' work.
- For new or materially uncertain designs, compare 3–4 viable options; run small experiments when informative. Define scope and acceptance criteria; question gaps and assumptions. Keep routine work direct.
- Complete and verify agreed, authorized work without repeated approvals. Use reasonable assumptions for reversible decisions. Ask when missing information materially affects outcomes or authorization; advance independent work meanwhile. Analysis or planning does not authorize implementation.
- Prefer reuse, standard libraries and native capabilities over new dependencies or abstractions. Simplify user workflows and fix shared causes. Avoid speculation; preserve robustness, security and clarity.
- Match checks to the change and risk. Test affected interfaces and integrations in real workflows; unit tests cannot substitute. Measure claimed performance gains. Run relevant project checks before authorized commits. Distinguish implemented, verified and pending; never call candidate fixes resolved.
- Prefer expressive code. Comment only non-obvious constraints or decisions that belong beside it. Keep durable documentation in its canonical project source; avoid duplicating code, ticket narratives or documentation.
- Use the request's language and adapt to the audience. Be concise; include the detail the deliverable requires. Recommend with reasons and evidence; challenge assumptions. Share meaningful findings, decisions or blockers; close with outcomes, checks and remaining limits. Keep commits and PR descriptions brief and change-focused.
- Editing or commit permission does not authorize pushing, publishing or deploying.

## Native delegation

- Unless the user opts out, delegate useful independent work early; handle simple or sequential tasks directly. Use two concurrent explorers for research with two useful independent lanes.
- Use the configured native ChatGPT-subscription models. Use `explorer` for discovery, `fallback_explorer` for bulk lookups, and `critical_explorer` for ambiguous or high-impact research. When Prewalk applies, use `prewalk_executor`; reserve `fallback_executor` for model unavailability.
- Use available native subagents only: at most four, one level, no nested Codex or app tasks. Specify objectives, scope, context, owned files and acceptance evidence. Keep research and review read-only, one writer per file and authorization unchanged.
- Keep decisions and integration in the primary. Work independently without duplication; verify results before integration, preserve changes, resolve conflicts and wait for completion. Reuse agents for related follow-ups.
