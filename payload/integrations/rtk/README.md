# RTK for Codex CLI

Requires Python 3 and RTK >= 0.23 on the non-interactive shell PATH. Validated
with RTK 0.40.0 and Codex CLI 0.153.0. The installer does not download binaries.

The PreToolUse hook passes Bash commands as one argument to `rtk rewrite` and
returns Codex's documented `updatedInput`. RTK decides which commands it can
optimize. Unrecognized commands, invalid inputs, missing RTK and timeouts pass
through unchanged. Only RTK exit 0 is rewritten automatically. Exit 3 means
RTK's Ask/Default permission verdict and is deliberately passed through: Codex
requires `allow` with `updatedInput`, so the adapter must not turn Ask into Allow.
This conservative behavior can leave some supported commands uncompressed.
The adapter does not execute the requested shell command,
change approval settings, or intercept subsequent interactive stdin writes.

Review and trust the `[codex-setup:rtk]` hook in `/hooks`. Both work and personal
inherit this user-level hook. Set `RTK_DISABLED=1` before starting Codex or
disable this hook in `/hooks` to stop automatic rewriting. Use `rtk gain` for
RTK's statistics; running `rewrite` alone does not generate savings.

Sources: https://github.com/rtk-ai/rtk and
https://learn.chatgpt.com/docs/hooks#pretooluse

RTK exit-code contract:
https://github.com/rtk-ai/rtk/blob/v0.40.0/src/hooks/rewrite_cmd.rs
