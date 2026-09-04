import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

import codex_panel as panel


def record(kind, payload):
    return json.dumps({"type": kind, "timestamp": "2026-09-03T21:00:00Z", "payload": payload}) + "\n"


def meta(identity, parent=None, timestamp="2026-09-03T21:00:00Z"):
    return {"id": identity, "timestamp": timestamp, "source": "cli" if parent is None else {
        "subagent": {"thread_spawn": {"parent_thread_id": parent, "agent_path": "/root/review"}}}}


class PanelTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def log(self, name="child", parent="root"):
        path = self.root / ("rollout-" + name + ".jsonl")
        path.write_text(record("session_meta", meta(name, parent)))
        return path

    def test_partial_line_and_incremental_read(self):
        path = self.log()
        with path.open("a") as stream:
            stream.write('{"type":"event_msg","payload":{"type":"task_started"}')
        tail = panel.Tail(path)
        tail.poll()
        self.assertEqual(tail.state, "sin datos")
        with path.open("a") as stream:
            stream.write('}\n')
        tail.poll()
        self.assertEqual(tail.state, "trabajando")
        old = tail.offset
        tail.poll()
        self.assertEqual(tail.offset, old)

    def test_public_transcript_stream_is_owned_and_incremental(self):
        path = self.log()
        with path.open("a") as stream:
            stream.write(record("session_meta", meta("root")))
            stream.write(record("response_item", {"type": "function_call", "name": "exec", "call_id": "parent-call", "arguments": '{"cmd":"PARENT_ONLY"}'}))
            stream.write(record("event_msg", {"type": "thread_settings_applied", "thread_id": "child"}))
            stream.write(record("response_item", {"type": "function_call", "name": "exec", "call_id": "child-call", "arguments": '{"cmd":"printf hello"}'}))
        tail = panel.Tail(path)
        tail.poll()
        before = json.dumps(tail.summary()["transcript"])
        self.assertIn("printf hello", before)
        self.assertNotIn("PARENT_ONLY", before)
        with path.open("a") as stream:
            output = record("response_item", {"type": "function_call_output", "call_id": "child-call", "output": "hello\n    original indentation"})
            stream.write(output[:-1])
        tail.poll()
        self.assertEqual(before, json.dumps(tail.summary()["transcript"]))
        with path.open("a") as stream: stream.write("\n")
        tail.poll()
        after = tail.summary()["transcript"]
        self.assertIn("hello\n    original indentation", "\n".join(e["body"] for e in after))
        tail.poll()
        self.assertEqual(after, tail.summary()["transcript"])

    def test_fork_metadata_does_not_replace_child(self):
        path = self.log()
        with path.open("a") as stream:
            stream.write(record("session_meta", meta("root")))
            stream.write(record("turn_context", {"model": "gpt-5.6-sol", "effort": "high"}))
            stream.write(record("event_msg", {"type": "thread_settings_applied", "thread_id": "child"}))
            stream.write(record("turn_context", {"model": "gpt-5.6-terra", "effort": "medium"}))
        tail = panel.Tail(path)
        tail.poll()
        self.assertEqual(tail.meta["id"], "child")
        self.assertEqual(tail.model, "gpt-5.6-terra")

    def test_ignore_reasoning_and_parent_items(self):
        tail = panel.Tail(self.log())
        for item in [{"type": "Reasoning", "raw_content": "PRIVATE"},
                     {"type": "reasoning", "summary": "PRIVATE"}]:
            tail.consume({"type": "event_msg", "payload": {"type": "item_completed", "item": item}})
        tail.consume({"type": "event_msg", "payload": {"type": "item_completed", "thread_id": "root",
                     "item": {"type": "AgentMessage", "content": "PARENT"}}})
        self.assertEqual(tail.activity, "Esperando actividad registrada")

    def test_completion_followup_and_abort(self):
        tail = panel.Tail(self.log())
        for event in ["task_started", "task_complete"]:
            tail.consume({"type": "event_msg", "payload": {"type": event}})
        self.assertEqual(tail.state, "completado")
        tail.consume({"type": "event_msg", "payload": {"type": "task_started"}})
        self.assertEqual(tail.state, "trabajando")
        self.assertIsNone(tail.ended)
        tail.consume({"type": "event_msg", "payload": {"type": "turn_aborted"}})
        self.assertEqual(tail.state, "interrumpido")

    def test_rotation_and_malformed_record(self):
        path = self.log()
        with path.open("a") as stream:
            stream.write("invalid json\n" + record("event_msg", {"type": "task_complete"}))
        tail = panel.Tail(path)
        tail.poll()
        self.assertEqual(tail.state, "completado")
        path.write_text(record("session_meta", meta("child", "root")))
        tail.poll()
        self.assertEqual(tail.state, "sin datos")

    def test_controls_and_secrets(self):
        text = panel.clean("\x1b[31mhello\x1b[0m\x00 password=hunter2 token=abc Bearer ABC.123")
        self.assertNotIn("\x1b", text)
        self.assertNotIn("hunter2", text)
        self.assertNotIn("ABC.123", text)

    def test_readonly_db_and_exact_parent(self):
        db = self.root / "state_5.sqlite"
        with sqlite3.connect(db) as conn:
            conn.execute("CREATE TABLE threads (id TEXT, rollout_path TEXT, source TEXT)")
            for ident, p in [("root", None), ("child", "root"), ("other", "unrelated")]:
                m = meta(ident, p)
                source = m["source"] if isinstance(m["source"], str) else json.dumps(m["source"])
                conn.execute("INSERT INTO threads VALUES (?,?,?)", (ident, ident + ".jsonl", source))
        before = db.read_bytes()
        self.assertCountEqual(panel.stored_paths(self.root, "root"), ["root.jsonl", "child.jsonl"])
        self.assertEqual(before, db.read_bytes())

    def test_process_fd_readers_are_not_owners(self):
        path = self.log("root", None)
        with path.open("rb"):
            self.assertNotIn(str(path), panel.process_files(os.getpid()))

    def test_inherited_events_and_analysis_are_hidden(self):
        tail = panel.Tail(self.log())
        for kind, payload in [
            ("session_meta", meta("root")),
            ("event_msg", {"type": "task_complete"}),
            ("turn_context", {"model": "WRONG"}),
            ("response_item", {"type": "message", "role": "assistant", "content": [{"text": "PARENT"}]}),
            ("event_msg", {"type": "thread_settings_applied", "thread_id": "child"}),
            ("response_item", {"type": "message", "role": "assistant", "channel": "analysis", "content": [{"text": "PRIVATE"}]}),
        ]:
            tail.consume({"type": kind, "payload": payload})
        self.assertEqual(tail.model, "?")
        self.assertEqual(tail.state, "sin datos")
        self.assertEqual(tail.activity, "Esperando actividad registrada")

    def test_command_arguments_are_never_shown(self):
        tail = panel.Tail(self.log())
        tail.item({"type": "CommandExecution", "command": ["bash", "-c", "curl --password PRIVATE"]})
        self.assertEqual(tail.activity, "Comando: bash · en curso")

    def test_live_append_updates_status(self):
        path = self.log()
        tail = panel.Tail(path)
        tail.poll()
        for event, expected in [("task_started", "trabajando"), ("task_complete", "completado")]:
            with path.open("a") as stream:
                stream.write(record("event_msg", {"type": event, "thread_id": "child"}))
            tail.poll()
            self.assertEqual(tail.state, expected)

    def test_duplicate_rollout_paths_do_not_duplicate_agents(self):
        root = self.log("root", None)
        child = self.log()
        duplicate = self.root / "copy.jsonl"
        duplicate.write_bytes(child.read_bytes())
        with patch.object(panel, "stored_paths", return_value=[str(root), str(child), str(duplicate)]):
            result = panel.Monitor(self.root, thread="root").refresh()
        self.assertEqual(len(result["agents"]), 1)

    def test_failed_turn_keeps_runtime(self):
        tail = panel.Tail(self.log())
        tail.consume({"type": "event_msg", "timestamp": "2026-01-01T00:00:00Z", "payload": {"type": "task_started"}})
        tail.consume({"type": "event_msg", "timestamp": "2026-01-01T00:00:50Z", "payload": {"type": "turn_failed"}})
        self.assertEqual(tail.summary()["seconds"], 50)

    def test_owner_exit_freezes_runtime(self):
        root = self.log("root", None)
        with root.open("a") as stream:
            stream.write(record("event_msg", {"type": "task_started"}))
        with patch.object(panel, "stored_paths", return_value=[str(root)]), \
             patch.object(panel, "process_identity", return_value=None), \
             patch.object(panel.time, "time", return_value=1788469400):
            monitor = panel.Monitor(self.root, pid=123, thread="root")
            first = monitor.refresh()["principal"]["seconds"]
        with patch.object(panel, "stored_paths", return_value=[str(root)]), \
             patch.object(panel, "process_identity", return_value=None), \
             patch.object(panel.time, "time", return_value=1788469900):
            self.assertEqual(monitor.refresh()["principal"]["seconds"], first)

    def test_monitor_never_uses_other_roots_children(self):
        root = self.log("root", None)
        child = self.log()
        other = self.log("other", "another-root")
        with patch.object(panel, "stored_paths", return_value=[str(root), str(child), str(other)]):
            result = panel.Monitor(self.root, thread="root").refresh()
        self.assertEqual([a["id"] for a in result["agents"]], ["child"])

    def test_new_session_resets_children(self):
        root = self.log("root", None)
        new = self.log("new", None)
        new.write_text(record("session_meta", meta("new", timestamp="2026-09-03T22:00:00Z")))
        with patch.object(panel, "process_identity", return_value="same"), \
             patch.object(panel, "stored_paths", return_value=[]), \
             patch.object(panel, "process_files", side_effect=[{str(root)}, {str(root), str(new)}]):
            monitor = panel.Monitor(self.root, pid=99)
            self.assertEqual(monitor.refresh()["thread"], "root")
            self.assertEqual(monitor.refresh()["thread"], "new")

    def test_narrow_render_and_missing_database(self):
        snapshot = panel.Monitor(self.root, thread="missing").refresh()
        self.assertTrue(panel.lines(snapshot, 28))
        self.assertEqual(snapshot["agents"], [])


if __name__ == "__main__":
    unittest.main()
