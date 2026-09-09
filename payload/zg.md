# zg: optional local semantic search

Use `zvec_grep_search` only if a relevant local index exists and either the query is conceptual or the concept's location is unknown. Use `rg` for exact or exhaustive searches; verify all results in the live file.

All agents must explicitly set `freshness: "wait_for_fresh"` on every call; never rely on server defaults or background indexing. Discard `possibly_stale`, `timeout` or `error` results; continue with `rg`. Also use `rg` if zg is unavailable, fails or lacks a relevant index; never block the task.

Create, rebuild or delete indexes only on explicit user request. Never authorize remote indexes or services.
