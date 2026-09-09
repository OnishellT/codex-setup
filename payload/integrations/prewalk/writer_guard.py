#!/usr/bin/env python3
"""Admit Prewalk writer spawns only with an isolated worktree.

This is a practical admission check at PreToolUse. It does not claim to lock
the filesystem or observe worker completion; native thread limits and the
worktree helper remain responsible for lifecycle.
"""

import fcntl
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sys

try:
    from worktrees import load
except ImportError:  # pragma: no cover - only needed when loaded by a file path
    load = None


LIMIT = 2 * 1024 * 1024
WRITERS = {"prewalk_executor", "fallback_executor"}
MANIFEST = re.compile(r"(?<!\S)(/[^\s\"'`,;]+/manifest\.json)")


def admission_path(session_id, task, home=None):
    if not isinstance(session_id, str) or not session_id or not isinstance(task, str) or not re.fullmatch(r"[a-z0-9_]+", task):
        raise ValueError("native parent session and task_name are required")
    root = Path(home or Path(__file__).resolve().parents[2]) / "worktrees" / "prewalk" / ".admissions"
    if root.is_symlink():
        raise ValueError("admission directory cannot be a symlink")
    digest = hashlib.sha256((session_id + "\0" + task).encode()).hexdigest()
    path = root / (digest + ".json")
    if path.is_symlink():
        raise ValueError("admission cannot be a symlink")
    return path


def register(manifest_path, worker_name, task, session_id, role="prewalk_executor", home=None):
    from worktrees import check_worktree
    if role not in WRITERS:
        raise ValueError("invalid writer role")
    path, manifest = load(manifest_path)
    worker = next((w for w in manifest["workers"] if w["name"] == worker_name), None)
    if worker is None:
        raise ValueError("worker is not in the manifest")
    check_worktree(worker, Path(manifest["repo"]), manifest["base"])
    value = {"manifest": str(path), "worktree": worker["path"], "role": role}
    target = admission_path(session_id, task, home)
    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    try:
        fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        if json.loads(target.read_text()) != value:
            raise ValueError("task already registered with a different assignment")
    else:
        with os.fdopen(fd, "w") as output:
            json.dump(value, output)
    return value


def deny(reason):
    return {"hookSpecificOutput": {"hookEventName": "PreToolUse",
            "permissionDecision": "deny", "permissionDecisionReason": reason}}


def strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for child in value.values():
            yield from strings(child)
    elif isinstance(value, list):
        for child in value:
            yield from strings(child)


def spawn_tool(name):
    return isinstance(name, str) and (name.endswith("spawn_agent") or name == "Agent")


def manifest_candidates(args):
    found = set()
    for value in strings(args):
        if value.startswith("/") and value.endswith("/manifest.json"):
            found.add(value)
        found.update(MANIFEST.findall(value))
        for line in value.splitlines():
            try:
                marker = json.loads(line)
            except ValueError:
                continue
            if (isinstance(marker, dict) and isinstance(marker.get("manifest"), str)
                    and Path(marker["manifest"]).is_absolute()):
                found.add(marker["manifest"])
    return {str(Path(path).resolve()) for path in found}


def worker_candidates(args, workers):
    found = set()
    for value in strings(args):
        for worker in workers:
            path = worker["path"]
            if path in value:
                found.add(path)
    return found


def guard(event, mode="auto", codex_home_path=None):
    if not isinstance(event, dict) or event.get("hook_event_name") != "PreToolUse":
        return None
    name = event.get("tool_name")
    if mode == "auto" and not spawn_tool(name):
        return None
    if mode != "auto" and mode != "spawn":
        return None
    args = event.get("tool_input")
    if not spawn_tool(name) or not isinstance(args, dict):
        return deny("Prewalk writer guard could not validate the spawn request.")
    if args.get("agent_type") not in WRITERS:
        return None

    manifests = manifest_candidates(args)
    # V2 encrypts the message before PreToolUse. Bind its public task name to
    # a planner-registered assignment; never try to decrypt agent messages.
    if not manifests:
        try:
            assignment = json.loads(admission_path(event.get("session_id"), args.get("task_name"), codex_home_path).read_text())
            if assignment["role"] != args["agent_type"]:
                return deny("Prewalk writer role differs from its registered assignment.")
            args = {"manifest": assignment["manifest"], "worktree": assignment["worktree"]}
            manifests = manifest_candidates(args)
        except (OSError, ValueError, TypeError, KeyError):
            return deny("Prewalk writer requires a registered task for this session; run writer_guard.py register before spawning it.")
    if len(manifests) != 1:
        return deny("Prewalk writer requires exactly one valid worktree manifest; shared checkout writes are refused.")
    if load is None:
        return deny("Prewalk writer guard cannot load the worktree manifest.")
    try:
        _, manifest = load(next(iter(manifests)))
    except (OSError, ValueError, RuntimeError, TypeError):
        return deny("Prewalk writer requires a valid worktree manifest.")
    workers = manifest["workers"]
    selected = worker_candidates(args, workers)
    if len(selected) != 1:
        return deny("Prewalk writer requires one explicit worker worktree from the manifest.")
    selected_path = Path(next(iter(selected))).resolve()
    selected_worker = next(worker for worker in workers if Path(worker["path"]).resolve() == selected_path)
    try:
        from worktrees import check_worktree
        check_worktree(selected_worker, Path(manifest["repo"]).resolve(), manifest["base"])
    except (ImportError, OSError, ValueError, RuntimeError, TypeError, StopIteration):
        return deny("Prewalk writer requires a clean, valid Git worktree from the manifest.")
    cwd = Path(event.get("cwd") or os.getcwd()).resolve()
    if selected_path == cwd:
        return deny("Prewalk writer cannot target the shared checkout; use its distinct manifest worktree.")
    session = Path(manifest["session"]).resolve()
    reservations = session / ".prewalk-writers"
    if reservations.exists() and reservations.is_symlink():
        return deny("Prewalk writer reservation directory cannot be a symlink.")
    try:
        reservations.mkdir(mode=0o700, exist_ok=True)
        lock_path = reservations / ".lock"
        if lock_path.exists() and lock_path.is_symlink():
            return deny("Prewalk writer reservation lock cannot be a symlink.")
        with lock_path.open("a+") as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            reservation = reservations / (selected_worker["name"] + ".json")
            fd = os.open(reservation, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, "w") as reserved:
                json.dump({"worker": selected_worker["name"], "path": str(selected_path)}, reserved)
    except FileExistsError:
        return deny("Prewalk writer worktree is already reserved; retry with a fresh worktree and manifest.")
    except (OSError, ValueError, TypeError):
        return deny("Prewalk writer reservation could not be established safely.")
    return None


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "register":
        parser = argparse.ArgumentParser()
        parser.add_argument("register")
        parser.add_argument("--manifest", required=True)
        parser.add_argument("--worker", required=True)
        parser.add_argument("--task", required=True)
        parser.add_argument("--role", choices=sorted(WRITERS), default="prewalk_executor")
        args = parser.parse_args()
        try:
            print(json.dumps(register(args.manifest, args.worker, args.task, os.environ.get("CODEX_THREAD_ID"), args.role)))
        except (OSError, ValueError, RuntimeError, TypeError) as error:
            parser.exit(1, "Prewalk registration: " + str(error) + "\n")
        return
    try:
        raw = sys.stdin.buffer.read(LIMIT + 1)
        if len(raw) > LIMIT:
            raise ValueError("oversized input")
        event = json.loads(raw)
        output = guard(event, sys.argv[1] if len(sys.argv) > 1 else "auto")
    except (ImportError, ValueError, OSError, RuntimeError, TypeError):
        output = deny("Prewalk writer guard could not validate this tool call; retry with explicit worktree evidence.")
    if output:
        print(json.dumps(output))


if __name__ == "__main__":
    main()
