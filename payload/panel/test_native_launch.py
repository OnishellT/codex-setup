"""No-model tests of the native command bridge and exact argv preservation."""
import contextlib
import io
import json
import os
import pty
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

from codex_panel import _run, launch


class NativeLaunchTests(unittest.TestCase):
    def run_launch(self, native, profile, argv):
        with tempfile.TemporaryDirectory(prefix="codex-native-test-") as folder:
            calls = []

            def tm(command, **kwargs):
                calls.append(command)
                return "%0\n" if "new-session" in command else "%1\n" if "split-window" in command else ""

            options = ["--detached", "--profile", profile]
            if native:
                options.append("--native")
            options += ["--", *argv]
            with patch.dict(os.environ, {"CODEX_PANEL_REAL_CODEX": "/official/codex"}), \
                 patch("codex_panel.tmux_binary", return_value="/official/tmux"), \
                 patch("codex_panel.tempfile.mkdtemp", return_value=folder), \
                 patch("subprocess.check_output", side_effect=tm), \
                 contextlib.redirect_stdout(io.StringIO()):
                launch(options)
                command = json.loads((Path(folder) / "launch.json").read_text())
                state = json.loads((Path(folder) / "control.json").read_text())
                self.assertEqual(os.environ["CODEX_PANEL_ACTIVE"], "1")
            return command, state, calls

    def test_native_no_implicit_profile(self):
        command, state, _ = self.run_launch(True, "", ["--yolo"])
        self.assertEqual(command, ["/official/codex", "--yolo"])
        self.assertEqual(state["profile"], "")

    def test_native_profile_and_relative_cwd_are_not_rewritten(self):
        argv = ["-p", "work", "-C", "relative dir", "--yolo", "prompt with spaces", ""]
        command, state, calls = self.run_launch(True, "work", argv)
        self.assertEqual(command, ["/official/codex", *argv])
        self.assertEqual(state["profile"], "work")
        new_session = next(call for call in calls if "new-session" in call)
        self.assertEqual(new_session[new_session.index("-c") + 1], str(Path.cwd()))
        session = new_session[new_session.index("-s") + 1]
        self.assertTrue(any(call[-5:] == ["set-option", "-t", session, "status", "off"] for call in calls))

    def test_native_server_enables_extended_keys(self):
        _, _, calls = self.run_launch(True, "", ["--yolo"])
        self.assertTrue(any(call[-4:] == ["set-option", "-s", "extended-keys", "on"] for call in calls))
        self.assertFalse(any("extended-keys-format" in call for call in calls))
        self.assertNotIn("*:extkeys", [part for call in calls for part in call])

    def test_native_resume_and_prompt_that_looks_like_remote(self):
        argv = ["resume", "--last", "--", "--remote-is-prompt"]
        command, _, _ = self.run_launch(True, "", argv)
        self.assertEqual(command, ["/official/codex", *argv])

    def test_explicit_panel_still_injects_requested_profile(self):
        command, _, _ = self.run_launch(False, "personal", ["--yolo"])
        self.assertEqual(command, ["/official/codex", "--profile", "personal", "--yolo"])

    def test_supervisor_waits_after_sigint_and_kills_only_its_server(self):
        with tempfile.TemporaryDirectory(prefix="codex-supervisor-test-") as folder:
            runtime = Path(folder)
            (runtime / "launch.json").write_text(json.dumps(["/official/codex"]))
            (runtime / "supervisor.json").write_text(json.dumps({"tmux": "/official/tmux", "session": "codex-test"}))

            class Child:
                def __init__(self):
                    self.waits = 0

                def wait(self):
                    self.waits += 1
                    if self.waits == 1:
                        raise KeyboardInterrupt
                    return -signal.SIGTERM

            child = Child()
            with patch("codex_panel.subprocess.Popen", return_value=child) as spawn, \
                 patch("codex_panel.subprocess.run") as run:
                self.assertEqual(_run(runtime), 128 + signal.SIGTERM)

            spawn.assert_called_once_with(["/official/codex"])
            self.assertEqual(child.waits, 2)
            self.assertEqual(json.loads((runtime / "exit.json").read_text())["status"], 128 + signal.SIGTERM)
            self.assertEqual(run.call_args.args[0], ["/official/tmux", "-L", "codex-test", "kill-session", "-t", "codex-test"])
            self.assertFalse((runtime / "launch.json").exists())

    def test_supervisor_signal_during_publish_still_kills_after_state(self):
        with tempfile.TemporaryDirectory(prefix="codex-signal-test-") as folder:
            runtime = Path(folder)
            (runtime / "launch.json").write_text(json.dumps(["/official/codex"]))
            (runtime / "supervisor.json").write_text(json.dumps({"tmux": "/official/tmux", "session": "codex-test"}))

            class Child:
                def wait(self):
                    os.kill(os.getpid(), signal.SIGINT)
                    return 23

            def publish(path, status):
                os.kill(os.getpid(), signal.SIGINT)
                path.joinpath("exit.json").write_text(json.dumps({"status": status}))

            previous = signal.getsignal(signal.SIGINT)
            with patch("codex_panel.subprocess.Popen", return_value=Child()), \
                 patch("codex_panel._write_exit_status", side_effect=publish), \
                 patch("codex_panel.subprocess.run") as run:
                self.assertEqual(_run(runtime), 23)

            self.assertEqual(signal.getsignal(signal.SIGINT), previous)
            self.assertEqual(json.loads((runtime / "exit.json").read_text())["status"], 23)
            self.assertEqual(run.call_args.args[0], ["/official/tmux", "-L", "codex-test", "kill-session", "-t", "codex-test"])

    def test_attach_server_disappearance_returns_codex_status(self):
        with tempfile.TemporaryDirectory(prefix="codex-attach-test-") as folder:
            calls = []

            def tm(command, **kwargs):
                calls.append(command)
                return "%0\n" if "new-session" in command else "%1\n" if "split-window" in command else ""

            options = ["--profile", "personal", "--native", "--", "--yolo"]
            for attach_status in (0, 1):
                def attach(command, **kwargs):
                    (Path(folder) / "exit.json").write_text(json.dumps({"status": 7}))
                    return subprocess.CompletedProcess(command, attach_status)

                output = io.StringIO()
                with patch.dict(os.environ, {"CODEX_PANEL_REAL_CODEX": "/official/codex"}), \
                     patch("codex_panel.tmux_binary", return_value="/official/tmux"), \
                     patch("codex_panel.tempfile.mkdtemp", return_value=folder), \
                     patch("subprocess.check_output", side_effect=tm), \
                     patch("subprocess.run", side_effect=attach), \
                     patch.object(sys.stdin, "isatty", return_value=True), \
                     contextlib.redirect_stdout(output):
                    self.assertEqual(launch(options), 7)
                self.assertNotIn("Para volver:", output.getvalue())

    def test_real_tmux_ctrl_c_and_detach_lifecycle(self):
        tmux = os.environ.get("CODEX_PANEL_TMUX")
        if not tmux:
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")
        version = subprocess.run([tmux, "-V"], capture_output=True, text=True)
        if version.returncode or "shim" in version.stdout.lower():
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")

        env = os.environ.copy()
        env.update(CODEX_PANEL_TMUX=tmux, CODEX_PANEL_REAL_CODEX=sys.executable,
                   CODEX_PANEL_QUOTA_OFFLINE="1")
        code = "import signal,time; signal.signal(signal.SIGINT, lambda *_: None); time.sleep(2)"
        created = subprocess.run(
            [sys.executable, "codex_panel.py", "--native", "--detached", "--", "-c", code],
            cwd=Path(__file__).parent, env=env, text=True, capture_output=True, check=True,
        )
        info = json.loads(created.stdout)
        probe = [tmux, "-L", info["socket"], "has-session", "-t", info["session"]]
        master, slave = pty.openpty()
        client = None
        try:
            client = subprocess.Popen(
                [tmux, "-L", info["socket"], "attach-session", "-t", info["session"]],
                stdin=slave, stdout=slave, stderr=slave, env={**env, "TERM": "xterm"},
            )
            os.close(slave)
            time.sleep(0.35)
            os.write(master, b"\x03")
            time.sleep(0.25)
            self.assertEqual(subprocess.run(probe, capture_output=True).returncode, 0)
            os.write(master, b"\x02d")
            client.wait(timeout=3)
            self.assertEqual(client.returncode, 0)
            self.assertEqual(subprocess.run(probe, capture_output=True).returncode, 0)
            time.sleep(2)
            self.assertNotEqual(subprocess.run(probe, capture_output=True).returncode, 0)
        finally:
            if client is not None and client.poll() is None:
                try:
                    os.write(master, b"\x02d")
                    client.wait(timeout=2)
                except (OSError, subprocess.TimeoutExpired):
                    client.kill()
            os.close(master)
            subprocess.run([tmux, "-L", info["socket"], "kill-server"], capture_output=True)

    def test_real_tmux_forwards_shift_enter_as_csi_u(self):
        tmux = os.environ.get("CODEX_PANEL_TMUX")
        if not tmux:
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")
        version = subprocess.run([tmux, "-V"], capture_output=True, text=True)
        if version.returncode or "shim" in version.stdout.lower():
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")

        with tempfile.TemporaryDirectory(prefix="codex-keys-runtime-") as runtime_root:
            runtime = Path(runtime_root)
            ready = runtime / "ready"
            received = runtime / "received"
            env = os.environ.copy()
            env.update(CODEX_PANEL_TMUX=tmux, CODEX_PANEL_REAL_CODEX=sys.executable,
                       CODEX_PANEL_QUOTA_OFFLINE="1", XDG_RUNTIME_DIR=runtime_root,
                       TERM="xterm-256color")
            env.pop("TMUX", None)
            code = (
                "import os,tty; tty.setraw(0); "
                f"open({str(ready)!r}, 'wb').write(b'1'); "
                "os.write(1,b'\\x1b[>4;1m'); data=b''\n"
                "while len(data)<8: data += os.read(0,8-len(data))\n"
                f"open({str(received)!r}, 'wb').write(data)"
            )
            created = subprocess.run(
                [sys.executable, "codex_panel.py", "--native", "--detached", "--", "-c", code],
                cwd=Path(__file__).parent, env=env, text=True, capture_output=True, check=True,
            )
            info = json.loads(created.stdout)
            master, slave = pty.openpty()
            client = subprocess.Popen(
                [tmux, "-L", info["socket"], "attach-session", "-t", info["session"]],
                stdin=slave, stdout=slave, stderr=slave, env={**env, "TERM": "xterm"},
            )
            os.close(slave)
            try:
                deadline = time.monotonic() + 3
                while not ready.exists() and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(ready.exists())
                time.sleep(0.15)
                os.write(master, b"\x1b[13;2u")
                os.write(master, b"\r")
                while not received.exists() and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertEqual(received.read_bytes(), b"\x1b[13;2u\r")
            finally:
                if client.poll() is None:
                    try:
                        os.write(master, b"\x02d")
                        client.wait(timeout=2)
                    except (OSError, subprocess.TimeoutExpired):
                        client.kill()
                        client.wait()
                os.close(master)
                subprocess.run([tmux, "-L", info["socket"], "kill-server"], capture_output=True)

    def test_real_attached_ctrl_c_returns_130_without_reconnect(self):
        tmux = os.environ.get("CODEX_PANEL_TMUX")
        if not tmux:
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")
        version = subprocess.run([tmux, "-V"], capture_output=True, text=True)
        if version.returncode or "shim" in version.stdout.lower():
            self.skipTest("real tmux unavailable (set CODEX_PANEL_TMUX)")

        with tempfile.TemporaryDirectory(prefix="codex-attached-runtime-") as runtime_root:
            env = os.environ.copy()
            env.update(CODEX_PANEL_TMUX=tmux, CODEX_PANEL_REAL_CODEX=sys.executable,
                       CODEX_PANEL_QUOTA_OFFLINE="1", XDG_RUNTIME_DIR=runtime_root,
                       TERM="xterm-256color")
            env.pop("TMUX", None)
            code = "import time; time.sleep(2)"
            master, slave = pty.openpty()
            client = subprocess.Popen(
                [sys.executable, "codex_panel.py", "--native", "--", "-c", code],
                cwd=Path(__file__).parent, env=env, stdin=slave, stdout=slave, stderr=slave,
            )
            os.close(slave)
            output = []

            def drain():
                try:
                    while True:
                        chunk = os.read(master, 65536)
                        if not chunk:
                            break
                        output.append(chunk)
                except OSError:
                    pass

            reader = threading.Thread(target=drain, daemon=True)
            reader.start()
            try:
                time.sleep(0.7)
                os.write(master, b"\x03")
                client.wait(timeout=6)
                reader.join(timeout=1)
                self.assertEqual(client.returncode, 130)
                self.assertNotIn(b"Para volver:", b"".join(output))
                runtimes = list(Path(runtime_root).glob("codex-panel-*/exit.json"))
                self.assertEqual(len(runtimes), 1)
                self.assertEqual(json.loads(runtimes[0].read_text())["status"], 130)
            finally:
                if client.poll() is None:
                    client.kill()
                    client.wait(timeout=2)
                os.close(master)


if __name__ == "__main__":
    unittest.main()
