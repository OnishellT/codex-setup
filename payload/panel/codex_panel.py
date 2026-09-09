#!/usr/bin/env python3
"""Local, read-only Codex subagent sidebar. Python standard library, Linux /proc."""
import argparse
from contextlib import closing
from collections import deque
import curses
import datetime as dt
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import sqlite3
import subprocess
import sys
import tempfile
import textwrap
import time

from telemetry import Telemetry
from transcript import Transcript
from quota_estimate import assign_estimates

SCRIPT = str(Path(__file__).resolve())


def clean(value, limit=500):
    """Never let log text inject terminal control sequences; redact obvious secrets."""
    value = str(value)
    value = re.sub(r"\x1b\][^\x07]*(?:\x07|\x1b\\)", "", value)
    value = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", value)
    value = " ".join("".join(c for c in value if c.isprintable() or c in "\n\t").split())
    value = re.sub(r"(?i)(bearer\s+)[^\s\"']+", r"\1[oculto]", value)
    value = re.sub(r"(?i)(\b(?:api[_-]?key|password|token|secret)[\"']?\s*[=:]\s*)[^\s,;]+", r"\1[REDACTED]", value)
    value = re.sub(r"\bsk-[A-Za-z0-9_-]+", "[oculto]", value)
    return value[:limit]


def epoch(value):
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except (TypeError, ValueError, AttributeError):
        return time.time()


def metadata(path):
    # A fork contains inherited session_meta records. Only the FIRST is its identity.
    try:
        with Path(path).open() as stream:
            record = json.loads(stream.readline())
        return record.get("payload", {}) if record.get("type") == "session_meta" else {}
    except (OSError, ValueError):
        return {}


def parent(meta):
    source = meta.get("source")
    if not isinstance(source, dict):
        return None
    return source.get("subagent", {}).get("thread_spawn", {}).get("parent_thread_id")


def process_identity(pid):
    try:
        # Fields after the final ')' start with process state (field 3).
        return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
    except (OSError, IndexError):
        return None


def process_files(pid):
    """Only inspect this launcher's process tree, never pick latest session by cwd."""
    todo, seen, paths = [pid], set(), set()
    while todo:
        current = todo.pop()
        if current in seen:
            continue
        seen.add(current)
        for children in Path(f"/proc/{current}/task").glob("*/children"):
            try:
                todo.extend(int(p) for p in children.read_text().split())
            except OSError:
                pass
        for fd in Path(f"/proc/{current}/fd").glob("*"):
            try:
                path = os.readlink(fd)
                if not (Path(path).name.startswith("rollout-") and path.endswith(".jsonl")):
                    continue
                # A tool may read another conversation. Only a Codex writer owns it.
                executable = Path(os.readlink(f"/proc/{current}/exe")).name
                info = Path(f"/proc/{current}/fdinfo/{fd.name}").read_text()
                flags = next(int(line.split()[1], 8) for line in info.splitlines() if line.startswith("flags:"))
                if executable == "codex" and flags & os.O_ACCMODE in (os.O_WRONLY, os.O_RDWR):
                    paths.add(path)
            except (OSError, StopIteration, ValueError):
                pass
    return paths


def stored_paths(codex_home, thread_id):
    """Read-only index lookup for an explicitly selected session and direct children."""
    databases = sorted(Path(codex_home).glob("state_*.sqlite"), key=lambda p: p.stat().st_mtime, reverse=True)
    if not databases:
        return []
    with closing(sqlite3.connect(databases[0].resolve().as_uri() + "?mode=ro", uri=True, timeout=0.2)) as conn:
        rows = conn.execute("""SELECT rollout_path FROM threads WHERE id=? OR
            CASE WHEN json_valid(source) THEN
            json_extract(source, '$.subagent.thread_spawn.parent_thread_id') END = ?""",
            (thread_id, thread_id)).fetchall()
    return [r[0] for r in rows]


class Tail:
    def __init__(self, path, meta=None):
        self.path = Path(path)
        self.meta = meta or metadata(path)
        self.offset = 0
        self.inode = None
        self.reset()

    def reset(self):
        self.model = "?"
        self.effort = "?"
        self.service_tier = None
        self.pricing_contexts = set()
        self.state = "sin datos"
        self.activity = "Esperando actividad registrada"
        self.result = ""
        self.started = None
        self.ended = None
        self.updated = None
        self.turn_id = None
        self.owner = self.meta.get("id")
        self.telemetry = Telemetry(self.meta.get("id"))
        self.history = deque(maxlen=150)
        self.transcript = Transcript()
        self.elapsed_completed = 0

    def item(self, item):
        kind = item.get("type", "").lower().replace("_", "")
        if kind == "commandexecution":
            cmd = item.get("command", "")
            executable = cmd[0] if isinstance(cmd, list) and cmd else "shell"
            self.activity = "Comando: " + clean(Path(executable).name) + " · " + clean(item.get("status", "en curso"))
            # Legacy one-line summary; the separate transcript retains arguments.
            paths = [p.get("path") for p in (item.get("parsed_cmd") or []) if isinstance(p, dict) and p.get("path")]
            if paths:
                self.activity += " · " + clean(", ".join(str(p) for p in paths[:3]))
        elif kind == "agentmessage":
            if item.get("channel") in ("analysis", "reasoning"):
                return
            if item.get("phase") not in (None, "commentary", "final", "final_answer"):
                return
            content = item.get("content", item.get("text", ""))
            if isinstance(content, list):
                content = " ".join(c.get("text", "") for c in content if isinstance(c, dict))
            if content:
                self.activity = clean(content)
                if item.get("phase") in ("final", "final_answer"):
                    self.result = clean(content, 900)
        elif kind in ("filechange", "mcpToolCall".lower(), "websearch"):
            self.activity = {"filechange": "Cambio de archivos", "mcptoolcall": "Herramienta MCP", "websearch": "Consulta web"}[kind]
        # This is only the legacy summary. Public inputs/outputs live in Transcript.

    def consume(self, record):
        kind, payload = record.get("type"), record.get("payload", {})
        if not isinstance(payload, dict):
            return
        if payload.get("channel") in ("analysis", "reasoning"):
            return
        own_id = self.meta.get("id")
        if kind == "session_meta":
            self.owner = payload.get("id")
            return
        if payload.get("thread_id") and payload["thread_id"] != own_id:
            return
        if kind == "event_msg" and payload.get("thread_id") == own_id:
            self.owner = own_id
        if self.owner != own_id:
            return  # Inherited fork history ends at an explicitly owned event.
        stamp = epoch(record.get("timestamp"))
        self.telemetry.consume(record)
        self.transcript.consume(record)
        before = self.activity
        if kind == "turn_context":
            self.model = payload.get("model", self.model)
            self.effort = payload.get("effort", self.effort)
            self.service_tier = payload.get("service_tier")
            self.pricing_contexts.add((self.model, self.service_tier))
        elif kind == "event_msg":
            event = payload.get("type")
            if event in ("task_started", "turn_started"):
                if self.state == "trabajando" and payload.get("turn_id") == self.turn_id:
                    return
                self.state, self.started, self.ended = "trabajando", stamp, None
                self.turn_id = payload.get("turn_id")
                self.result = ""
                self.activity = "Turno iniciado"
            elif event in ("task_complete", "turn_complete"):
                if self.state == "trabajando" and self.started is not None:
                    self.elapsed_completed += max(0, stamp - self.started)
                self.state, self.ended = "completado", stamp
                if payload.get("last_agent_message"):
                    self.result = clean(payload["last_agent_message"], 900)
            elif event in ("turn_aborted", "task_aborted"):
                if self.state == "trabajando" and self.started is not None:
                    self.elapsed_completed += max(0, stamp - self.started)
                self.state, self.ended = "interrumpido", stamp
            elif event in ("error", "turn_failed"):
                if self.state == "trabajando" and self.started is not None:
                    self.elapsed_completed += max(0, stamp - self.started)
                self.state, self.ended = "error", stamp
                self.activity = clean(payload.get("message", "Error registrado"))
            elif event in ("item_started", "item_completed"):
                # Ignore items inherited from another thread in forked history.
                if payload.get("thread_id", self.meta.get("id")) == self.meta.get("id"):
                    self.item(payload.get("item", {}))
            elif event == "agent_message":
                self.activity = clean(payload.get("message", ""))
            elif event == "exec_command_begin":
                self.item({"type": "CommandExecution", "command": payload.get("command", "")})
            else:
                return
            self.updated = stamp
        elif kind == "response_item":
            if payload.get("type") == "message" and payload.get("role") == "assistant":
                self.item({**payload, "type": "AgentMessage"})
                self.updated = stamp
            elif payload.get("type") in ("function_call", "custom_tool_call"):
                self.activity = "Herramienta: " + clean(payload.get("name", ""))
                self.updated = stamp
        if self.activity != before:
            self.history.append({"timestamp": stamp, "text": self.activity})

    def poll(self):
        try:
            stat = self.path.stat()
            if self.inode != stat.st_ino or stat.st_size < self.offset:
                self.offset = 0
                self.reset()
            self.inode = stat.st_ino
            with self.path.open("rb") as stream:
                stream.seek(self.offset)
                # Cap per-refresh work; leave partial lines for the next refresh.
                for _ in range(2500):
                    start = stream.tell()
                    line = stream.readline()
                    if not line or not line.endswith(b"\n"):
                        self.offset = start
                        break
                    self.offset = stream.tell()
                    try:
                        self.consume(json.loads(line))
                    except (ValueError, TypeError, AttributeError):
                        pass
        except OSError:
            self.state = "registro no disponible"

    def summary(self):
        spawn = self.meta.get("source", {})
        spawn = spawn.get("subagent", {}).get("thread_spawn", {}) if isinstance(spawn, dict) else {}
        label = (spawn.get("agent_path") or spawn.get("agent_role") or "principal").rsplit("/", 1)[-1]
        elapsed = self.elapsed_completed
        if self.started and self.ended is None:
            elapsed += max(0, time.time() - self.started)
        elapsed = int(elapsed)
        stale = self.state == "trabajando" and self.updated and time.time() - self.updated > 30
        return {"id": self.meta.get("id"), "task": label, "nickname": spawn.get("agent_nickname"),
                "model": self.model, "effort": self.effort, "state": self.state,
                "service_tier": self.service_tier, "mixed_pricing_context": len(self.pricing_contexts) > 1,
                "seconds": elapsed, "quiet": bool(stale), "activity": self.activity, "result": self.result,
                "history": list(self.history), "transcript": list(self.transcript.events),
                **self.telemetry.summary()}


class Monitor:
    def __init__(self, home, pid=None, thread=None):
        self.home, self.pid, self.root = home, pid, thread
        self.identity = process_identity(pid) if pid else None
        self.explicit = bool(thread)
        self.tails = {}
        self.notice = "Esperando sesión · envía tu primer mensaje en Codex"

    def refresh(self):
        alive = self.identity is not None and self.identity == process_identity(self.pid)
        paths = process_files(self.pid) if self.pid and alive else set()
        metas = {p: metadata(p) for p in paths}
        roots = [(p, m) for p, m in metas.items() if m.get("id") and not parent(m)]
        if not self.explicit and roots:
            # /new leaves the same process alive; choose its newest root, not cwd.
            _, newest = max(roots, key=lambda pair: pair[1].get("timestamp", ""))
            if self.root != newest["id"]:
                self.root, self.tails = newest["id"], {}
        if self.root:
            try:
                paths.update(stored_paths(self.home, self.root))
                self.notice = "Lectura local · sin consumo de tokens"
            except (sqlite3.Error, OSError):
                self.notice = "Índice no disponible; leyendo registros abiertos"
            # A moved/archived rollout can have two paths. Identity, not path,
            # is the accounting key; never count the same agent twice.
            candidates = {}
            for path in sorted(paths, key=lambda p: (p in metas, p)):
                meta = metas.get(path) or metadata(path)
                if meta.get("id") == self.root or parent(meta) == self.root:
                    candidates[meta["id"]] = (path, meta)
            for identity, (path, meta) in candidates.items():
                if identity not in self.tails or self.tails[identity].path != Path(path):
                    self.tails[identity] = Tail(path, meta)
            for tail in self.tails.values():
                tail.poll()
        if self.pid and not alive:
            self.notice = "Codex terminó · q para cerrar el panel"
            for tail in self.tails.values():
                if tail.state == "trabajando":
                    tail.ended = time.time()
                    if tail.started is not None:
                        tail.elapsed_completed += max(0, tail.ended - tail.started)
                    tail.state = "sin confirmar (Codex cerrado)"
        return self.snapshot()

    def snapshot(self):
        agents = [t.summary() for t in self.tails.values() if parent(t.meta) == self.root]
        agents.sort(key=lambda a: (a["state"] != "trabajando", a["task"]))
        root = next((t.summary() for t in self.tails.values() if t.meta.get("id") == self.root), None)
        members = ([root] if root else []) + agents
        complete = bool(root) and all(a.get("tokens") is not None for a in members)
        total = sum(a["tokens"] for a in members) if complete else None
        for agent in members:
            agent["token_share"] = 100 * agent["tokens"] / total if total else None
            agent["quota_share"] = None  # No supported per-thread quota attribution.
        assign_estimates(members, has_main=bool(root))
        return {"thread": self.root, "notice": self.notice, "principal": root, "agents": agents,
                "total_tokens": total}


def lines(snapshot, width):
    result = ["CODEX · SUBAGENTES", snapshot["notice"], ""]
    if snapshot["thread"]:
        result += ["Sesión: " + snapshot["thread"][:13]]
    root = snapshot.get("principal")
    if root:
        result += ["Principal: " + root["state"]]
    agents = snapshot["agents"]
    result += [f"{sum(a['state'] == 'trabajando' for a in agents)} activos · {len(agents)} en esta sesión", ""]
    if not agents:
        result += ["Sin subagentes registrados todavía.", "Aparecerán al delegar trabajo."]
    for agent in agents:
        result += ["─" * max(1, width), agent["task"] + (" · " + agent["nickname"] if agent["nickname"] else ""),
                   f"{agent['state']} · {agent['seconds']}s", f"{agent['model']} / {agent['effort']}"]
        if agent["quiet"]:
            result += ["Sin novedades >30s; no implica bloqueo"]
        result += [clean(agent["activity"], 160)]
        if agent["result"] and agent["result"] != agent["activity"]:
            result += ["Resultado: " + clean(agent["result"], 200)]
        result += [""]
    wrapped = []
    for line in result:
        wrapped.extend(textwrap.wrap(clean(line, 1000), max(10, width), replace_whitespace=True) or [""])
    return wrapped


def watch(args):
    parser = argparse.ArgumentParser(description="Monitor Codex subagents (read-only)")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--pid", type=int)
    group.add_argument("--thread")
    group.add_argument("--runtime", type=Path)
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--profile", default="personal")
    parser.add_argument("--detail", help="Open the live activity view for this agent ID")
    parser.add_argument("--owner-pid", type=int, help=argparse.SUPPRESS)
    options = parser.parse_args(args)
    pid = options.pid or options.owner_pid
    if options.runtime:
        for _ in range(100):
            try:
                pid = int((options.runtime / "pid").read_text())
                break
            except (OSError, ValueError):
                time.sleep(0.1)
        if not pid:
            raise RuntimeError("Codex no arrancó; revisa el panel izquierdo")
    monitor = Monitor(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")), pid, options.thread)
    if options.once:
        print(json.dumps(monitor.refresh(), ensure_ascii=False, indent=2))
    else:
        from panel_ui import run
        quota = None
        # Only the live sidebar polls account data, not detail popups or --once.
        # Offline/explicit-history viewers do not make account requests.
        if options.runtime and not options.detail and not os.environ.get("CODEX_PANEL_QUOTA_OFFLINE"):
            from account_quota import AccountQuota
            quota = AccountQuota(monitor.home, options.profile)
        options.quota = quota
        previous_signals = {}
        if quota is not None:
            def stop_watch(signum, frame):
                raise SystemExit(0)
            for signum in (signal.SIGTERM, signal.SIGHUP):
                previous_signals[signum] = signal.signal(signum, stop_watch)
        try:
            curses.wrapper(run, monitor, options)
        finally:
            if quota is not None:
                quota.close()
            for signum, handler in previous_signals.items():
                signal.signal(signum, handler)


def tmux_binary():
    override = os.environ.get("CODEX_PANEL_TMUX")
    for candidate in [override, "/usr/bin/tmux", str(Path(SCRIPT).parent / "tmux/bin/tmux"), shutil.which("tmux")]:
        if candidate and os.access(candidate, os.X_OK):
            result = subprocess.run([candidate, "-V"], capture_output=True, text=True)
            if result.returncode == 0 and "shim" not in result.stdout.lower():
                return candidate
    raise RuntimeError("Se necesita tmux real (el shim herdr no basta). Usa CODEX_PANEL_TMUX=/ruta/tmux")


def _write_exit_status(runtime, status):
    """Publish the child status atomically before the isolated server is removed."""
    temporary = runtime / "exit.json.tmp"
    temporary.write_text(json.dumps({"status": status}))
    temporary.replace(runtime / "exit.json")


def _run(runtime):
    """Run Codex, then tear down this launch's private tmux server."""
    runtime = Path(runtime)
    command = json.loads((runtime / "launch.json").read_text())
    (runtime / "launch.json").unlink()
    (runtime / "pid").write_text(str(os.getpid()))
    supervisor = json.loads((runtime / "supervisor.json").read_text())

    def ignore_sigint(signum, frame):
        pass
    previous_sigint = signal.signal(signal.SIGINT, ignore_sigint)
    status = 127
    try:
        try:
            child = subprocess.Popen(command)
            while True:
                try:
                    returncode = child.wait()
                    break
                except KeyboardInterrupt:
                    # Keep waiting if a test or an older signal path raises;
                    # the installed handler makes real Ctrl-C a no-op here.
                    continue
        except OSError:
            returncode = 127
        status = returncode if returncode >= 0 else 128 - returncode
        _write_exit_status(runtime, status)
    finally:
        # Publish first, then tear down only this launch's private server.
        subprocess.run([supervisor["tmux"], "-L", supervisor["session"],
                        "kill-session", "-t", supervisor["session"]],
                       capture_output=True)
        signal.signal(signal.SIGINT, previous_sigint)
    return status


def _read_exit_status(runtime):
    try:
        status = json.loads((Path(runtime) / "exit.json").read_text())["status"]
        return status if isinstance(status, int) and not isinstance(status, bool) else None
    except (OSError, ValueError, TypeError, KeyError):
        return None


def launch(args):
    parser = argparse.ArgumentParser(description="Codex CLI + panel lateral local de subagentes")
    parser.add_argument("--profile", "-p", default="personal")
    parser.add_argument("--cwd", "-C", default=os.getcwd())
    parser.add_argument("--width", type=int, default=48)
    parser.add_argument("--detached", action="store_true", help="Crear la sesión sin adjuntar el terminal")
    parser.add_argument("--native", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("codex_args", nargs=argparse.REMAINDER, help="Opciones adicionales después de --")
    options = parser.parse_args(args)
    cwd = str(Path(options.cwd).expanduser().resolve(strict=True))
    extra = options.codex_args
    if extra[:1] == ["--"]:
        extra = extra[1:]
    if not options.native and any(a == "--ephemeral" or a.startswith("--remote") for a in extra):
        parser.error("El panel requiere una sesión local persistida (sin --ephemeral ni --remote)")
    if not 28 <= options.width <= 100:
        parser.error("--width debe estar entre 28 y 100 columnas")
    codex = os.environ.get("CODEX_PANEL_REAL_CODEX") or shutil.which("codex")
    if not codex:
        raise RuntimeError("No se encontró codex en PATH")
    tmux = tmux_binary()
    if not options.detached and not sys.stdin.isatty():
        raise RuntimeError("Ejecuta codex-panel desde un terminal interactivo")
    runtime = Path(tempfile.mkdtemp(prefix="codex-panel-", dir=os.environ.get("XDG_RUNTIME_DIR") or None))
    # Native entry preserves argv exactly: profile/cwd were already supplied
    # by the user, and absence of --profile must keep the CLI's default config.
    command = [codex, *extra] if options.native else [codex, "--profile", options.profile, *extra]
    os.environ["CODEX_PANEL_ACTIVE"] = "1"
    (runtime / "launch.json").write_text(json.dumps(command))
    session = "codex-" + runtime.name.removeprefix("codex-panel-")
    prefix = [tmux, "-L", session]
    (runtime / "supervisor.json").write_text(json.dumps({"tmux": tmux, "session": session}))
    # One server per launch preserves its exact account/config/PATH environment.
    def tm(*parts):
        return subprocess.check_output([*prefix, *parts], text=True, stderr=subprocess.PIPE).strip()
    runner = shlex.join([sys.executable, SCRIPT, "_run", str(runtime)])
    from panel_control import ui_command
    side = ui_command([sys.executable, SCRIPT, "watch", "--runtime", str(runtime), "--profile", options.profile])
    size = shutil.get_terminal_size((160, 40))
    try:
        left = tm("-f", "/dev/null", "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session,
                  "-x", str(max(100, size.columns)), "-y", str(max(24, size.lines)), "-c", cwd, runner)
        # Window/session scoped options. No global bindings or shell aliases.
        tm("set-option", "-t", session, "mouse", "on")
        tm("set-option", "-t", session, "status", "off")
        tm("set-option", "-t", session, "status-right", "Ctrl-S sidebar | Ctrl-b arrows: focus | Ctrl-b d: detach")
        tm("set-option", "-t", session, "status-right-length", "85")
        right_width = min(options.width, max(28, size.columns // 3))
        tm("set-option", "-s", "terminal-features", "*:RGB")
        tm("set-option", "-s", "extended-keys", "on")
        # This server was just created exclusively for this launch; never alter
        # escape-time on an existing/shared tmux server.
        tm("set-option", "-s", "escape-time", "10")
        right = tm("split-window", "-h", "-d", "-P", "-F", "#{pane_id}", "-l", str(right_width), "-t", left, "-c", cwd,
                   side)
        from panel_control import install
        install(tmux, session, runtime, left, right, right_width, options.profile)
        tm("select-pane", "-t", left)
    except (subprocess.CalledProcessError, OSError):
        subprocess.run([*prefix, "kill-session", "-t", session], capture_output=True)
        raise
    if options.detached:
        print(json.dumps({"session": session, "socket": session, "runtime": str(runtime), "tmux": tmux}))
    else:
        # TMUX from another server must not prevent attaching our isolated server.
        env = dict(os.environ)
        env.pop("TMUX", None)
        attached = subprocess.run([*prefix, "attach-session", "-t", session], env=env)
        status = _read_exit_status(runtime)
        if status is not None:
            return status
        if attached.returncode:
            raise subprocess.CalledProcessError(attached.returncode, attached.args)
        print("Para volver: " + shlex.join([*prefix, "attach-session", "-t", session]))
    return 0


def main():
    try:
        if len(sys.argv) > 1 and sys.argv[1] == "_run":
            return _run(Path(sys.argv[2]))
        elif len(sys.argv) > 1 and sys.argv[1] == "watch":
            watch(sys.argv[2:])
        elif len(sys.argv) > 1 and sys.argv[1] == "toggle":
            from panel_control import toggle
            toggle(Path(sys.argv[2]))
        else:
            result = launch(sys.argv[1:])
            return 0 if result is None else result
    except KeyboardInterrupt:
        return 130
    except (OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        print("codex-panel: " + str(exc), file=sys.stderr)
        if isinstance(exc, subprocess.CalledProcessError) and exc.stderr:
            print(exc.stderr, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
