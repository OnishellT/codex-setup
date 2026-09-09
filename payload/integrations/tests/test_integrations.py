"""Adapter contract tests; never contact an LLM or touch the real Codex home."""

import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import shlex
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


rtk = load("rtk_hook", ROOT / "rtk" / "hook.py")
ponytail = load("ponytail_runner", ROOT / "ponytail" / "runner.py")


class RTKTests(unittest.TestCase):
    def event(self, command="git status --short"):
        return {"hook_event_name": "PreToolUse", "tool_name": "Bash",
                "tool_input": {"command": command, "cwd": "/tmp/example", "timeout_ms": 9000}}

    def test_rewrites_without_losing_arguments_or_using_shell(self):
        event = self.event("git status; printf '%s' '$SECRET'")
        result = subprocess.CompletedProcess([], 0, "rtk git status; printf '%s' '$SECRET'", "")
        with patch.object(rtk.subprocess, "run", return_value=result) as run:
            output = rtk.rewrite(event)["hookSpecificOutput"]
        self.assertEqual(output["permissionDecision"], "allow")
        self.assertEqual(output["updatedInput"]["cwd"], "/tmp/example")
        self.assertEqual(output["updatedInput"]["timeout_ms"], 9000)
        self.assertTrue(output["updatedInput"]["command"].startswith("rtk git"))
        self.assertEqual(event["tool_input"]["command"], "git status; printf '%s' '$SECRET'")
        self.assertEqual(run.call_args.args[0], ["rtk", "rewrite", "--", event["tool_input"]["command"]])
        self.assertNotIn("shell", run.call_args.kwargs)
        self.assertEqual(run.call_args.kwargs["timeout"], 2)

    def test_exit_three_never_promotes_ask_to_allow(self):
        with patch.object(rtk.subprocess, "run", return_value=subprocess.CompletedProcess([], 3, "rtk git status && rtk git diff", "")):
            self.assertIsNone(rtk.rewrite(self.event("git status && git diff")))

    def test_non_rewrites(self):
        for code, output in ((1, ""), (2, "error"), (0, ""), (0, "git status --short")):
            with self.subTest(code=code, output=output), patch.object(rtk.subprocess, "run", return_value=subprocess.CompletedProcess([], code, output, "")):
                self.assertIsNone(rtk.rewrite(self.event()))

    def test_ignores_wrong_event_tool_and_malformed_arguments(self):
        events = [None, [], {}, {**self.event(), "hook_event_name": "PermissionRequest"},
                  {**self.event(), "tool_name": "apply_patch"},
                  {**self.event(), "tool_input": []}, self.event(None), self.event(""),
                  self.event("rtk git status"), self.event("\n rtk git status"), self.event("abc\0def")]
        with patch.object(rtk.subprocess, "run") as run:
            for event in events:
                self.assertIsNone(rtk.rewrite(event))
            run.assert_not_called()

    def test_opt_out(self):
        with patch.dict(os.environ, {"RTK_DISABLED": "1"}), patch.object(rtk.subprocess, "run") as run:
            self.assertIsNone(rtk.rewrite(self.event()))
            run.assert_not_called()

    def test_managed_rtk_without_codex_home_and_path(self):
        with tempfile.TemporaryDirectory(prefix="rtk managed ") as temp:
            root = Path(temp) / "home with spaces"
            hook = root / "integrations" / "rtk" / "hook.py"
            hook.parent.mkdir(parents=True)
            shutil.copy(ROOT / "rtk" / "hook.py", hook)
            binary = hook.parent / "bin" / "rtk"
            binary.parent.mkdir()
            binary.write_text("#!/bin/sh\nprintf '%s\\n' 'rtk git status'\n")
            binary.chmod(0o755)
            with patch.dict(os.environ, {"PATH": "/nonexistent", "CODEX_HOME": ""}, clear=False), patch.object(rtk, "__file__", str(hook)):
                output = rtk.rewrite(self.event())
            self.assertEqual(output["hookSpecificOutput"]["updatedInput"]["command"], shlex.quote(str(binary)) + " git status")

    def test_managed_rtk_declines_unexpected_output_prefix(self):
        with tempfile.TemporaryDirectory(prefix="rtk managed ") as temp:
            root = Path(temp) / "home with spaces"
            managed = root / "integrations" / "rtk" / "bin" / "rtk"
            managed.parent.mkdir(parents=True)
            managed.write_text("#!/bin/sh\nprintf '%s\\n' 'unexpected output'\n")
            managed.chmod(0o755)
            with patch.dict(os.environ, {"PATH": "/nonexistent", "CODEX_HOME": ""}, clear=False), patch.object(rtk, "__file__", str(root / "integrations" / "rtk" / "hook.py")):
                self.assertIsNone(rtk.rewrite(self.event()))

    def test_entrypoint_timeout_and_missing_rtk(self):
        for error in (FileNotFoundError(), subprocess.TimeoutExpired("rtk", 2)):
            raw = json.dumps(self.event()).encode()
            with patch.object(rtk.sys, "stdin", SimpleNamespace(buffer=io.BytesIO(raw))), \
                 patch.object(rtk.sys, "stdout", new_callable=io.StringIO) as output, \
                 patch.object(rtk.subprocess, "run", side_effect=error):
                rtk.main()
                self.assertEqual(output.getvalue(), "")

    def test_entrypoint_invalid_json_and_missing_binary_are_fail_open(self):
        for raw in (b"{invalid", json.dumps(self.event()).encode(), b"x" * (rtk.MAX_INPUT_BYTES + 1)):
            result = subprocess.run([sys.executable, str(ROOT / "rtk" / "hook.py")], input=raw,
                                    capture_output=True, env={**os.environ, "PATH": "/nonexistent"})
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stdout, b"")
            self.assertEqual(result.stderr, b"")

    @unittest.skipUnless(shutil.which("rtk"), "RTK not installed")
    def test_actual_rtk_parser_respects_permission_exit(self):
        with patch.dict(os.environ, {"RTK_DISABLED": "0"}):
            direct = subprocess.run(["rtk", "rewrite", "--", "git status --short"], capture_output=True, text=True)
            output = rtk.rewrite(self.event())
            if direct.returncode == 0:
                self.assertEqual(output["hookSpecificOutput"]["updatedInput"]["command"], "rtk git status --short")
            else:
                self.assertIsNone(output)
            self.assertIsNone(rtk.rewrite(self.event("-h")))


@unittest.skipUnless(shutil.which("node"), "Node.js not installed")
class PonytailTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ponytail-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "home with ' quotes" / ".codex" / "integrations" / "ponytail"
        shutil.copytree(ROOT / "ponytail" / "upstream", self.root / "upstream")
        self.root_patch = patch.object(ponytail, "ROOT", self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.env_patch = patch.dict(os.environ, {"PONYTAIL_DEFAULT_MODE": "full", "COPILOT_PLUGIN_DATA": "/nonexistent/copilot", "QODER_SESSION_ID": "unrelated"})
        self.env_patch.start()
        self.addCleanup(self.env_patch.stop)

    def call(self, action, session="work-session", **extra):
        return ponytail.run_hook(action, {"hook_event_name": ponytail.HOOKS[action][0],
                                        "session_id": session, **extra})

    def context(self, output):
        return output["hookSpecificOutput"]["additionalContext"]

    def test_activation_and_parent_subagent_inherit(self):
        output = self.call("activate", source="startup")
        self.assertEqual(output["systemMessage"], "PONYTAIL:FULL")
        self.assertEqual(output["hookSpecificOutput"]["hookEventName"], "SessionStart")
        self.assertIn("PONYTAIL MODE ACTIVE", self.context(output))
        self.call("prompt", prompt="$ponytail ultra")
        subagent = self.call("subagent")
        self.assertEqual(subagent["hookSpecificOutput"]["hookEventName"], "SubagentStart")
        self.assertIn("level: ultra", self.context(subagent))

    def test_uses_private_instructions_not_discoverable_skills(self):
        upstream = ROOT / "ponytail" / "upstream"
        self.assertTrue((upstream / "instructions.md").is_file())
        self.assertFalse(any(upstream.rglob("SKILL.md")))
        source = (upstream / "hooks" / "ponytail-instructions.js").read_text()
        self.assertIn("instructions.md", source)
        self.assertNotIn("skills', 'ponytail'", source)

    def test_off_is_isolated_and_survives_compact_resume(self):
        self.call("activate")
        self.call("activate", session="personal-session")
        self.call("prompt", prompt="stop ponytail")
        self.assertIsNone(self.call("subagent"))
        self.assertIn("level: full", self.context(self.call("subagent", session="personal-session")))
        for source in ("compact", "resume"):
            output = self.call("activate", source=source)
            self.assertEqual(output, {"systemMessage": "PONYTAIL:OFF"})
        self.assertIn("level: full", self.context(self.call("activate", source="clear")))

    def test_mode_survives_compaction(self):
        self.call("activate")
        self.call("prompt", prompt="$ponytail lite")
        self.assertIn("level: lite", self.context(self.call("activate", source="compact")))

    def test_default_stays_in_codex_integration(self):
        self.call("activate")
        self.call("prompt", prompt="$ponytail default lite")
        config = self.root / "preferences" / "ponytail" / "config.json"
        self.assertEqual(json.loads(config.read_text())["defaultMode"], "lite")
        # Explicit default-mode environment variables retain upstream precedence.
        with patch.dict(os.environ):
            os.environ.pop("PONYTAIL_DEFAULT_MODE", None)
            self.assertIn("level: lite", self.context(self.call("activate", session="new-session")))

    def test_invalid_event_creates_no_state(self):
        for event in (None, [], {}, {"hook_event_name": "SessionStart", "session_id": ""},
                      {"hook_event_name": "PermissionRequest", "session_id": "a"}):
            self.assertIsNone(ponytail.run_hook("activate", event))
        self.assertFalse((self.root / "state").exists())

    def test_id_is_hashed_and_private(self):
        self.call("activate", session="../../outside; $(touch pwned)")
        dirs = list((self.root / "state").iterdir())
        self.assertEqual(len(dirs), 1)
        self.assertEqual(len(dirs[0].name), 64)
        self.assertEqual(dirs[0].stat().st_mode & 0o777, 0o700)

    def test_output_validation(self):
        for stdout in ("not-json", "{}{}", "[]"):
            with patch.object(ponytail.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, stdout, "")):
                try:
                    output = self.call("activate")
                except ValueError:
                    output = None
                self.assertIsNone(output)
        result = subprocess.run([sys.executable, str(ROOT / "ponytail" / "runner.py"), "activate"],
                                input=b"{invalid", capture_output=True)
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, b"", b""))

    def test_entrypoint_valid_input_missing_node_and_timeout(self):
        event = {"hook_event_name": "SessionStart", "session_id": "entrypoint-test"}
        for error in (FileNotFoundError(), subprocess.TimeoutExpired("node", 3)):
            with patch.object(ponytail.sys, "argv", ["runner.py", "activate"]), \
                 patch.object(ponytail.sys, "stdin", SimpleNamespace(buffer=io.BytesIO(json.dumps(event).encode()))), \
                 patch.object(ponytail.sys, "stdout", new_callable=io.StringIO) as output, \
                 patch.object(ponytail.subprocess, "run", side_effect=error) as run:
                ponytail.main()
                run.assert_called_once()
                self.assertEqual(output.getvalue(), "")

    def test_entrypoint_invalid_upstream_output(self):
        event = {"hook_event_name": "SessionStart", "session_id": "bad-output-test"}
        with patch.object(ponytail.sys, "argv", ["runner.py", "activate"]), \
             patch.object(ponytail.sys, "stdin", SimpleNamespace(buffer=io.BytesIO(json.dumps(event).encode()))), \
             patch.object(ponytail.sys, "stdout", new_callable=io.StringIO) as output, \
             patch.object(ponytail.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "log\n{}", "")):
            ponytail.main()
            self.assertEqual(output.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
