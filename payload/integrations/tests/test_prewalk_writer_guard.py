import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "prewalk"))
spec = importlib.util.spec_from_file_location("writer_guard", ROOT / "prewalk" / "writer_guard.py")
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class WriterGuardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.codex_home = self.root / "codex"
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "user.name", "Test")
        (self.repo / "app.txt").write_text("base\n")
        self.git("add", "app.txt")
        self.git("commit", "-qm", "base")
        parent = self.root / "sessions"
        parent.mkdir()
        result = subprocess.run([sys.executable, str(ROOT / "prewalk" / "worktrees.py"),
                                 "create", "--repo", str(self.repo), "--workers", "1",
                                 "--session-parent", str(parent)],
                                text=True, capture_output=True, check=True,
                                env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
        data = json.loads(result.stdout)
        self.manifest = Path(data["manifest"])
        self.worker = Path(data["workers"][0]["path"])

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo), *args],
                              text=True, capture_output=True, check=True)

    def tearDown(self):
        self.temp.cleanup()

    def event(self, **args):
        return {"hook_event_name": "PreToolUse", "tool_name": "spawn_agent",
                "tool_input": args, "cwd": str(self.repo)}

    def denied(self, event):
        result = guard.guard(event, codex_home_path=self.codex_home)
        self.assertIsNotNone(result)
        self.assertEqual(result["hookSpecificOutput"]["permissionDecision"], "deny")

    def test_non_writer_and_non_spawn_events_are_unchanged(self):
        self.assertIsNone(guard.guard(self.event(agent_type="explorer"), codex_home_path=self.codex_home))
        event = {"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": {}}
        self.assertIsNone(guard.guard(event, codex_home_path=self.codex_home))

    def test_writer_requires_manifest_and_worker_path(self):
        self.denied(self.event(agent_type="prewalk_executor", prompt="edit files"))
        self.denied(self.event(agent_type="fallback_executor", prompt=str(self.manifest)))

    def test_valid_manifest_and_distinct_worker_are_allowed(self):
        result = guard.guard(self.event(agent_type="prewalk_executor",
                                        prompt=f"manifest {self.manifest} worktree {self.worker}"),
                             codex_home_path=self.codex_home)
        self.assertIsNone(result)

    def test_shared_checkout_is_denied(self):
        event = self.event(agent_type="prewalk_executor",
                           prompt=f"manifest {self.manifest} worktree {self.worker}")
        event["cwd"] = str(self.worker)
        self.denied(event)

    def test_invalid_manifest_is_denied(self):
        self.manifest.write_text("{}")
        self.denied(self.event(agent_type="prewalk_executor",
                               prompt=f"manifest {self.manifest} worktree {self.worker}"))

    def test_json_marker_preserves_paths_with_spaces(self):
        marker = json.dumps({"manifest": "/tmp/session with spaces/manifest.json",
                             "worktree": "/tmp/session with spaces/worker-1"})
        self.assertEqual(guard.manifest_candidates({"prompt": marker}),
                         {"/tmp/session with spaces/manifest.json"})

    def test_malformed_stdin_fails_closed(self):
        result = subprocess.run([sys.executable, str(ROOT / "prewalk" / "writer_guard.py")],
                                input=b"{invalid", text=False, capture_output=True)
        self.assertEqual(result.returncode, 0)
        output = json.loads(result.stdout)
        self.assertEqual(output["hookSpecificOutput"]["permissionDecision"], "deny")

    def test_worktree_reservation_is_single_use(self):
        event = self.event(agent_type="prewalk_executor",
                           prompt=f"manifest {self.manifest} worktree {self.worker}")
        self.assertIsNone(guard.guard(event, codex_home_path=self.codex_home))
        self.denied(event)

    def test_v2_opaque_message_uses_registered_assignment(self):
        event = self.event(agent_type="prewalk_executor", task_name="report_worker", message="gAAAA_opaque_v2_message")
        event["session_id"] = "parent-session"
        self.denied(event)
        guard.register(str(self.manifest), "worker-1", "report_worker", "parent-session", home=self.codex_home)
        self.assertIsNone(guard.guard(event, codex_home_path=self.codex_home))
        self.denied(event)

    def test_registration_is_bound_to_session_task_and_role(self):
        guard.register(str(self.manifest), "worker-1", "report_worker", "parent-session", home=self.codex_home)
        event = self.event(agent_type="prewalk_executor", task_name="report_worker", message="opaque")
        event["session_id"] = "other-session"
        self.denied(event)
        event["session_id"] = "parent-session"
        event["tool_input"]["task_name"] = "other_task"
        self.denied(event)
        event["tool_input"]["task_name"] = "report_worker"
        event["tool_input"]["agent_type"] = "fallback_executor"
        self.denied(event)

    def test_registration_requires_native_parent_and_known_worker(self):
        with self.assertRaises(ValueError):
            guard.register(str(self.manifest), "worker-1", "report_worker", None, home=self.codex_home)
        with self.assertRaises(ValueError):
            guard.register(str(self.manifest), "worker-9", "report_worker", "parent", home=self.codex_home)


if __name__ == "__main__":
    unittest.main()
