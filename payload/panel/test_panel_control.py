import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import uuid
import fcntl
import pty
import struct
import termios
import threading
import time
import shlex
import sys
from collections import deque

from panel_control import install, toggle, tmux, ui_command, sync_style, style_color


class TmuxTests(unittest.TestCase):
    def setUp(self):
        from codex_panel import tmux_binary
        try:
            self.binary = tmux_binary()
        except RuntimeError:
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")
        self.tmp = tempfile.TemporaryDirectory(prefix="codex-panel-test-")
        self.addCleanup(self.tmp.cleanup)
        self.runtime = Path(self.tmp.name)
        self.session = "codex-panel-test-" + uuid.uuid4().hex[:10]
        self.state = {"tmux": self.binary, "session": self.session}
        self.addCleanup(lambda: subprocess.run([self.binary, "-L", self.session, "kill-server"], capture_output=True))
        self.left = tmux(self.state, "-f", "/dev/null", "new-session", "-d", "-P", "-F", "#{pane_id}",
                         "-s", self.session, "-x", "140", "-y", "40", "sleep 120")
        # Mirror the isolated server created by launch(), not tmux's 500ms default.
        tmux(self.state, "set-option", "-s", "escape-time", "10")
        self.right = tmux(self.state, "split-window", "-h", "-d", "-P", "-F", "#{pane_id}",
                          "-t", self.left, "sleep 120")
        self.state = install(self.binary, self.session, self.runtime, self.left, self.right, 48, "personal")

    def test_hide_show_keeps_both_processes_and_selection(self):
        pid = tmux(self.state, "display-message", "-p", "-t", self.right, "#{pane_pid}")
        left_pid = tmux(self.state, "display-message", "-p", "-t", self.left, "#{pane_pid}")
        for _ in range(3):
            toggle(self.runtime)
            left_window = tmux(self.state, "display-message", "-p", "-t", self.left, "#{window_id}")
            right_window = tmux(self.state, "display-message", "-p", "-t", self.right, "#{window_id}")
            self.assertNotEqual(left_window, right_window)
            toggle(self.runtime)
            self.assertEqual(left_window, tmux(self.state, "display-message", "-p", "-t", self.right, "#{window_id}"))
        self.assertEqual(pid, tmux(self.state, "display-message", "-p", "-t", self.right, "#{pane_pid}"))
        self.assertEqual(left_pid, tmux(self.state, "display-message", "-p", "-t", self.left, "#{pane_pid}"))

    def test_shortcut_is_conditional_on_our_panes(self):
        binding = "\n".join(line for line in tmux(self.state, "list-keys", "-T", "root").splitlines() if "C-s" in line)
        self.assertIn("pane_id", binding)
        self.assertIn(self.left, binding)
        self.assertIn(self.right, binding)
        self.assertIn("send-keys C-s", binding)
        self.assertNotIn("kill-pane", binding)

    def test_styles_are_scoped_and_never_use_syntax_background(self):
        other = tmux(self.state, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "unrelated", "sleep 120")
        tmux(self.state, "set-option", "-t", "unrelated", "status-style", "bg=red")
        tmux(self.state, "set-option", "-p", "-t", other, "window-style", "bg=blue")
        tmux(self.state, "set-option", "-p", "-t", self.left, "window-style", "bg=green")
        look = {"colors": {"fg": "#eeeeee", "bg": "#112233", "accent": "#ff0000", "muted": "#777777"}}
        sync_style(self.runtime, look)
        self.assertEqual(style_color(look, "bg"), "default")
        self.assertEqual(tmux(self.state, "show-options", "-pv", "-t", self.right, "window-style"), "fg=default,bg=default")
        self.assertEqual(tmux(self.state, "show-options", "-pv", "-t", self.left, "window-style"), "bg=green")
        self.assertEqual(tmux(self.state, "show-options", "-pv", "-t", other, "window-style"), "bg=blue")
        self.assertEqual(tmux(self.state, "show-options", "-v", "-t", "unrelated", "status-style"), "bg=red")

    def test_live_ui_popup_and_keyboard_hide(self):
        """Real curses/tmux/PTY smoke test using existing read-only fixture history."""
        root = "01a0691a-eaf4-7290-9f43-f92cced63291"
        if not list((Path.home() / ".codex/sessions/2026/09/03").glob("*" + root + ".jsonl")):
            self.skipTest("scoped historical fixture not available")
        module_dir = str(Path(__file__).parent)
        leftpid = tmux(self.state, "display-message", "-p", "-t", self.left, "#{pane_pid}")
        (self.runtime / "pid").write_text(leftpid)
        source = ("import sys,curses;from pathlib import Path;from types import SimpleNamespace;"
                  f"sys.path.insert(0,{module_dir!r});from codex_panel import Monitor;from panel_ui import run;"
                  f"curses.wrapper(run,Monitor(str(Path.home()/'.codex'),thread={root!r}),"
                  f"SimpleNamespace(runtime=Path({str(self.runtime)!r}),profile='personal',detail=None))")
        tmux(self.state, "respawn-pane", "-k", "-t", self.right,
             ui_command([sys.executable, "-c", source]))
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
        client = subprocess.Popen([self.binary, "-L", self.session, "attach", "-t", self.session],
                                  stdin=slave, stdout=slave, stderr=slave, env={**os.environ, "TERM": "xterm-256color"})
        os.close(slave)
        received = deque(maxlen=256)
        def drain():
            pending = b""
            # Answer tmux's terminal probes. A silent fake terminal leaves queries
            # pending and tmux intentionally raises escape-time to 500 ms.
            replies = {
                b"\x1b[c": b"\x1b[?1;2c",
                b"\x1b[>c": b"\x1b[>0;136;0c",
                b"\x1b[>q": b"\x1bP>|XTerm(136)\x1b\\",
                b"\x1b]10;?\x1b\\": b"\x1b]10;rgb:eeee/eeee/eeee\x1b\\",
                b"\x1b]11;?\x1b\\": b"\x1b]11;rgb:1111/1111/1111\x1b\\",
            }
            try:
                while True:
                    chunk = os.read(master, 65536)
                    if not chunk: break
                    received.append(chunk)
                    pending += chunk
                    for query, reply in replies.items():
                        if query in pending:
                            os.write(master, reply)
                            pending = pending.replace(query, b"")
                    pending = pending[-32:]
            except OSError: pass
        reader = threading.Thread(target=drain, daemon=True)
        reader.start()
        def cleanup():
            client.terminate()
            client.wait(timeout=5)
            os.close(master)
        self.addCleanup(cleanup)
        def until(check):
            deadline = time.monotonic() + 8
            while time.monotonic() < deadline:
                if check(): return
                time.sleep(0.1)
            self.fail("PTY UI check timed out")
        def pane(): return tmux(self.state, "capture-pane", "-p", "-t", self.right)
        until(lambda: "Archimedes" in pane())
        self.assertNotIn("Main → Archimedes", pane())
        self.assertNotIn("Archimedes → Main", pane())
        sidebar_pid = tmux(self.state, "display-message", "-p", "-t", self.right, "#{pane_pid}")
        tmux(self.state, "select-pane", "-t", self.right)
        # Send the real keyboard shortcut to the attached client, not the toggle helper.
        os.write(master, b"\x13")
        until(lambda: tmux(self.state, "display-message", "-p", "-t", self.right, "#{window_name}") == "__subagents_hidden")
        os.write(master, b"\x13")
        until(lambda: tmux(self.state, "display-message", "-p", "-t", self.right, "#{window_name}") != "__subagents_hidden")
        # xterm's application-mode down arrow selects second agent, Enter opens popup.
        os.write(master, b"\x1bOB\r")
        def popup_processes():
            found = []
            for proc in Path('/proc').glob('[0-9]*/cmdline'):
                try: args = proc.read_bytes().split(b'\0')
                except OSError: continue
                if b'--detail' in args and b'--owner-pid' in args and leftpid.encode() in args:
                    found.append(args)
            return found
        until(lambda: bool(popup_processes()))
        self.assertTrue(any(b"01a0691b-25f5-75a1-8ec3-a486b7c0ed91" in p for p in popup_processes()))
        until(lambda: b"Following" in b"".join(received))
        self.assertNotIn(b"Traceback", b"".join(received))
        # Home reaches the original commands, not a generated 'Herramienta' summary.
        os.write(master, b"\x1bOH")
        until(lambda: b"function_call" in b"".join(received) or b"custom_tool_call" in b"".join(received))
        self.assertNotIn(b"Token [oculto]", b"".join(received))
        # Esc and q are ignored by this popup, not handled as close shortcuts.
        os.write(master, b"\x1b")
        time.sleep(0.15)
        self.assertTrue(popup_processes(), "Esc must leave detail open")
        os.write(master, b"q")
        time.sleep(0.15)
        self.assertTrue(popup_processes(), "q must leave detail open")
        started = time.monotonic()
        os.write(master, b"\r")
        until(lambda: not popup_processes())
        enter_seconds = time.monotonic() - started
        self.assertLess(enter_seconds, 0.5)
        self.assertEqual(sidebar_pid, tmux(self.state, "display-message", "-p", "-t", self.right, "#{pane_pid}"))


if __name__ == "__main__": unittest.main()
