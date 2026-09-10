# Usage guide

English · [Español](usage.md) · [Back to README](../README.md)

## Installation and automation

Examples use `./install.sh` from a checkout. With a downloaded binary, replace
it with `./codex-setup-linux-amd64` or its arm64 variant.
The script uses `bin/`; if the binary is missing, it tries to build with Go 1.26+.
After changing code or payload, rebuild: the script does not detect stale binaries.

```sh
./install.sh --list
./install.sh --modules base,profiles,agents,prewalk --dry-run
./install.sh --modules base,profiles,agents,prewalk --yes --install-deps
./install.sh --modules base,profiles,agents,prewalk --check
```

`--yes` authorizes configuration changes, not dependencies. `--install-deps`
authorizes dependencies separately and requires `--yes` and `--modules`;
it cannot be combined with `--dry-run` or `--check`. The TUI provides equivalent
confirmations. Configuration is not applied without a verifiable ChatGPT account.
The account and catalog are queried through the local app-server, without
requesting model generations. A configured external provider or catalog blocks
validation: it is not silently migrated. The installer interface is in Spanish.

## Models and profiles

In the TUI, press **m**: ↑/↓ selects a role, ←/→ changes the model, Tab changes
effort, **d** restores presets, and Enter returns to modules. **c** queries the
account again. The preview shows the effective choices before writing.

Project presets are Astra/medium for the main agent, Sol/xhigh for the reviewer,
Spark/medium for execution/exploration, and Luna/medium for fallback roles.
These are setup preferences, not availability guarantees. If an ordinary role's
preset is unavailable, the installer proposes a catalog alternative. Fallback
roles require Luna or an explicit available choice. Incompatible explicit choices
are rejected. Catalog access does not guarantee remaining quota.

`--models roles.json` accepts a partial object using models from your account:

```json
{"prewalk_executor":{"model":"gpt-5.6-sol","effort":"medium"}}
```

Omitted roles follow default resolution. During execution, the configured fallback
is used only after explicit Spark quota exhaustion, not for failed tests, network
errors, or poor results. It does not change the main agent or consume a reset.
The `personal` and `work` profiles inherit shared configuration; they are not
separate accounts. No external providers or API credentials are installed.

## Destinations and recovery

`CODEX_HOME` is respected. `--codex-home` selects an explicit destination;
`--home` uses the specified home directory and ignores the inherited `CODEX_HOME`
unless explicitly overridden. An isolated destination needs its own login;
do not copy credentials for testing.

Destinations include the Codex directory, `~/.local/share/codex-panel`, launchers
in `~/.local/bin`, and marked blocks in shell files. Unrelated values are preserved,
but values managed by the selected module are updated. TOML merging may reformat
files or remove comments.

Originals and `manifest.json` are saved with private permissions in
`~/.local/state/codex-setup/backups/<date-id>/`. Reinstalling without changes
creates no backups or duplicate blocks/hooks. There is no automatic uninstall.

To restore, stop the panel first. The manifest identifies each destination with
`path` and its original with `file`; `existed=false` indicates a new file.
Restore only those destinations, without deleting shared directories. Symlinks
and changes made after preview are rejected; a failed write triggers a rollback
attempt. A power outage or `kill -9` may require manual recovery.

## Panel

Requires Linux with `/proc`, an interactive terminal, Python 3.11+ with curses,
sqlite3 and tomllib, real tmux ≥3.3, and ncurses `tic`. A 120-column terminal
is recommended. Packages are offered with separate consent. For a custom path:
`CODEX_PANEL_TMUX=/path/to/tmux ./install.sh`.

Open a new terminal after installation:

```sh
codex --profile personal
codex --profile work
codex resume --last
CODEX_PANEL_DISABLE=1 codex     # bypass the panel
```

Bash/Zsh integration preserves arguments and does not replace the Codex executable.
Service commands (`exec`, `login`, `app-server`, `--help`, etc.) and non-TTY runs
go directly to the CLI. No permissions are added; if you pass `--yolo`, it retains
its meaning of bypassing sandboxing and approvals. If `.zshrc` is a managed symlink,
the installer uses `.zshenv` without changing the link. Other shells can use
`codex-panel`.

To disable the integration, remove only the `codex-setup:launcher` block from
the file shown in the preview, or restore the backup. Do not copy launchers
between machines: they contain local paths, so install again on each machine.

Ctrl-S hides/shows the panel. Arrow keys and Enter open an agent; Enter closes
the floating view. Quota is queried when opening the panel, not during installation.
To disable this query: `CODEX_PANEL_QUOTA_OFFLINE=1 codex-panel -p personal`.
Per-agent estimates are not official figures. More options are in the
[panel guide (Spanish)](../payload/panel/README.md).

## Prewalk, Ponytail, and handoff

Prewalk loads its skill for nontrivial implementations: work contracts, private
worktrees, Qlty, and independent review. The [canonical policy](../payload/skills/prewalk/SKILL.md)
defines the workflow. Hooks need trust and are not a security sandbox.
Instructions alone do not guarantee a correct result.

Customize `$CODEX_HOME/integrations/prewalk/settings.json` and roles, not managed
scripts. `worktrees`, `max_workers`, and `quality` are setup settings.
Permission defaults are added only when no prior choices exist; `--yolo` is not
enabled. Qlty and its analyzers require network access to download.
To disable Prewalk, remove only the `codex-setup:prewalk` block from
`developer_instructions` and disable its entry in `skills.config`.
Preserve other instructions. Reinstalling enables it again.

Ponytail installs six skills and its hooks. Use `$ponytail lite`, `$ponytail full`,
`$ponytail ultra`, or `stop ponytail`. See its [guide](../payload/integrations/ponytail/README.md).

`context-handoff` alerts on active context usage and provides `$handoff`.
Handoffs are private, bounded Markdown files in `$CODEX_HOME/handoffs`, without
conversations or hidden reasoning. Settings live in
`$CODEX_HOME/integrations/handoff/settings.json`.
The [skill](../payload/handoff.skill.md) defines creation, validation, and resumption;
it does not push or open another task on its own.

## Optional zg

`./install.sh --modules zg --yes --install-deps` installs private Node and zg,
verifies hashes and pinned dependencies, and configures MCP with absolute paths.
It does not use global npm, download models, start the server, or create indexes.
Do not run `zg install`, which manages configuration outside this setup.

Only `zvec_grep_search` is exposed, with mandatory `freshness: "wait_for_fresh"`.
Use it with a relevant local index for conceptual queries; use `rg` for exact
searches, missing indexes, or errors. Discard `possibly_stale`, `timeout`, and
`error` results. Creating indexes requires separate authorization.
Runtime and patch rollback: [zg guide (Spanish)](../payload/integrations/zg/README.md).

## Verification and limits

Run `make test check` and `make release` before distributing changes.
Tests use temporary destinations and simulated accounts. The panel suite runs
in a temporary installation with its own tmux servers. Some optional tests are
skipped when dependencies or local fixtures are unavailable.

Clean amd64 installations on Ubuntu 24.04 and Debian 12 with a simulated account,
and the installed panel, have been tested. arm64 builds are produced, but the
actual arm64 runtime and end-to-end Fedora/Arch workflow have not been verified.
Hook trust and full human interaction with the TUI require manual validation.
Codex internal formats may change: revalidate new versions.

## Adding modules

The catalog is [`payload/modules.json`](../payload/modules.json). Add an ID,
name, description, and operations, place resources under `payload/`, and rebuild.
Reuse operations (`copy`, `tree`, `merge`, `append`, etc.) and tests in
[`internal/installer`](../internal/installer). `depends` declares dependencies;
`platforms` restricts systems. Destinations are relative to allowed roots;
path escapes with `..` and arbitrary manifest commands are not accepted.
There is no additional plugin framework.

## Provenance

Ponytail retains its [MIT license](../payload/integrations/ponytail/upstream/LICENSE)
and [pinned provenance](../payload/integrations/ponytail/upstream/PROVENANCE.json).
Go, Bubble Tea, and other dependencies retain their own licenses;
distribution texts are in [Third-party notices](THIRD_PARTY_NOTICES.md).
Qlty is downloaded from its upstream project; review its BSL/Fair Source terms
before offering it as a service to third parties. Integration references remain
alongside their code. Palette author attributions are preserved.

No overall license has been established for the project's original code.
Publishing the repository does not add a license to its components.
