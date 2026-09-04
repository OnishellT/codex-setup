#!/usr/bin/env python3
"""Dispatch the shell's original Codex arguments without rewriting or logging them.

The generated wrapper supplies CODEX_PANEL_REAL_CODEX and CODEX_PANEL_LAUNCHER
as absolute executable paths. PATH lookup is a fallback, not shell evaluation.
This module deliberately does not import the panel or change the working directory.
"""

from dataclasses import dataclass
import os
import shutil
import sys
from typing import Mapping, Optional, Sequence


PASSTHROUGH_COMMANDS = frozenset({
    "exec", "e", "review", "login", "logout", "mcp", "plugin", "mcp-server",
    "app-server", "remote-control", "completion", "update", "doctor", "sandbox",
    "debug", "apply", "a", "agents", "queue", "archive", "delete",
    "migrate-rollouts", "unarchive", "cloud", "exec-server", "features", "help",
})

# From the installed CLI's root/resume/fork --help. Keep this an allowlist:
# newly introduced options belong to the official CLI until explicitly supported.
VALUE_OPTIONS = frozenset({
    "-c", "--config", "--enable", "--disable", "-i", "--image", "-m", "--model",
    "--local-provider", "-p", "--profile", "-s", "--sandbox", "-C", "--cd",
    "--add-dir", "-a", "--ask-for-approval",
})
SWITCH_OPTIONS = frozenset({
    "--strict-config", "--oss", "--approve-for-me",
    "--dangerously-bypass-approvals-and-sandbox", "--yolo",
    "--dangerously-bypass-hook-trust", "--search", "--no-alt-screen",
})
DIRECT_OPTIONS = frozenset({
    "-h", "--help", "-V", "--version", "--remote", "--remote-auth-token-env",
    "--ephemeral",
})
_EXEC_PID = "_CODEX_PANEL_ENTRY_EXEC_PID"


@dataclass(frozen=True)
class Route:
    """A routing decision; profile is only a panel theme hint, never CLI input."""

    target: str
    profile: str = ""


def classify(
    argv: Sequence[str], *, stdin_isatty: bool = True, stdout_isatty: bool = True,
    environ: Optional[Mapping[str, str]] = None,
) -> Route:
    """Pure, conservative classifier. Never mutate argv or validate CLI semantics.

    Repeated profiles use the last value for the theme; the official CLI still
    receives every occurrence and decides whether the invocation is valid.
    Detached image options greedily consume values until the next option;
    attached image values consume only their own token. Arbitrary positional
    prompt text is never split into words.
    """
    env = environ if environ is not None else {}
    official = Route("official")
    if (not stdin_isatty or not stdout_isatty
            or env.get("CODEX_PANEL_DISABLE") == "1"
            or env.get("CODEX_PANEL_ACTIVE") == "1"):
        return official

    profile = ""
    command = None
    first_positional = True
    index = 0
    while index < len(argv):
        token = argv[index]
        index += 1
        if token == "--":
            break
        if token.startswith("-") and token != "-":
            option, equals, value = token.partition("=")
            attached = bool(equals)
            # All supported short options except help/version take a value.
            # Thus -pfoo, -p=foo, -C../work and -cmodel=x are unambiguous.
            if not token.startswith("--") and token[:2] in VALUE_OPTIONS:
                option = token[:2]
                value = token[2:]
                attached = bool(value)
                if value.startswith("="):
                    value = value[1:]
            if option in DIRECT_OPTIONS:
                return official
            if option in VALUE_OPTIONS:
                if not attached:
                    if index == len(argv) or argv[index].startswith("-"):
                        return official
                    value = argv[index]
                    index += 1
                    if option in {"-i", "--image"}:
                        while index < len(argv) and not argv[index].startswith("-"):
                            index += 1
                if option in {"-p", "--profile"}:
                    profile = value
                continue
            switches = SWITCH_OPTIONS
            if command in {"resume", "fork"}:
                switches = switches | {"--last", "--all"}
            if command == "resume":
                switches = switches | {"--include-non-interactive"}
            if option not in switches or attached:
                return official
            continue

        if first_positional:
            first_positional = False
            if token in PASSTHROUGH_COMMANDS:
                return official
            if token in {"resume", "fork"}:
                command = token
        # Subsequent positionals are session IDs/prompts, not subcommands.
    return Route("panel", profile)


class _LaunchError(Exception):
    def __init__(self, message: str, status: int):
        super().__init__(message)
        self.status = status


def _same_file(left: str, right: str) -> bool:
    if not left or not right:
        return False
    if os.path.realpath(left) == os.path.realpath(right):
        return True
    try:
        return os.path.samefile(left, right)
    except OSError:
        return False


def _executable(candidate: Optional[str], label: str, variable: str) -> str:
    if not candidate or not os.path.isfile(candidate):
        raise _LaunchError(f"{label} executable is missing; install it or set {variable}.", 127)
    if not os.path.isabs(candidate):
        raise _LaunchError(f"{variable} must name an absolute executable path.", 126)
    if not os.access(candidate, os.X_OK):
        raise _LaunchError(f"{label} is not executable; check {variable}.", 126)
    return candidate


def _lookup(env: Mapping[str, str], variable: str, name: str) -> Optional[str]:
    if env.get(variable):
        return env[variable]
    found = shutil.which(name)
    return os.path.abspath(found) if found else None


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Replace this process; only launcher errors return (126 or 127)."""
    original = list(sys.argv[1:] if argv is None else argv)
    childenv = os.environ.copy()
    try:
        # An incorrectly configured exec-style wrapper can have a different
        # filename. PID scoping catches re-entry without blocking child shells.
        if childenv.get(_EXEC_PID) == str(os.getpid()):
            raise _LaunchError("recursive dispatcher invocation; check CODEX_PANEL_REAL_CODEX.", 126)
        real = _executable(
            _lookup(childenv, "CODEX_PANEL_REAL_CODEX", "codex"),
            "Official Codex", "CODEX_PANEL_REAL_CODEX",
        )
        launcher = _lookup(childenv, "CODEX_PANEL_LAUNCHER", "codex-panel")
        self_paths = [__file__, sys.argv[0], os.path.join(os.path.dirname(__file__), "codex_panel.py")]
        if any(_same_file(real, path) for path in self_paths + [launcher or ""]):
            raise _LaunchError("CODEX_PANEL_REAL_CODEX points to the dispatcher or panel, not official Codex.", 126)

        route = classify(original, stdin_isatty=sys.stdin.isatty(),
                         stdout_isatty=sys.stdout.isatty(), environ=childenv)
        childenv["CODEX_PANEL_REAL_CODEX"] = real
        childenv[_EXEC_PID] = str(os.getpid())
        if route.target == "panel":
            launcher = _executable(launcher, "codex-panel", "CODEX_PANEL_LAUNCHER")
            if any(_same_file(launcher, path) for path in self_paths):
                raise _LaunchError("CODEX_PANEL_LAUNCHER points to the dispatcher or panel module; use installed codex-panel.", 126)
            childenv["CODEX_PANEL_ACTIVE"] = "1"
            os.execvpe(launcher, [launcher, "--native", "--profile", route.profile, "--", *original], childenv)
        else:
            os.execvpe(real, [real, *original], childenv)
    except _LaunchError as error:
        print(f"codex-entry: {error}", file=sys.stderr)
        return error.status
    except OSError as error:
        # Do not print the exception: filenames/messages may contain user data.
        print("codex-entry: unable to execute configured executable; check installation and permissions.", file=sys.stderr)
        return 127 if isinstance(error, FileNotFoundError) else 126
    return 0  # Only reachable when execvpe is mocked in tests.


if __name__ == "__main__":
    raise SystemExit(main())
