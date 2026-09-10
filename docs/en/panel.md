# Codex with a subagent sidebar

English · [Español](../../payload/panel/README.md) · [Usage guide](../usage.en.md) · [README](../../README.md)

## Usage

From any repository, in a regular terminal:

```sh
codex --yolo
codex --profile personal
codex --profile work
# You can also open the panel explicitly:
codex-panel --profile personal
codex-panel --profile work
```

The installer integrates `codex` through a Bash/Zsh function without replacing
the original executable. Open a new terminal after installation, or reload the
file shown in the preview. If Nix manages `.zshrc`, the hook goes in `.zshenv`
and applies only to interactive shells. To bypass the panel:
`CODEX_PANEL_DISABLE=1 codex` or `command codex`.

The integration preserves argv and the default profile: `codex --yolo` does not
force `personal`. `--yolo` retains its meaning of bypassing sandboxing and approvals.
`exec`, services, help, non-TTY commands, and remote sessions go directly to the
CLI. Interactive `codex resume`/`codex fork` can also use the panel.

You can specify a project:

```sh
codex-panel --profile personal --cwd ~/projects/tracer
```

Left: the original Codex CLI. Right: interactive subagent cards. The launcher
does not replace `codex`, change models, permissions, or profiles, or alter the
`tmux` on your PATH. If Codex asks whether you trust the project, answer in the
left pane; until then, the monitor waits for the session to be created.

## Controls

- In the Codex editor, **Enter** sends the message and **Shift+Enter** inserts a newline.
- Click a pane to focus it, or press `Ctrl-b` followed by an arrow key.
- **Ctrl-S**, without a prefix, hides/shows the sidebar from Codex or the monitor.
  The monitor moves to a parked window; it is not terminated or restarted.
  Showing it focuses it for navigation. Ctrl-S is unchanged outside these two panes.
- In the monitor, up/down arrows or `j`/`k` select an agent. **Enter** opens its
  activity in a live-updating tmux floating view.
- In the floating view, arrows scroll, `End` resumes following, and **only Enter
  closes the view**. `Esc` and `q` are ignored there. It does not send messages to
  the agent. The 25 ms delay for distinguishing key sequences affects only the viewer.
- In the monitor, `q`/Esc also hide it; they do not remove it.
- `Ctrl-b`, then `z`: zoom/restore the focused pane.
- `Ctrl-b`, then `d`: detach the view without stopping Codex.
- When Codex exits normally or through Ctrl-C, the supervisor records its status
  and closes this isolated session, including the monitor. A second Ctrl-C is not
  needed. If Codex handles Ctrl-C and continues, the session stays intact.
  Detaching with `Ctrl-b`, then `d`, preserves the session for reattachment.
- Use `/agent` inside Codex to control or inspect a subagent. The monitor does not control it.

To reattach:

```sh
tmux -L NAME attach -t NAME
```

Additional Codex options go after `--`:

```sh
codex-panel -p personal -- --sandbox read-only
codex-panel -p work -- resume SESSION_ID
```

Detaching prints the reattachment command with the exact tmux path and name.
Use that path if the `tmux` on your PATH is a shim.

`--width 48` sets the maximum sidebar width; it shrinks in narrow terminals.
A terminal of at least 120 columns is recommended. Launch from a terminal outside
another tmux session to avoid nested key prefixes.

## Cards and metrics

Each card shows nickname/task, model/effort, `Running` or `Done` (with separate
states for errors, interruptions, or missing data), cumulative turn duration,
and tokens. Cards no longer show communication arrows; the original log remains
available in the floating view. Logs can lag behind model activity.

The main view does not print narrative paragraphs about each agent. The floating
view shows original public logs: tool calls and inputs, commands, outputs, and
agent messages, separated by event/time and call ID. It does not translate,
summarize, or replace logs with monitor-generated descriptions. Newlines and
indentation are preserved, and lines wrap without being cut off. `Home` goes to
the beginning, `End` resumes following, and `PgUp/PgDown` scroll. Messages retain
the agent's original language. Output already truncated by Codex cannot be
reconstructed and is shown as recorded. Visible history is limited to 8 MiB of
text/indexes and 4096 events per agent. Reaching the limit displays an explicit
`[OMITTED: …]` notice; original JSONL files are unchanged. The monitor does not
generate model-based summaries.

**Tokens:** reported input + output. Cached input is already included in input,
and reasoning tokens in output; neither is counted twice. Inherited parent
history is ignored, and usage snapshots and paths are deduplicated.
Cards show full counts without abbreviations. In the floating view, `Token share`
means agent tokens / tokens for Main and all direct subagents. It is hidden if
any required counter is missing.

**Est. quota share ~X% of session:** an approximate allocation of session usage,
weighted by public Codex credit rates. It is neither a percentage of the whole
account nor an official measurement. The denominator includes Main and all agents.

```text
weight = (input - cached) × input_rate
       + cached × cached_rate + output × output_rate
estimated share = 100 × agent_weight / sum_of_session_weights
```

The table uses credit rates verified on 2026-09-03, not API prices.
Sol: 100/10/500; Terra: 50/5/300; Luna: 5/0.5/30 credits per million tokens,
for input/cached/output respectively. The module also includes 5.5/5.4/5.4-mini.
If speed is absent from the log, Standard is assumed and disclosed in the floating
view; explicit Fast uses the published multiplier. Reasoning is not added again
because it is included in output. Missing counters, model/tier changes without a
per-model breakdown, or unknown rates produce `—`, rather than silently excluding
that member from the denominator. The table requires revalidation after
2026-11-21; rates are not downloaded automatically.

**ACCOUNT QUOTA:** replaces the old notice. Bars show percentage **used** for the
account's primary/secondary windows, time until reset, and the age of the last
reading. Every 30 seconds, without blocking the keyboard, the monitor queries
`account/rateLimits/read` through the official app-server over stdio. It opens no
conversation and calls no model. Account quota includes other sessions/devices,
so it is not multiplied by this session's share. Errors retain the last value
marked `STALE`; unknown or expired windows are not converted to 0%.
It does not buy credits or consume resets.

The display refreshes every second. `Running` means a recorded turn start without
a recorded end. Output appears when Codex writes it to JSONL; if a tool records
only its final result, there is no intermediate output to display. Internal
reasoning, system instructions, and encrypted bodies are not shown. Terminal
controls are removed and recognizable credentials replaced with `[REDACTED]`,
without hiding labels such as `Token share`. Secret detection is best-effort:
treat the floating view as private project information.

The monitor consumes no model tokens. It incrementally reads Codex JSONL and
queries its SQLite index with `mode=ro`, without resuming conversations. Only the
account meter makes authenticated network queries through Codex, using the same
account CODEX_HOME shared by personal/work; it neither reads nor prints credentials
directly. `app-server` does not support `--profile` in this version. Floating
views and `--once` do not query the account. To disable these queries:
`CODEX_PANEL_QUOTA_OFFLINE=1 codex-panel -p personal`.
Identity comes from logs opened by the process that launched that window, not
from choosing the latest conversation in the directory. Each window stays
separate. Subagents are filtered by their exact parent ID; the current setup
allows one level. `/new` switches the monitor to the newly detected root.

If Codex exits without recording an end, an unconfirmed state is shown and
observed time is frozen. Absence of events is not assumed to mean a blocked agent.

## Language and colors

Codex CLI 0.153.0 uses an English UI; the panel also uses English, while agent
messages retain their original language. Response language is distinct from UI
language; the desktop app's language is not used. To select Spanish manually:
`CODEX_PANEL_LANG=es codex-panel -p personal`.

`tui.theme` is reread from `~/.codex/config.toml` and the profile every second.
Saving a change through `/theme` refreshes sidebar and floating-view colors.
Unsaved picker previews cannot be detected externally. Custom `.tmTheme` files
are read from `$CODEX_HOME/themes`. As in Codex, **normal background and text use
terminal defaults**, not the syntax editor's background/foreground. Accents follow
the saved theme; selection uses reverse video and secondary text is dimmed, so a
syntax theme does not introduce a separate background block.

The panel includes verified palettes for all 32 themes in this CLI version, plus
custom themes. Local terminfo combines direct RGB and exact ANSI/base16 theme
indexes without changing the terminal or the main Codex environment. Other
terminals use a 256-color approximation when direct color is unavailable.
Unknown themes use terminal colors and are labeled as a fallback.
`window-style` and `window-active-style` are set only on the monitor pane
(`set-option -p`); status-bar styling applies only to its session. Floating views
receive their own styles when opened. `.tmux.conf`, the emulator's global palette,
the main pane, and other sessions' styles are unchanged. Theme overrides through
`-c`, project configuration, or a remote host are not detected; the saved
global/profile selection is followed.

References: [Codex syntax themes](https://learn.chatgpt.com/docs/cli-customization)
and [tmux pane/session options](https://man.openbsd.org/tmux).
Metrics: [credit rates](https://learn.chatgpt.com/docs/pricing),
[Fast mode](https://learn.chatgpt.com/docs/agent-configuration/speed), and
[account quota](https://learn.chatgpt.com/docs/app-server).

## Compatibility and dependencies

Validated with Linux, Codex CLI 0.153.0, Python 3, and tmux 3.7b. Uses only the
Python standard library. It does not support `--ephemeral` sessions, `--remote`
hosts, or processes inaccessible through `/proc`. Codex's local formats are
internal; updates may require reader changes. This is not an official plugin.

The portable installer detects a real tmux on the target machine and generates
the launcher with that path. It does not copy Nix binaries or use/replace tmux
shims. Set a custom path with `CODEX_PANEL_TMUX`. Python 3.11+ with extended-color
curses and `tic` are required; `tic` compiles terminfo for the target machine.
Each launch uses a separate, uniquely named tmux server without loading your
`.tmux.conf`, inheriting another launch's environment, or changing existing sessions.

## Diagnostics without starting models

```sh
codex-panel watch --thread SESSION_ID --once
codex-panel watch --pid CODEX_PID --once
cd ~/.local/share/codex-panel
python3 -m unittest discover -v
```

A launcher process temporarily keeps its PID and pane controls in a private
directory under `XDG_RUNTIME_DIR` (or `/tmp`). Launch arguments are removed when
Codex starts. The control directory survives hiding the panel and may remain
until reboot after tmux closes. It contains no transcripts or credentials.

To uninstall, remove `~/.local/bin/codex-panel` and `~/.local/share/codex-panel`
after closing their sessions. Original Codex configuration and profiles do not
need restoration. See the [usage guide](../usage.en.md#panel) for removing the
separate Bash/Zsh launcher integration.
