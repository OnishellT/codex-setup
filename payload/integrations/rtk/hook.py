#!/usr/bin/env python3
"""Rewrite Codex Bash calls through RTK; never execute the supplied command here."""

import hashlib
import json
import os
import platform
import re
import shlex
import subprocess
import sys
from pathlib import Path

MAX_INPUT_BYTES = 2 * 1024 * 1024
REWRITE_TIMEOUT = 2
RTK_HASHES = {
    "x86_64": "b04ea330c46265634f214f14934acb89e7cabb2b1562c74d9a68f497aa41ad23",
    "aarch64": "43b0d54259fb2ba2537bd6ad9bf5ed3c01db497674ac19b5b865a05b34b7b861",
}


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
    executable = rtk_command()
    if executable is None:
        return None
    result = subprocess.run(
        [executable, "rewrite", "--", command],
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
    if not re.match(r"^rtk(?:\s|$)", rewritten):
        return None
    rewritten = shlex.quote(executable) + rewritten[3:]
    # Keep cwd, timeout and other tool input fields. Do not change approval or
    # sandbox settings; this is PreToolUse, never PermissionRequest.
    return {
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "allow",
            "updatedInput": {**original, "command": rewritten},
        }
    }


def rtk_command():
    """Use only the pinned private binary next to this installed hook."""
    managed = Path(__file__).resolve().parent / "bin" / "rtk"
    if not managed.is_file() or managed.is_symlink() or not os.access(managed, os.X_OK):
        return None
    if managed.stat().st_size > 16 * 1024 * 1024:
        return None
    with managed.open("rb") as binary:
        digest = hashlib.file_digest(binary, "sha256").hexdigest()
    return str(managed) if digest == RTK_HASHES.get(platform.machine()) else None


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
