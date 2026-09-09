import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[1] / "handoff" / "context.py"
spec = importlib.util.spec_from_file_location("context_handoff", SCRIPT)
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


def token_count(input_tokens, window, ordinal=1, total=999999):
    return {"type": "event_msg", "ordinal": ordinal, "payload": {
        "type": "token_count", "info": {
            "last_token_usage": {"input_tokens": input_tokens},
            "model_context_window": window,
            "total_token_usage": {"input_tokens": total},
        }}}


class HandoffTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.codex = self.root / ".codex"
        self.settings = self.root / "settings.json"
        self.settings.write_text(json.dumps(handoff.DEFAULTS))
        self.patch_home = patch.dict(os.environ, {"CODEX_HOME": str(self.codex)})
        self.patch_settings = patch.object(handoff, "_settings", side_effect=self._read_settings)
        self.patch_state = patch.object(handoff, "_state_path", side_effect=self._state_path)
        self.patch_home.start()
        self.addCleanup(self.patch_home.stop)
        self.patch_settings.start()
        self.addCleanup(self.patch_settings.stop)
        self.patch_state.start()
        self.addCleanup(self.patch_state.stop)

    def _read_settings(self):
        return json.loads(self.settings.read_text())

    def _state_path(self, session_id):
        digest = handoff.hashlib.sha256(session_id.encode()).hexdigest()
        return self.root / "state" / (digest + ".json")

    def transcript(self, *records):
        path = self.root / "rollout.jsonl"
        path.write_text("".join(json.dumps(record) + "\n" for record in records))
        return path

    def event(self, path, name="Stop", model="gpt-test"):
        return {"hook_event_name": name, "session_id": "session/../../safe",
                "transcript_path": str(path), "model": model}

    def test_alerts_once_per_band_and_resets_after_compaction(self):
        path = self.transcript(token_count(70, 100))
        first = handoff.hook(self.event(path))
        self.assertIn("70%", first["systemMessage"])
        self.assertIsNone(handoff.hook(self.event(path)))
        path.write_text(path.read_text() + json.dumps(token_count(90, 100, 2)) + "\n")
        self.assertIn("$handoff", handoff.hook(self.event(path))["systemMessage"])
        self.assertIsNone(handoff.hook(self.event(path)))
        self.assertIsNone(handoff.hook(self.event(path, "PostCompact")))
        self.assertIsNotNone(handoff.hook(self.event(path)))
        state = list((self.root / "state").glob("*.json"))
        self.assertEqual(len(state), 1)
        self.assertEqual(len(state[0].stem), 64)

    def test_uses_active_input_not_cumulative_and_supports_model_cliff(self):
        self.settings.write_text(json.dumps({**handoff.DEFAULTS,
            "model_thresholds": {"priced-model": {"warning_tokens": 50}}}))
        path = self.transcript(token_count(51, 100, total=1_000_000))
        self.assertIsNone(handoff.hook(self.event(path, model="other-model")))
        result = handoff.hook({**self.event(path, model="priced-model"), "session_id": "other"})
        self.assertIn("51%", result["systemMessage"])

    def test_sol_default_warns_before_long_context_pricing_threshold(self):
        path = self.transcript(token_count(250_000, 1_050_000))
        warning = handoff.hook(self.event(path, model="gpt-5.6-sol"))
        self.assertIn("Contexto alto", warning["systemMessage"])
        path.write_text(json.dumps(token_count(272_000, 1_050_000, 2)) + "\n")
        critical = handoff.hook(self.event(path, model="gpt-5.6-sol"))
        self.assertIn("Contexto crítico", critical["systemMessage"])

    def test_malformed_or_missing_transcript_fails_open(self):
        bad = self.root / "bad.jsonl"
        bad.write_text("not json\n{}\n")
        for event in (None, {}, self.event(bad), self.event(self.root / "missing")):
            self.assertIsNone(handoff.hook(event))
        process = subprocess.run([sys.executable, str(SCRIPT), "hook"], input=b"{bad",
                                 capture_output=True, env={**os.environ, "CODEX_HOME": str(self.codex)})
        self.assertEqual((process.returncode, process.stdout, process.stderr), (0, b"", b""))

    def test_create_validate_show_and_detect_git_drift(self):
        project = self.root / "project"
        project.mkdir()
        subprocess.run(["git", "init", "-q", str(project)], check=True)
        subprocess.run(["git", "-C", str(project), "config", "user.name", "Test"], check=True)
        subprocess.run(["git", "-C", str(project), "config", "user.email", "test@example.invalid"], check=True)
        (project / "a.txt").write_text("one\n")
        subprocess.run(["git", "-C", str(project), "add", "a.txt"], check=True)
        subprocess.run(["git", "-C", str(project), "commit", "-qm", "initial"], check=True)
        created = handoff._new(project)
        path = Path(created["path"])
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        text = path.read_text().replace(handoff.PLACEHOLDER, "Recorded:")
        path.write_text(text)
        validated = handoff._validate(path)
        self.assertLessEqual(validated["chars"], validated["max_chars"])
        self.assertEqual(handoff._artifact(created["id"]), path)
        self.assertFalse(handoff._check(created["id"], project)["head_changed"])
        (project / "a.txt").write_text("two\n")
        self.assertTrue(handoff._check(created["id"], project)["dirty_state_changed"])

    def test_non_git_artifact_and_validation_guards(self):
        project = self.root / "plain"
        project.mkdir()
        created = handoff._new(project)
        path = Path(created["path"])
        with self.assertRaisesRegex(ValueError, "unfinished"):
            handoff._validate(path)
        path.write_text(path.read_text().replace(handoff.PLACEHOLDER, "Recorded:"))
        self.assertFalse(handoff._check(created["id"], project)["repository_changed"])
        outside = self.root / "outside.md"
        outside.write_text(path.read_text())
        with self.assertRaisesRegex(ValueError, "CODEX_HOME"):
            handoff._validate(outside)
        path.write_text(path.read_text() + "\napi_key=sk-abcdefghijklmnopqrstuvwxyz\n")
        with self.assertRaisesRegex(ValueError, "secret"):
            handoff._validate(path)

    def test_git_drift_hash_covers_output_beyond_markdown_preview(self):
        project = self.root / "large-status"
        project.mkdir()
        subprocess.run(["git", "init", "-q", str(project)], check=True)
        for index in range(300):
            (project / f"a-{index:04}-{'x' * 24}.txt").touch()
        before = handoff._git_snapshot(project)
        (project / "z-after-preview.txt").touch()
        after = handoff._git_snapshot(project)
        self.assertEqual(before["status"], after["status"], "preview should stay truncated")
        self.assertNotEqual(before["status_hash"], after["status_hash"])


if __name__ == "__main__":
    unittest.main()
