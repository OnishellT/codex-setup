"""No-model tests of the native command bridge and exact argv preservation."""
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from codex_panel import launch


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

    def test_native_resume_and_prompt_that_looks_like_remote(self):
        argv = ["resume", "--last", "--", "--remote-is-prompt"]
        command, _, _ = self.run_launch(True, "", argv)
        self.assertEqual(command, ["/official/codex", *argv])

    def test_explicit_panel_still_injects_requested_profile(self):
        command, _, _ = self.run_launch(False, "personal", ["--yolo"])
        self.assertEqual(command, ["/official/codex", "--profile", "personal", "--yolo"])


if __name__ == "__main__":
    unittest.main()
