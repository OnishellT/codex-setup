#!/usr/bin/env python3
"""Run pinned upstream Ponytail hooks with Codex-local, session-scoped state."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent
MAX_INPUT_BYTES = 2 * 1024 * 1024
HOOKS = {
    "activate": ("SessionStart", "ponytail-activate.js"),
    "subagent": ("SubagentStart", "ponytail-subagent.js"),
    "prompt": ("UserPromptSubmit", "ponytail-mode-tracker.js"),
}


def run_hook(action, event):
    expected_event, script = HOOKS[action]
    if not isinstance(event, dict) or event.get("hook_event_name") != expected_event:
        return None
    session_id = event.get("session_id")
    if not isinstance(session_id, str) or not session_id.strip():
        return None
    # SubagentStart's session_id is the parent's in Codex's hook contract.
    # Hash instead of embedding a caller-controlled identifier in a path.
    state_root = ROOT / "state"
    state_dir = state_root / hashlib.sha256(session_id.encode()).hexdigest()
    for directory in (state_root, state_dir):
        if directory.is_symlink():
            return None
        directory.mkdir(mode=0o700, exist_ok=True)
    marker = state_dir / ".initialized"
    flag = state_dir / ".ponytail-active"
    if marker.is_symlink() or flag.is_symlink():
        return None
    env = os.environ.copy()
    # Upstream uses PLUGIN_DATA to select Codex JSON output, avoiding ~/.claude.
    env["PLUGIN_DATA"] = str(state_dir)
    env["CLAUDE_PLUGIN_ROOT"] = str(ROOT / "upstream")
    env["XDG_CONFIG_HOME"] = str(ROOT / "preferences")
    env.pop("COPILOT_PLUGIN_DATA", None)
    env.pop("QODER_SESSION_ID", None)
    # Upstream resets the mode on every SessionStart. Keep explicit choices
    # (including off) across resume/compaction, but let clear reset the mode.
    if action == "activate" and marker.exists() and event.get("source") != "clear":
        mode = flag.read_text().strip() if flag.exists() else "off"
        if mode in ("off", "lite", "full", "ultra"):
            env["PONYTAIL_DEFAULT_MODE"] = mode
    result = subprocess.run(
        ["node", str(ROOT / "upstream" / "hooks" / script)],
        input=json.dumps(event),
        capture_output=True,
        text=True,
        env=env,
        timeout=3,
        check=False,
    )
    if result.returncode != 0:
        return None
    output = json.loads(result.stdout) if result.stdout.strip() else None
    if output is not None and not isinstance(output, dict):
        return None
    if action == "activate":
        marker.touch(mode=0o600)
    return output


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in HOOKS:
        return
    try:
        raw = sys.stdin.buffer.read(MAX_INPUT_BYTES + 1)
        if len(raw) > MAX_INPUT_BYTES:
            return
        output = run_hook(sys.argv[1], json.loads(raw.decode("utf-8-sig")))
        if output:
            print(json.dumps(output))
    except (ValueError, OSError, RecursionError, subprocess.SubprocessError):
        # Optional productivity hooks must not fail the user's task.
        # No prompt, transcript, or secret is printed on failure.
        return


if __name__ == "__main__":
    main()
