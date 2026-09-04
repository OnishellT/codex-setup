# Ponytail 4.9.0 for Codex CLI

This module vendors the official Ponytail hook code at the commit in
upstream/PROVENANCE.json. It uses a private instruction resource for the hook
adapter; it also installs the six official Ponytail skills. No npm install, network access,
marketplace or global plugin enablement is required. Python 3 and Node.js >= 18
must be on PATH.

Review the three `[codex-setup:ponytail]` definitions in `/hooks`, trust them,
and start a new CLI session. The default is full. The skills appear in `/skills`:
`ponytail`, `ponytail-audit`, `ponytail-debt`, `ponytail-gain`, `ponytail-help`,
and `ponytail-review`. For a mode change, send the literal
text `$ponytail lite`, `$ponytail full`, `$ponytail ultra`, or `$ponytail off`.
`stop ponytail` also disables it for the current session.

Both work and personal use the shared installation; session modes are separated
by hashed Codex session id. Subagents inherit their parent's mode. The adapter
preserves runtime choices (off/lite/full/ultra) across resume and compaction.
`$ponytail default lite` changes the default for future sessions in this Codex
home, not other agents. Upstream's special review mode is reset to the configured
runtime default on SessionStart; it is not a persistent default mode.

The wrapper sets PLUGIN_DATA for Codex JSON output and stores preferences inside
this integration. It does not write ~/.claude or the shared ~/.config/ponytail.
State directories contain mode flags only, not prompts or transcripts.

Do not also install the marketplace Ponytail plugin without disabling/removing
these hooks first, or you will inject duplicate guidance. This integration does
not enable unrelated plugins or skills. To disable it, use `/hooks`; merely
deselecting a setup module is not uninstall.

Sources: https://github.com/DietrichGebert/ponytail and
https://learn.chatgpt.com/docs/hooks
