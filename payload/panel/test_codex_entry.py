"""No model calls: unit mocks and real exec/PTY tests using temporary fixtures."""

import contextlib
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import codex_entry as entry

try:
    import pty
except ImportError:
    pty = None


class ClassifierTests(unittest.TestCase):
    def assert_route(self, args, target="panel", profile="", **kwargs):
        original = list(args)
        self.assertEqual(entry.classify(args, **kwargs), entry.Route(target, profile))
        self.assertEqual(args, original)

    def test_interactive_and_profiles(self):
        for args, profile in [
            ([], ""), (["a prompt mentioning exec and help"], ""),
            (["-p", "work"], "work"), (["--profile", "work"], "work"),
            (["--profile=work"], "work"), (["-pwork"], "work"),
            (["-p=work"], "work"), (["--profile="], ""),
            (["-p", "first", "--profile=last"], "last"),
            (["prompt", "-pwork"], "work"),
            (["resume", "--last", "-p", "work"], "work"),
            (["fork", "session", "--profile=work", "prompt"], "work"),
            (["-pwork", "resume", "--all", "--include-non-interactive"], "work"),
            (["fork", "--all", "--last"], ""),
            (["-C", "../relative dir", "--yolo"], ""),
        ]:
            with self.subTest(args=args):
                self.assert_route(args, profile=profile)

    def test_each_service_command_and_alias(self):
        commands = "exec e review login logout mcp plugin mcp-server app-server remote-control completion update doctor sandbox debug apply a agents queue archive delete migrate-rollouts unarchive cloud exec-server features help".split()
        for command in commands:
            for prefix in [[], ["-p", "work"], ["-C../dir", "--config", "model=exec"]]:
                with self.subTest(command=command, prefix=prefix):
                    self.assert_route([*prefix, command, "arbitrary", "--unknown"], "official")

    def test_non_tty_and_exact_environment_switches(self):
        for stdin, stdout in [(False, False), (False, True), (True, False)]:
            self.assert_route([], "official", stdin_isatty=stdin, stdout_isatty=stdout)
        for name in ["CODEX_PANEL_DISABLE", "CODEX_PANEL_ACTIVE"]:
            self.assert_route([], "official", environ={name: "1"})
            for value in ["", "0", "true", "2"]:
                self.assert_route([], environ={name: value})
        with patch.dict(os.environ, {"CODEX_PANEL_DISABLE": "1"}):
            self.assert_route([])  # Pure: ambient environment is not consulted.

    def test_help_version_remote_ephemeral(self):
        for option in ["-h", "--help", "-V", "--version", "-hV", "--remote",
                       "--remote=unix://server", "--remote-auth-token-env",
                       "--remote-auth-token-env=TOKEN", "--ephemeral"]:
            for prefix in [[], ["resume"], ["fork", "session"], ["prompt"]]:
                with self.subTest(option=option, prefix=prefix):
                    self.assert_route([*prefix, option], "official")

    def test_known_option_values_do_not_become_commands(self):
        options = ["-c", "--config", "--enable", "--disable", "-i", "--image",
                   "-m", "--model", "--local-provider", "-p", "--profile", "-s",
                   "--sandbox", "-C", "--cd", "--add-dir", "-a", "--ask-for-approval"]
        for option in options:
            for value in ["help", "exec", "resume", "fork", "a", "contains exec help"]:
                profile = value if option in {"-p", "--profile"} else ""
                with self.subTest(option=option, value=value):
                    self.assert_route([option, value], profile=profile)
                    self.assert_route([option + "=" + value], profile=profile)
                    if not option.startswith("--"):
                        self.assert_route([option + value], profile=profile)
                    if option in {"-i", "--image"}:
                        self.assert_route([option, value, "exec"])
                        self.assert_route([option, value, "--search", "exec"], "official")
                    else:
                        self.assert_route([option, value, "exec"], "official")
        self.assert_route(["--config=help=exec", "--image=a.png,b.png", "prompt"])
        self.assert_route(["-i", "exec", "-i", "help", "prompt"])
        self.assert_route(["--profile=--help"], profile="--help")
        self.assert_route(["-p--ephemeral"], profile="--ephemeral")

    def test_detached_images_are_variadic_but_attached_images_are_not(self):
        for option in ["-i", "--image"]:
            self.assert_route([option, "first.png", "exec", "help", "resume"])
            self.assert_route([option, "first.png", "help", "-pwork"], profile="work")
            self.assert_route([option, "first.png", "exec", "--", "--help"])
            self.assert_route([option, "first.png", "--search", "exec"], "official")
        for option in ["-ifirst.png", "-i=first.png", "--image=first.png"]:
            self.assert_route([option, "exec"], "official")
            self.assert_route([option, "resume", "-pwork"], profile="work")

    def test_all_root_switches(self):
        switches = ["--strict-config", "--oss", "--approve-for-me",
                    "--dangerously-bypass-approvals-and-sandbox", "--yolo",
                    "--dangerously-bypass-hook-trust", "--search", "--no-alt-screen"]
        for option in switches:
            self.assert_route([option, "prompt"])
            self.assert_route([option, "exec"], "official")
            self.assert_route([option + "=false"], "official")

    def test_positional_prompts_and_session_names(self):
        for word in ["exec", "help", "resume", "fork", "cloud"]:
            self.assert_route(["prompt", word])
            self.assert_route(["resume", word])
            self.assert_route(["fork", "id", word])
        self.assert_route([""])
        self.assert_route(["-"])
        self.assert_route(["resume", "exec", "-pwork"], profile="work")

    def test_double_dash_stops_all_interpretation(self):
        for suffix in [["exec"], ["--help"], ["--remote=x"], ["--ephemeral"],
                       ["--unknown"], ["-p", "not-a-profile"]]:
            self.assert_route(["--", *suffix])
            self.assert_route(["-pwork", "resume", "--", *suffix], profile="work")
        self.assert_route(["exec", "--", "prompt"], "official")

    def test_unknown_and_malformed_options_are_left_to_cli(self):
        for args in [["--future"], ["--future=exec"], ["-z"], ["-xpfoo"],
                     ["-p"], ["--profile"], ["-C"], ["--model", "--help"],
                     ["--profile", "--"], ["--last"], ["--all"],
                     ["fork", "--include-non-interactive"],
                     ["resume", "--future"], ["prompt", "--future"]]:
            with self.subTest(args=args):
                self.assert_route(args, "official")


class FixtureTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="codex entry tests ")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.real = self.executable("codex")
        self.panel = self.executable("codex-panel")
        self.env = {
            "PATH": str(self.root),
            "CODEX_PANEL_REAL_CODEX": str(self.real),
            "CODEX_PANEL_LAUNCHER": str(self.panel),
            "ENTRY_TEST_OUTPUT": str(self.root / "result.json"),
            "ENTRY_TEST_EXIT": "37",
            "PRESERVED_VARIABLE": "untouched",
            "PYTHONDONTWRITEBYTECODE": "1",
        }

    def executable(self, name, source=None):
        path = self.root / name
        if source is None:
            source = (
                "import json, os, signal, sys\n"
                "with open(os.environ['ENTRY_TEST_OUTPUT'], 'w') as output:\n"
                "    json.dump({'argv': sys.argv, 'cwd': os.getcwd(), 'pid': os.getpid(),\n"
                "               'env': dict(os.environ), 'tty': [os.isatty(0), os.isatty(1)]}, output)\n"
                "if os.environ.get('ENTRY_TEST_SIGNAL'):\n"
                "    os.kill(os.getpid(), int(os.environ['ENTRY_TEST_SIGNAL']))\n"
                "sys.exit(int(os.environ['ENTRY_TEST_EXIT']))\n"
            )
        path.write_text("#!" + sys.executable + "\n" + source)
        path.chmod(0o755)
        return path


class MainTests(FixtureTests):
    def invoke(self, args, stdin=True, stdout=True, env=None, exec_error=None):
        error_output = io.StringIO()
        with patch.dict(os.environ, self.env if env is None else env, clear=True), \
                patch.object(entry.sys.stdin, "isatty", return_value=stdin), \
                patch.object(entry.sys.stdout, "isatty", return_value=stdout), \
                patch.object(entry.os, "execvpe", side_effect=exec_error) as execute, \
                patch.object(entry.os, "chdir") as chdir, \
                contextlib.redirect_stderr(error_output):
            before = dict(os.environ)
            result = entry.main(args)
            self.assertEqual(dict(os.environ), before)
            chdir.assert_not_called()
        return result, execute, error_output.getvalue()

    def test_native_panel_exact_argv_environment_and_cwd(self):
        args = ["-C", "../relative work", "resume", "--profile=work", "--",
                "$(touch DO_NOT_CREATE); 'quotes'\n秘密", "", "exec"]
        result, execute, error = self.invoke(args)
        self.assertEqual((result, error), (0, ""))
        path, actual, env = execute.call_args.args
        self.assertEqual(path, str(self.panel))
        self.assertEqual(actual, [str(self.panel), "--native", "--profile", "work", "--", *args])
        self.assertEqual(env["CODEX_PANEL_ACTIVE"], "1")
        self.assertEqual(env["CODEX_PANEL_REAL_CODEX"], str(self.real))
        self.assertEqual(env["PRESERVED_VARIABLE"], "untouched")
        self.assertEqual(env[entry._EXEC_PID], str(os.getpid()))

    def test_no_default_profile_injected_into_cli_arguments(self):
        _, execute, _ = self.invoke([])
        self.assertEqual(execute.call_args.args[1], [str(self.panel), "--native", "--profile", "", "--"])

    def test_official_exact_argv_all_bypass_conditions(self):
        for args, stdin, stdout, overrides in [
            (["exec", "--json", "sensitive prompt"], True, True, {}),
            (["resume", "--help"], True, True, {}),
            (["--unknown", "value"], True, True, {}),
            (["--remote=unix://socket"], True, True, {}),
            (["--ephemeral"], True, True, {}),
            (["prompt"], False, True, {}), (["prompt"], True, False, {}),
            (["prompt"], True, True, {"CODEX_PANEL_DISABLE": "1"}),
            (["prompt"], True, True, {"CODEX_PANEL_ACTIVE": "1"}),
        ]:
            with self.subTest(args=args, overrides=overrides, stdin=stdin, stdout=stdout):
                env = dict(self.env, **overrides)
                env["CODEX_PANEL_LAUNCHER"] = str(self.root / "missing-panel")
                result, execute, error = self.invoke(args, stdin, stdout, env)
                self.assertEqual((result, error), (0, ""))
                self.assertEqual(execute.call_args.args[:2], (str(self.real), [str(self.real), *args]))
                self.assertEqual(execute.call_args.args[2].get("CODEX_PANEL_ACTIVE"), overrides.get("CODEX_PANEL_ACTIVE"))

    def test_main_uses_sys_argv_by_default(self):
        with patch.object(sys, "argv", [entry.__file__, "-pwork", "a prompt"]):
            _, execute, _ = self.invoke(None)
        self.assertEqual(execute.call_args.args[1][-2:], ["-pwork", "a prompt"])

    def test_path_fallback_is_absolute_and_exported(self):
        env = {key: value for key, value in self.env.items()
               if key not in {"CODEX_PANEL_REAL_CODEX", "CODEX_PANEL_LAUNCHER"}}
        _, execute, error = self.invoke([], env=env)
        self.assertEqual(error, "")
        self.assertEqual(execute.call_args.args[0], str(self.panel))
        self.assertEqual(execute.call_args.args[2]["CODEX_PANEL_REAL_CODEX"], str(self.real))

    def test_explicit_missing_paths_do_not_fall_back(self):
        for key in ["CODEX_PANEL_REAL_CODEX", "CODEX_PANEL_LAUNCHER"]:
            with self.subTest(key=key):
                env = dict(self.env, **{key: str(self.root / "missing")})
                result, execute, error = self.invoke([], env=env)
                self.assertEqual(result, 127)
                execute.assert_not_called()
                self.assertIn(key, error)
                self.assertIn("missing", error)

    def test_missing_path_executables(self):
        env = {"PATH": str(self.root / "empty")}
        result, execute, error = self.invoke([], env=env)
        self.assertEqual(result, 127)
        execute.assert_not_called()
        self.assertIn("Official Codex", error)
        env["CODEX_PANEL_REAL_CODEX"] = str(self.real)
        result, execute, error = self.invoke([], env=env)
        self.assertEqual(result, 127)
        execute.assert_not_called()
        self.assertIn("codex-panel", error)

    def test_non_executable_and_directory(self):
        self.real.chmod(0o644)
        result, execute, error = self.invoke([])
        self.assertEqual(result, 126)
        execute.assert_not_called()
        self.assertIn("not executable", error)
        env = dict(self.env, CODEX_PANEL_REAL_CODEX=str(self.root))
        result, execute, _ = self.invoke([], env=env)
        self.assertEqual(result, 127)
        execute.assert_not_called()

    def test_relative_explicit_path_rejected(self):
        relative = os.path.relpath(self.real)
        result, execute, error = self.invoke([], env=dict(self.env, CODEX_PANEL_REAL_CODEX=relative))
        self.assertEqual(result, 126)
        execute.assert_not_called()
        self.assertIn("absolute", error)

    def test_original_cannot_be_panel_even_when_disabled(self):
        for target in [self.panel, self.root / "panel-alias", self.root / "panel-hardlink"]:
            if target.name == "panel-alias":
                target.symlink_to(self.panel)
            elif target.name == "panel-hardlink":
                os.link(self.panel, target)
            env = dict(self.env, CODEX_PANEL_REAL_CODEX=str(target), CODEX_PANEL_DISABLE="1")
            result, execute, error = self.invoke([], env=env)
            self.assertEqual(result, 126)
            execute.assert_not_called()
            self.assertIn("not official Codex", error)

    def test_dispatcher_self_and_alias_validation(self):
        # Use an executable stand-in for __file__, without chmod on source files.
        dispatcher = self.executable("dispatcher")
        alias = self.root / "dispatcher-alias"
        alias.symlink_to(dispatcher)
        for key in ["CODEX_PANEL_REAL_CODEX", "CODEX_PANEL_LAUNCHER"]:
            for target in [dispatcher, alias]:
                with patch.object(entry, "__file__", str(dispatcher)):
                    result, execute, error = self.invoke([], env=dict(self.env, **{key: str(target)}))
                self.assertEqual(result, 126)
                execute.assert_not_called()
                self.assertIn("dispatcher", error)

    def test_exec_loop_guard_is_pid_scoped(self):
        result, execute, error = self.invoke([], env=dict(self.env, **{entry._EXEC_PID: str(os.getpid())}))
        self.assertEqual(result, 126)
        execute.assert_not_called()
        self.assertIn("recursive", error)
        result, execute, _ = self.invoke([], env=dict(self.env, **{entry._EXEC_PID: "-1"}))
        self.assertEqual(result, 0)
        execute.assert_called_once()

    def test_exec_errors_are_concise_and_never_log_arguments(self):
        for error, expected in [(FileNotFoundError("SECRET"), 127),
                                (PermissionError("SECRET"), 126), (OSError("SECRET"), 126)]:
            result, _, diagnostic = self.invoke(["SECRET"], exec_error=error)
            self.assertEqual(result, expected)
            self.assertNotIn("SECRET", diagnostic)
            self.assertIn("unable to execute", diagnostic)


@unittest.skipUnless(os.name == "posix" and pty is not None, "requires POSIX PTYs and exec")
class ExecIntegrationTests(FixtureTests):
    def run_entry(self, args, stdin_tty=True, stdout_tty=True, overrides=None):
        env = dict(self.env, **(overrides or {}))
        master, slave = pty.openpty()
        try:
            with subprocess.Popen(
                [sys.executable, "-B", str(Path(entry.__file__).absolute()), *args],
                stdin=slave if stdin_tty else subprocess.DEVNULL,
                stdout=slave if stdout_tty else subprocess.PIPE,
                stderr=subprocess.PIPE, cwd=self.root, env=env,
            ) as proc:
                try:
                    output, error = proc.communicate(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.communicate()
                    self.fail("dispatcher did not exit (possible recursion)")
                result = (proc.returncode, proc.pid, output, error)
        finally:
            os.close(slave)
            os.close(master)
        output_path = Path(env["ENTRY_TEST_OUTPUT"])
        record = json.loads(output_path.read_text()) if output_path.exists() else None
        return result, record

    def test_pty_panel_preserves_process_argv_cwd_environment_and_status(self):
        args = ["-C", "../relative project", "resume", "-pwork", "--", "",
                "$(touch PWNED); `touch PWNED` 'quoted'\n秘密", "--help"]
        (status, pid, _, error), record = self.run_entry(args)
        self.assertEqual((status, error), (37, b""))
        self.assertEqual(record["pid"], pid)  # exec, not a waited-on child.
        self.assertEqual(record["argv"], [str(self.panel), "--native", "--profile", "work", "--", *args])
        self.assertEqual(record["cwd"], str(self.root))
        self.assertEqual(record["tty"], [True, True])
        self.assertEqual(record["env"]["CODEX_PANEL_ACTIVE"], "1")
        self.assertEqual(record["env"]["PRESERVED_VARIABLE"], "untouched")
        self.assertFalse((self.root / "PWNED").exists())

    def test_redirecting_either_stream_bypasses_panel(self):
        args = ["fork", "session", "--profile=work", "literal prompt"]
        for stdin, stdout in [(False, True), (True, False), (False, False)]:
            with self.subTest(stdin=stdin, stdout=stdout):
                (status, pid, output, error), record = self.run_entry(args, stdin, stdout)
                self.assertEqual((status, error), (37, b""))
                self.assertIn(output, (None, b""))
                self.assertEqual(record["argv"], [str(self.real), *args])
                self.assertEqual(record["pid"], pid)
                self.assertEqual(record["tty"], [stdin, stdout])
                self.assertNotIn("CODEX_PANEL_ACTIVE", record["env"])

    def test_pty_variadic_image_values_are_preserved(self):
        args = ["--image", "first.png", "help", "exec", "-pwork"]
        (status, _, _, error), record = self.run_entry(args)
        self.assertEqual((status, error), (37, b""))
        self.assertEqual(record["argv"], [str(self.panel), "--native", "--profile", "work", "--", *args])

    def test_pty_passthrough_commands_modes_and_env(self):
        for args, overrides in [
            (["exec", "--json", "$(literal)"], {}), (["help"], {}),
            (["--help"], {}), (["--version"], {}), (["--unknown", "exec"], {}),
            (["--remote", "unix://socket"], {}), (["resume", "--ephemeral"], {}),
            ([], {"CODEX_PANEL_DISABLE": "1"}), ([], {"CODEX_PANEL_ACTIVE": "1"}),
        ]:
            with self.subTest(args=args, overrides=overrides):
                (status, pid, _, error), record = self.run_entry(args, overrides=overrides)
                self.assertEqual((status, error), (37, b""))
                self.assertEqual(record["argv"], [str(self.real), *args])
                self.assertEqual(record["pid"], pid)

    def test_actual_path_lookup(self):
        (status, _, _, error), record = self.run_entry([], overrides={
            "CODEX_PANEL_REAL_CODEX": "", "CODEX_PANEL_LAUNCHER": "",
        })
        self.assertEqual((status, error), (37, b""))
        self.assertEqual(record["argv"], [str(self.panel), "--native", "--profile", "", "--"])
        self.assertEqual(record["env"]["CODEX_PANEL_REAL_CODEX"], str(self.real))

    def test_child_signal_status_is_not_remapped(self):
        for args in [[], ["exec"]]:
            (status, _, _, error), _ = self.run_entry(args, overrides={"ENTRY_TEST_SIGNAL": str(signal.SIGTERM)})
            self.assertEqual(status, -signal.SIGTERM)
            self.assertEqual(error, b"")

    def test_missing_panel_is_explicit_and_does_not_run_official(self):
        (status, _, _, error), record = self.run_entry(["PRIVATE"], overrides={
            "CODEX_PANEL_LAUNCHER": str(self.root / "missing"),
        })
        self.assertEqual(status, 127)
        self.assertIsNone(record)
        self.assertIn(b"codex-panel", error)
        self.assertNotIn(b"PRIVATE", error)

    def test_exec_wrapper_loop_stops_without_model_call(self):
        loop = self.executable("misconfigured-wrapper", (
            "import os, sys\n"
            f"os.execv(sys.executable, [sys.executable, '-B', {entry.__file__!r}, *sys.argv[1:]])\n"
        ))
        (status, _, _, error), record = self.run_entry(["exec", "PRIVATE"], overrides={
            "CODEX_PANEL_REAL_CODEX": str(loop),
        })
        self.assertEqual(status, 126)
        self.assertIsNone(record)
        self.assertIn(b"recursive", error)
        self.assertNotIn(b"PRIVATE", error)


if __name__ == "__main__":
    unittest.main()
