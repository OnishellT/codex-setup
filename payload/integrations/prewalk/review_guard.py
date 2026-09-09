#!/usr/bin/env python3
"""Practical anti-history guard, not a filesystem sandbox. Never reads transcripts."""

import json
import os
from pathlib import Path
import re
import shlex
import sys

LIMIT = 2 * 1024 * 1024
HISTORY = re.compile(
    r"(?:^|[/\\])(?:sessions|archived_sessions|memories|shell_snapshots|"
    r"history\.jsonl|state_\d+\.sqlite(?:-wal|-shm)?)(?:[/\\\s\"']|$)", re.I
)
TOOLS = re.compile(
    r"(?:read_thread|list_threads|list_archived_threads|"
    r"read_thread_terminal|send_message_to_thread|resume_thread|fork_thread|"
    r"get_thread|read_session|list_sessions|read_conversation|"
    r"list_conversations|search_conversations|search_history)(?:$|__)", re.I
)


def deny(reason):
    return {"hookSpecificOutput": {"hookEventName": "PreToolUse",
            "permissionDecision": "deny", "permissionDecisionReason": reason}}


def strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from strings(item)


def guard(event, mode="auto", codex_home=None):
    if not isinstance(event, dict):
        return deny("Prewalk received a malformed hook event.")
    if event.get("hook_event_name") != "PreToolUse":
        return None
    name = event.get("tool_name", "")
    if not isinstance(name, str) or not name:
        return deny("Prewalk received a malformed tool name.")
    args = event.get("tool_input", {})
    if mode == "auto":
        if event.get("agent_type") == "engineering_reviewer":
            mode = "review"
        elif name.endswith("spawn_agent") or name == "Agent":
            mode = "spawn"
        else:
            return None
    if mode == "spawn":
        if not (name.endswith("spawn_agent") or name == "Agent") or not isinstance(args, dict):
            return None
        if args.get("agent_type") != "engineering_reviewer":
            return None
        # Require an explicit fresh-context choice. Do not rewrite unknown API fields.
        if args.get("fork_turns") == "none" and "fork_context" not in args:
            return None
        if args.get("fork_context") is False and "fork_turns" not in args:
            return None
        return deny("Prewalk reviewer requires fresh context: fork_turns='none' "
                    "(or fork_context=false on the legacy API). Send only the original "
                    "request, plan, project rules and code/base; no implementer history.")

    if TOOLS.search(name):
        return deny("Prewalk reviewer cannot query agent conversations or session history.")
    home = Path(codex_home or os.environ.get("CODEX_HOME") or Path(__file__).resolve().parents[2])
    protected = [home / part for part in ("sessions", "archived_sessions", "memories", "shell_snapshots")]
    transcript = event.get("transcript_path")
    if isinstance(transcript, str) and transcript:
        protected.append(Path(transcript))
    cwd = Path(event.get("cwd") or os.getcwd())
    for value in strings(args):
        expanded = value.replace("${CODEX_HOME}", str(home)).replace("$CODEX_HOME", str(home))
        expanded = os.path.expanduser(os.path.expandvars(expanded))
        if re.search(re.escape(str(home)) + r"[/\\]state_\d+\.sqlite", expanded):
            return deny("Prewalk reviewer cannot read the Codex history database.")
        if re.search(r"\bcodex\s+(?:agents|resume|fork|migrate-rollouts)\b", expanded):
            return deny("Prewalk reviewer cannot open other Codex sessions.")
        # Detect conventional paths, including paths embedded in Python/JS or RTK.
        if re.search(r"(?:\.codex|CODEX_HOME)[/\\].*", expanded, re.I) and HISTORY.search(expanded):
            return deny("Prewalk reviewer cannot read Codex histories or memories.")
        try:
            tokens = shlex.split(expanded)
        except ValueError:
            tokens = [expanded]
        for token in [expanded, *tokens]:
            candidate = token.strip("\"'(),;")
            if "\n" in candidate or not candidate:
                continue
            p = Path(candidate)
            p = (cwd / p if not p.is_absolute() else p).resolve()
            # Resolve existing symlinks without opening the referenced files.
            resolved_home = home.resolve()
            if (p == resolved_home or p in resolved_home.parents or
                    any(p == x.resolve() or x.resolve() in p.parents for x in protected)):
                return deny("Prewalk reviewer cannot read conversation storage (including symlinks).")
            if p.parent == home.resolve() and (p.name == "history.jsonl" or re.match(r"state_\d+\.sqlite", p.name)):
                return deny("Prewalk reviewer cannot read the Codex history database.")
        for path in protected + [home / "history.jsonl"]:
            if str(path) in expanded:
                return deny("Prewalk reviewer cannot read conversation storage.")
    return None


def main():
    try:
        raw = sys.stdin.buffer.read(LIMIT + 1)
        if len(raw) > LIMIT:
            raise ValueError("oversized input")
        event = json.loads(raw)
        output = guard(event, sys.argv[1] if len(sys.argv) > 1 else "auto")
    except (ValueError, OSError, RuntimeError, TypeError):
        output = deny("Prewalk could not validate this tool call; retry with a smaller, explicit request.")
    if output:
        print(json.dumps(output))


if __name__ == "__main__":
    main()
