#!/usr/bin/env python3
"""Context alerts and compact handoff artifacts, using only the stdlib."""

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile


MAX_HOOK_INPUT = 2 * 1024 * 1024
TRANSCRIPT_TAIL = 2 * 1024 * 1024
DEFAULTS = {"warning_percent": 70, "critical_percent": 85,
            "handoff_max_chars": 12000, "model_thresholds": {
                "gpt-5.6": {"warning_tokens": 250000, "critical_tokens": 272000},
                "gpt-5.6-sol": {"warning_tokens": 250000, "critical_tokens": 272000},
            }}
REQUIRED = (
    "## Objective", "## Acceptance criteria", "## Decisions",
    "## Completed", "## Pending", "## Relevant files", "## Validation",
    "## Risks and blockers", "## Next action",
)
PLACEHOLDER = "REPLACE:"
FORBIDDEN = re.compile(
    r"(?:\.codex[/\\](?:sessions|archived_sessions|history\.jsonl)|"
    r"state_\d+\.sqlite|(?:api[_-]?key|password|secret)\s*[:=]\s*[^\s`]+|"
    r"\bsk-[A-Za-z0-9_-]{20,})", re.I
)


def _codex_home():
    return Path(os.environ.get("CODEX_HOME") or Path.home() / ".codex").expanduser().resolve()


def _settings():
    result = dict(DEFAULTS)
    try:
        value = json.loads((Path(__file__).with_name("settings.json")).read_text())
        if not isinstance(value, dict):
            return result
        warning, critical = value.get("warning_percent"), value.get("critical_percent")
        maximum = value.get("handoff_max_chars")
        if isinstance(warning, (int, float)) and isinstance(critical, (int, float)) \
                and 0 < warning < critical < 100:
            result["warning_percent"], result["critical_percent"] = warning, critical
        if type(maximum) is int and 2000 <= maximum <= 100000:
            result["handoff_max_chars"] = maximum
        if isinstance(value.get("model_thresholds"), dict):
            result["model_thresholds"] = value["model_thresholds"]
    except (OSError, ValueError, TypeError):
        pass
    return result


def _counter(value, positive=False):
    return value if type(value) is int and value >= (1 if positive else 0) else None


def _latest_context(path):
    path = Path(path)
    with path.open("rb") as stream:
        stream.seek(0, os.SEEK_END)
        size = stream.tell()
        stream.seek(max(0, size - TRANSCRIPT_TAIL))
        data = stream.read(TRANSCRIPT_TAIL)
    lines = data.splitlines()
    if size > TRANSCRIPT_TAIL and lines:
        lines = lines[1:]  # The first line may be a truncated JSON record.
    for raw in reversed(lines):
        try:
            record = json.loads(raw)
            payload = record.get("payload", {})
            info = payload.get("info", {})
            usage = info.get("last_token_usage", {})
            if record.get("type") != "event_msg" or payload.get("type") != "token_count":
                continue
            tokens = _counter(usage.get("input_tokens"))
            window = _counter(info.get("model_context_window"), positive=True)
            if tokens is not None and window is not None:
                return tokens, window
        except (AttributeError, TypeError, ValueError):
            continue
    return None


def _thresholds(settings, model, window):
    warning, critical = settings["warning_percent"], settings["critical_percent"]
    override = settings["model_thresholds"].get(model)
    if isinstance(override, dict):
        for key, current in (("warning_tokens", warning), ("critical_tokens", critical)):
            tokens = _counter(override.get(key), positive=True)
            if tokens is not None:
                percent = tokens * 100 / window
                if key == "warning_tokens":
                    warning = min(current, percent)
                else:
                    critical = min(current, percent)
    return warning, max(warning, critical)


def _state_path(session_id):
    digest = hashlib.sha256(session_id.encode()).hexdigest()
    return Path(__file__).with_name("state") / (digest + ".json")


def _read_band(path):
    try:
        value = json.loads(path.read_text())
        return value.get("band", 0) if isinstance(value, dict) else 0
    except (OSError, ValueError, TypeError):
        return 0


def _write_state(path, band, tokens, window):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    temporary = path.with_name(path.name + "." + str(os.getpid()))
    temporary.write_text(json.dumps({"band": band, "input_tokens": tokens,
                                     "model_context_window": window}) + "\n")
    os.chmod(temporary, 0o600)
    temporary.replace(path)


def hook(event):
    """Return a Codex hook response, or None. Errors intentionally fail open."""
    if not isinstance(event, dict):
        return None
    name, session_id = event.get("hook_event_name"), event.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        return None
    state = _state_path(session_id)
    if name == "PostCompact":
        try:
            state.unlink(missing_ok=True)
        except OSError:
            pass
        return None
    if name != "Stop" or not isinstance(event.get("transcript_path"), str):
        return None
    try:
        current = _latest_context(event["transcript_path"])
        if current is None:
            return None
        tokens, window = current
        warning, critical = _thresholds(_settings(), event.get("model"), window)
        percent = tokens * 100 / window
        band = 2 if percent >= critical else 1 if percent >= warning else 0
        previous = _read_band(state)
        _write_state(state, max(previous, band), tokens, window)
    except (OSError, ValueError, TypeError):
        return None
    if band <= previous:
        return None
    if band == 2:
        message = (f"Contexto crítico: la última entrada usa {percent:.0f}% de la ventana "
                   f"({tokens:,}/{window:,}). Usa $handoff para continuar en una tarea nueva.")
    else:
        message = (f"Contexto alto: la última entrada usa {percent:.0f}% de la ventana "
                   f"({tokens:,}/{window:,}). Considera /compact; usa $handoff si vas a continuar mucho tiempo.")
    return {"systemMessage": message}


def _run_git(cwd, *args):
    try:
        result = subprocess.run(["git", "-C", str(cwd), *args], capture_output=True,
                                text=True, timeout=2, check=False)
        return result.stdout.strip()[:4000] if result.returncode == 0 else None
    except (OSError, subprocess.TimeoutExpired):
        return None


def _git_hash(cwd, *args):
    """Hash complete Git output without retaining it in memory."""
    try:
        with tempfile.TemporaryFile() as output:
            result = subprocess.run(["git", "-C", str(cwd), *args], stdout=output,
                                    stderr=subprocess.DEVNULL, timeout=2, check=False)
            if result.returncode != 0:
                return None
            output.seek(0)
            digest = hashlib.sha256()
            while chunk := output.read(64 * 1024):
                digest.update(chunk)
            return digest.hexdigest()
    except (OSError, subprocess.TimeoutExpired):
        return None


def _git_snapshot(cwd):
    root = _run_git(cwd, "rev-parse", "--show-toplevel")
    if root is None:
        return {"repository": False}
    status = _run_git(cwd, "status", "--short") or "clean"
    worktrees = _run_git(cwd, "worktree", "list", "--porcelain") or "unavailable"
    return {
        "repository": True,
        "root": root,
        "head": _run_git(cwd, "rev-parse", "HEAD") or "unborn",
        "branch": _run_git(cwd, "branch", "--show-current") or "detached",
        "status": status,
        "worktrees": worktrees,
        "status_hash": _git_hash(cwd, "status", "--short"),
        "worktrees_hash": _git_hash(cwd, "worktree", "list", "--porcelain"),
    }


def _git_metadata(snapshot):
    if not snapshot.get("repository"):
        return {"repository": False}
    return {
        "repository": True,
        "head": snapshot.get("head"),
        "branch": snapshot.get("branch"),
        "status_hash": snapshot.get("status_hash"),
        "worktrees_hash": snapshot.get("worktrees_hash"),
    }


def _private_directory(path):
    if path.is_symlink():
        raise ValueError("handoff directory cannot be a symlink")
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    if path.is_symlink() or not path.is_dir():
        raise ValueError("invalid handoff directory")
    os.chmod(path, 0o700)


def _new(cwd):
    cwd = Path(cwd).expanduser().resolve()
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    handoff_id = f"{stamp}-{secrets.token_hex(3)}"
    project_hash = hashlib.sha256(str(cwd).encode()).hexdigest()[:12]
    root = _codex_home() / "handoffs"
    _private_directory(root)
    directory = root / project_hash
    _private_directory(directory)
    path = directory / (handoff_id + ".md")
    git = _git_snapshot(cwd)
    meta = {"id": handoff_id, "project": str(cwd), "created_utc": stamp,
            "git": _git_metadata(git)}
    git_text = "Not a Git repository"
    if git["repository"]:
        git_text = (f"Branch: {git['branch']}\nHEAD: {git['head']}\nStatus:\n{git['status']}\n"
                    f"Worktrees:\n{git['worktrees']}")
    body = f"""# Codex handoff: {handoff_id}
<!-- handoff-meta: {json.dumps(meta, ensure_ascii=False, separators=(',', ':'))} -->

Project: {cwd}
Created UTC: {stamp}

## Objective
{PLACEHOLDER} Summarize the user's original objective.

## Acceptance criteria
{PLACEHOLDER} List observable completion conditions.

## Decisions
{PLACEHOLDER} Record decisions that constrain the continuation.

## Completed
{PLACEHOLDER} Summarize completed work, without conversation history.

## Pending
{PLACEHOLDER} List unfinished work.

## Relevant files
{PLACEHOLDER} List only files needed to continue.

## Git state
{git_text}

## Validation
{PLACEHOLDER} List checks already run and their results.

## Risks and blockers
{PLACEHOLDER} List known risks, blockers, or `None`.

## Next action
{PLACEHOLDER} State the first concrete continuation step.
"""
    with path.open("x") as stream:
        stream.write(body)
    os.chmod(path, 0o600)
    return {"id": handoff_id, "path": str(path)}


def _artifact(handoff_id):
    if not re.fullmatch(r"[A-Za-z0-9._-]{1,80}", handoff_id):
        raise ValueError("invalid handoff id")
    root = _codex_home() / "handoffs"
    matches = []
    if root.is_dir():
        for path in root.glob("*/" + handoff_id + ".md"):
            resolved = path.resolve()
            if root.resolve() in resolved.parents and resolved.is_file():
                matches.append(resolved)
    if len(matches) != 1:
        raise ValueError("handoff id not found or ambiguous")
    return matches[0]


def _metadata(text):
    match = re.search(r"^<!-- handoff-meta: (\{.*\}) -->$", text, re.MULTILINE)
    if not match:
        raise ValueError("missing handoff metadata")
    value = json.loads(match.group(1))
    if not isinstance(value, dict) or not isinstance(value.get("project"), str):
        raise ValueError("invalid handoff metadata")
    return value


def _validate(path):
    source = Path(path).expanduser()
    if source.is_symlink():
        raise ValueError("handoff artifact cannot be a symlink")
    path = source.resolve()
    root = (_codex_home() / "handoffs").resolve()
    if root not in path.parents or not path.is_file():
        raise ValueError("handoff must be stored under CODEX_HOME/handoffs")
    maximum = _settings()["handoff_max_chars"]
    if path.stat().st_size > maximum * 4:
        raise ValueError("artifact too large")
    text = path.read_text()
    missing = [heading for heading in REQUIRED if heading not in text]
    if missing or PLACEHOLDER in text or len(text) > maximum or FORBIDDEN.search(text):
        problem = "missing sections" if missing else "unfinished placeholders" if PLACEHOLDER in text else "artifact too large"
        if FORBIDDEN.search(text):
            problem = "artifact contains a secret or conversation-storage reference"
        raise ValueError(problem)
    meta = _metadata(text)
    if meta.get("id") != path.stem:
        raise ValueError("handoff id does not match its filename")
    return {"id": meta.get("id"), "path": str(path), "chars": len(text), "max_chars": maximum}


def _check(handoff_id, cwd):
    path = _artifact(handoff_id)
    _validate(path)
    meta = _metadata(path.read_text())
    current = Path(cwd).expanduser().resolve()
    before = meta.get("git") if isinstance(meta.get("git"), dict) else {"repository": False}
    after = _git_metadata(_git_snapshot(current))
    hashes = (before.get("status_hash"), before.get("worktrees_hash"),
              after.get("status_hash"), after.get("worktrees_hash"))
    unverified = before.get("repository") and any(value is None for value in hashes)
    return {
        "id": handoff_id, "path": str(path), "project": meta["project"],
        "current_project": str(current), "project_changed": str(current) != meta["project"],
        "repository_changed": before.get("repository") != after.get("repository"),
        "head_changed": before.get("head") != after.get("head"),
        "branch_changed": before.get("branch") != after.get("branch"),
        "dirty_state_changed": (None if unverified else
                                before.get("status_hash") != after.get("status_hash")),
        "worktrees_changed": (None if unverified else
                              before.get("worktrees_hash") != after.get("worktrees_hash")),
        "git_state_unverified": bool(unverified),
    }


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("hook")
    new = commands.add_parser("new")
    new.add_argument("--cwd", default=os.getcwd())
    validate = commands.add_parser("validate")
    validate.add_argument("path")
    show = commands.add_parser("show")
    show.add_argument("id")
    check = commands.add_parser("check")
    check.add_argument("id")
    check.add_argument("--cwd", default=os.getcwd())
    args = parser.parse_args()
    try:
        if args.command == "hook":
            raw = sys.stdin.buffer.read(MAX_HOOK_INPUT + 1)
            if len(raw) > MAX_HOOK_INPUT:
                return
            output = hook(json.loads(raw))
            if output:
                print(json.dumps(output))
        elif args.command == "new":
            print(json.dumps(_new(args.cwd), ensure_ascii=False))
        elif args.command == "validate":
            print(json.dumps(_validate(args.path), ensure_ascii=False))
        elif args.command == "show":
            path = _artifact(args.id)
            _validate(path)
            sys.stdout.write(path.read_text())
        elif args.command == "check":
            print(json.dumps(_check(args.id, args.cwd), ensure_ascii=False))
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as error:
        if args.command == "hook":
            return  # Alerts must never break a Codex turn.
        parser.error(str(error))


if __name__ == "__main__":
    main()
