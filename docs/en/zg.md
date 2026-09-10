# Optional zg MCP

English · [Español](../../payload/integrations/zg/README.md) · [Usage guide](../usage.en.md) · [README](../../README.md)

Select zg in the menu or run:

```sh
./install.sh --modules zg --yes --install-deps
./install.sh --modules zg --check
```

The installer prepares a private runtime under `CODEX_HOME/integrations/zg`:
Node **22.23.2** in `node-v22.23.2` and **@zvec/zvec-grep@0.2.1** in
`packages-v0.2.1`. It verifies the Node archive and main package hashes, uses
private npm with `--ignore-scripts` and no global configuration or installation,
and verifies native dependencies. PR 86 is applied only to its private staging
package; the patch/backup is checked before exposing the directory.

It does not modify global packages, start the server during installation,
download models, or create, rebuild, or delete indexes. An invalid existing
private directory is preserved and installation fails with a diagnostic; it is
not silently replaced. Failures can leave Node installed; retrying reuses that
runtime after validation.

The MCP entry uses absolute paths to private Node and the entrypoint, with
`server --stdio --mcp-toolset agent`. It is enabled for work/personal, with
`required = false`, only `zvec_grep_search`, `auto` approval, a 30-second startup
timeout, and a 120-second tool timeout. The server starts when Codex loads it,
not during the installer's check. Native approvals still apply; a rejected call
falls back to `rg` without granting permissions globally.

## Linux patch

The [PR 86](https://github.com/zvec-ai/zvec-grep/pull/86) helper supports only
version 0.2.1 and known hashes. It retains one original copy,
`watch-manager.js.codex-setup-pr86.original`, and rejects unknown files or backups.
It does not fix issue #92.

For a manual check, always use the private package root:

```sh
zg_root="${CODEX_HOME:-$HOME/.codex}/integrations/zg"
zg_node="$zg_root/node-v22.23.2/bin/node"
zg_package="$zg_root/packages-v0.2.1/node_modules/@zvec/zvec-grep"
"$zg_node" "$zg_root/pr86-watch-manager.mjs" check --package-root "$zg_package"
```

For an explicit rollback, stop its daemon first, then use
`restore --package-root "$zg_package"` with the same helper. Stopping the daemon
affects other sessions; already running processes retain the old code.
After restoring the original, `--check` rejects the installation until the known
patch is reapplied and verified. Do not force the helper onto another version.

Historical observation on 2026-09-04 with Linux, Node 24.19.0, and zg 0.2.1:
with PR 86, ignored files added no watches; after 31 minutes without queries,
watches disappeared and an edit was not automatically indexed (#92).
`zg query --refresh wait` recovered the result. This observation does not
guarantee the MCP's current state.

## Indexes and usage

Use zg only when a relevant index exists and the query is conceptual or the
location is unknown. For exact/exhaustive matches, missing indexes, or failures,
use `rg` and verify the live file. Do not authorize remote indexes or create,
rebuild, or delete indexes without an explicit request.

The orchestrator and **every subagent** must send `freshness: "wait_for_fresh"`
on every `zvec_grep_search` call. This is not a server default. Discard
`possibly_stale`, `timeout`, and `error`; fall back to `rg`. Background indexing
does not guarantee freshness.

Only if the user requests an index, scope it to the chosen repository, never
HOME. Using the variables above:

```sh
"$zg_node" "$zg_package/dist/cli/index.js" index /path/to/repo --embedding local/potion-code-16m-v2 --device cpu
```

This command may download the model and create data; it is not part of onboarding.
Do not run `zg install`, which manages configuration and approvals outside this
setup. No secrets or loopback-service tokens are copied.

To disable MCP in future sessions, set `enabled = false` in
`[mcp_servers.zvec_grep]`. To explicitly stop the shared daemon:

```sh
"$zg_node" "$zg_package/dist/cli/index.js" server off
```

This affects sessions using it, but does not delete indexes. Reinstalling the
module enables it again after checking prerequisites.
