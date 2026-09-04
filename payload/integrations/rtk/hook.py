#!/usr/bin/env python3
"""Rewrite Codex Bash calls through RTK; never execute the supplied command here."""

import json
import os
import re
import subprocess
import sys

MAX_INPUT_BYTES = 2 * 1024 * 1024
REWRITE_TIMEOUT = 2


def rewrite(event):
    if not isinstance(event, dict) or event.get("hook_event_name") != "PreToolUse":
        return None
    if event.get("tool_name") not in ("Bash", "bash", "exec_command", "shell_command"):
        return None
    if os.environ.get("RTK_DISABLED") == "1":
        return None
    original = event.get("tool_input")
    if not isinstance(original, dict):
        return None
    command = original.get("command")
    if not isinstance(command, str) or not command.strip() or "\x00" in command:
        return None
    if re.match(r"^\s*rtk(?:\s|$)", command):
        return None
    # `rewrite` is a parser: command is one argv element, never shell=True.
    result = subprocess.run(
        ["rtk", "rewrite", "--", command],
        capture_output=True,
        text=True,
        timeout=REWRITE_TIMEOUT,
        check=False,
    )
    # RTK 0.40: 3 means rewrite available but Ask/Default permission verdict.
    # Codex requires "allow" for updatedInput, so never promote that to allow.
    if result.returncode != 0:
        return None
    rewritten = result.stdout.strip()
    if not rewritten or rewritten == command or "\x00" in rewritten:
        return None
    # Keep cwd, timeout and other tool input fields. Do not change approval or
    # sandbox settings; this is PreToolUse, never PermissionRequest.
    return {
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "allow",
            "updatedInput": {**original, "command": rewritten},
        }
    }


def main():
    try:
        raw = sys.stdin.buffer.read(MAX_INPUT_BYTES + 1)
        if len(raw) > MAX_INPUT_BYTES:
            return
        output = rewrite(json.loads(raw.decode("utf-8-sig")))
        if output:
            print(json.dumps(output))
    except (ValueError, OSError, RecursionError, subprocess.SubprocessError):
        # Missing RTK, invalid JSON, timeout or rewrite failure: pass through.
        # Never log the command, which could contain private data.
        return


if __name__ == "__main__":
    main()
