"""Read-only Codex account quota cache (stdlib, POSIX stdio).

Protocol: https://learn.chatgpt.com/docs/app-server
AccountQuota(home, profile).snapshot() starts one lazy worker and returns a copy
without waiting for RPC. Polls are separated by 30 seconds; each probe owns a
short-lived app-server, with an 8-second transport deadline and 1 MiB limit.
Call close() on UI exit, or use a context manager. No UI/estimation logic lives
here. updated_at is the last usable read's Unix time; status is ok/stale/unknown.
Null fields stay None; unavailable reads never imply zero usage. autostart=False
disables implicit startup; start() enables it explicitly. An injected fetch()
returns a raw RPC result and must itself be bounded/cooperative (test use only).
"""

import atexit
import copy
import json
import math
import os
import select
import signal
import subprocess
import threading
import time


def _number(value):
    return value if type(value) in (int, float) and math.isfinite(value) else None


def _windows(result):
    buckets = result.get("rateLimitsByLimitId")
    bucket = (buckets.get("codex") if isinstance(buckets, dict)
              else result.get("rateLimits") if buckets is None else None)
    if not isinstance(bucket, dict) or bucket.get("limitId") not in (None, "codex"):
        raise ValueError
    windows = []
    for name in ("primary", "secondary"):
        window = bucket.get(name)
        if not isinstance(window, dict):
            continue
        used = _number(window.get("usedPercent"))
        windows.append({
            "name": name, "used_percent": used,
            "remaining_percent": None if used is None else max(0, min(100, 100 - used)),
            "window_minutes": _number(window.get("windowDurationMins")),
            "resets_at": _number(window.get("resetsAt")),
        })
    if not any(w["used_percent"] is not None for w in windows):
        raise ValueError
    return windows


class AccountQuota:
    POLL_SECONDS = 30.0
    TIMEOUT_SECONDS = 8.0
    MAX_BYTES = 1024 * 1024

    def __init__(self, home, profile=None, *, autostart=True, fetch=None):
        self._home = os.fspath(home)
        if not self._home:
            raise ValueError("home must be explicit")
        self._profile = profile
        self._autostart = autostart
        self._fetch = fetch if fetch is not None else self._probe
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._thread = None
        self._state = {"windows": [], "updated_at": None, "status": "unknown"}

    def start(self):
        """Start at most one worker; closed instances cannot restart."""
        with self._lock:
            if self._thread is None and not self._stop.is_set():
                self._thread = threading.Thread(target=self._run, name="account-quota",
                                                daemon=True)
                atexit.register(self.close)
                self._thread.start()

    def snapshot(self):
        if self._autostart:
            self.start()
        with self._lock:
            return copy.deepcopy(self._state)

    def _refresh(self):
        try:
            windows = _windows(self._fetch())
            state = {"windows": windows, "updated_at": time.time(), "status": "ok"}
        except Exception:
            # Never retain/log service errors, stderr, credentials, or raw payloads.
            with self._lock:
                state = dict(self._state, status=("stale" if self._state["updated_at"]
                                                 is not None else "unknown"),
                             error="quota_unavailable")
        with self._lock:
            if not self._stop.is_set():
                self._state = state

    def _run(self):
        while not self._stop.is_set():
            self._refresh()
            if self._stop.wait(self.POLL_SECONDS):
                break

    def _ready(self, fd, writing, deadline):
        while True:
            remaining = deadline - time.monotonic()
            if self._stop.is_set() or remaining <= 0:
                raise TimeoutError
            readable, writable, _ = select.select(
                [] if writing else [fd], [fd] if writing else [], [], min(.05, remaining))
            if readable or writable:
                return

    def _exchange(self, proc, packet, expected, deadline):
        data = memoryview((json.dumps(packet) + "\n").encode())
        while data:
            self._ready(proc.stdin, True, deadline)
            try:
                count = os.write(proc.stdin.fileno(), data)
            except BlockingIOError:
                continue
            if not count:
                raise EOFError
            data = data[count:]
        if expected is None:
            return
        buffer, received = b"", 0
        while True:
            self._ready(proc.stdout, False, deadline)
            try:
                chunk = os.read(proc.stdout.fileno(), 65536)
            except BlockingIOError:
                continue
            if not chunk:
                raise EOFError
            received += len(chunk)
            if received > self.MAX_BYTES:
                raise ValueError
            buffer += chunk
            while b"\n" in buffer:
                if self._stop.is_set() or time.monotonic() >= deadline:
                    raise TimeoutError
                line, buffer = buffer.split(b"\n", 1)
                reply = json.loads(line)
                if not isinstance(reply, dict):
                    raise ValueError
                if "method" in reply or reply.get("id") != expected:
                    continue
                if "error" in reply or not isinstance(reply.get("result"), dict):
                    raise ValueError
                return reply["result"]

    def _probe(self):
        env = os.environ.copy()
        env["CODEX_HOME"] = self._home
        # CLI 0.153.0 rejects --profile for app-server. Account auth belongs to
        # CODEX_HOME (shared by personal/work), not an agent/model profile.
        command = ["codex", "app-server", "--listen", "stdio://"]
        deadline = time.monotonic() + self.TIMEOUT_SECONDS
        proc = subprocess.Popen(command, env=env, stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                bufsize=0, start_new_session=True)
        try:
            os.set_blocking(proc.stdin.fileno(), False)
            os.set_blocking(proc.stdout.fileno(), False)
            self._exchange(proc, {"id": 1, "method": "initialize", "params": {
                "clientInfo": {"name": "codex_panel_quota", "version": "1.0"}}},
                1, deadline)
            self._exchange(proc, {"method": "initialized"}, None, deadline)
            return self._exchange(proc, {"id": 2, "method": "account/rateLimits/read"},
                                  2, deadline)
        finally:
            # The session is private to this probe; also stop any helper children.
            for sig in (signal.SIGTERM, signal.SIGKILL):
                try:
                    os.killpg(proc.pid, sig)
                except ProcessLookupError:
                    pass
                if sig == signal.SIGTERM:
                    try:
                        proc.wait(timeout=.2)
                    except subprocess.TimeoutExpired:
                        pass
            try:
                proc.wait(timeout=.2)
            finally:
                proc.stdin.close()
                proc.stdout.close()

    def close(self):
        """Stop polling and reap the private subprocess; bounded, idempotent."""
        with self._lock:
            self._stop.set()
            thread = self._thread
        if thread is not None and thread is not threading.current_thread():
            thread.join(timeout=1.0)
        atexit.unregister(self.close)

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()
