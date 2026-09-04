"""Public-only synthetic fixtures matching the inspected Popper rollout schemas.

Run: python -B -m unittest -v test_transcript
No fixture files, real conversation bodies, or filesystem writes are needed.
"""

import json
import shlex
import unittest

from transcript import Transcript, safe_text


STAMP = "2026-09-03T21:32:03.153Z"


def response(kind, **fields):
    return {"timestamp": STAMP, "type": "response_item",
            "payload": {"type": kind, **fields}}


def event(kind, **fields):
    return {"timestamp": STAMP, "type": "event_msg",
            "payload": {"type": kind, **fields}}


def item(kind, **fields):
    return event("item_completed", item={"type": kind, **fields})


class TranscriptTests(unittest.TestCase):
    def test_import_contract_and_detached_snapshot(self):
        transcript = Transcript()
        self.assertEqual(transcript.events, [])
        self.assertIsNone(transcript.consume(event("task_started", turn_id="turn")))
        row = transcript.events[0]
        self.assertEqual(row["timestamp"], STAMP)
        self.assertEqual(row["title"], "task_started")
        self.assertTrue({"timestamp", "kind", "title", "body", "call_id"} <= row.keys())
        row["event_types"].clear()
        row["body"] = "outside mutation"
        self.assertEqual(transcript.events[0]["body"], "")
        self.assertEqual(transcript.events[0]["event_types"], ["task_started"])

    def test_function_and_custom_calls_exact_text(self):
        transcript = Transcript()
        arguments = '{\n  "command": "echo hola",\n  "limit": 0\n}'
        code = "const result = await tools.exec_command({\n\tcmd: 'echo hola'\n});\ntext(result);\n"
        transcript.consume(response("function_call", id="fc1", call_id="one",
                                    name="exec_command", arguments=arguments))
        transcript.consume(response("function_call_output", id="fco1", call_id="one",
                                    output="  hola\n\n"))
        transcript.consume(response("custom_tool_call", id="ctc1", call_id="two",
                                    name="exec", input=code))
        transcript.consume(response("custom_tool_call_output", id="ctco1", call_id="two",
                                    output=[{"type": "input_text", "text": "  uno\n"},
                                            {"type": "input_text", "text": "\tdos\n"}]))
        self.assertEqual([e["body"] for e in transcript.events],
                         [arguments, "  hola\n\n", code, "  uno\n\tdos\n"])
        self.assertEqual([e["call_id"] for e in transcript.events], ["one", "one", "two", "two"])

    def test_same_call_mirrors_update_and_repeated_calls_survive(self):
        transcript = Transcript()
        for _ in range(2):
            transcript.consume(response("custom_tool_call", id="ctc1", call_id="a",
                                        name="exec", input="same()"))
        for output in ("first\n", "first\nsecond\n", "first\n"):
            transcript.consume(response("custom_tool_call_output", call_id="a", output=output))
        transcript.consume(response("custom_tool_call", call_id="b", name="exec", input="same()"))
        transcript.consume(response("custom_tool_call_output", call_id="b", output="first\n"))
        self.assertEqual(len(transcript.events), 4)
        self.assertEqual(transcript.events[1]["body"], "first\nsecond\n")
        self.assertEqual(transcript.events[3]["body"], "first\n")

    def test_unidentified_identical_messages_are_not_deduped(self):
        transcript = Transcript()
        for _ in range(2):
            transcript.consume(event("agent_message", message="same"))
        self.assertEqual(len(transcript.events), 2)

    def test_actual_public_agent_message_mirrors(self):
        transcript = Transcript()
        content = [{"type": "output_text", "text": "Mensaje original.\n\n  Indented\n"}]
        transcript.consume(item("AgentMessage", id="msg1", phase="commentary", content=content))
        transcript.consume(response("message", role="assistant", id="msg1",
                                    phase="commentary", content=content,
                                    internal_chat_message_metadata_passthrough={"encrypted_body": "HIDDEN"}))
        self.assertEqual(len(transcript.events), 1)
        self.assertEqual(transcript.events[0]["body"], content[0]["text"])
        self.assertEqual(transcript.events[0]["event_types"], ["AgentMessage", "message"])
        transcript.consume(response("message", role="assistant", id="msg2",
                                    phase="final_answer", content=content))
        self.assertEqual(len(transcript.events), 2)

    def test_exec_live_deltas_and_end_snapshot_not_doubled(self):
        transcript = Transcript()
        command = ["bash", "-c", "printf 'hello\\n'\n  echo fin"]
        transcript.consume(event("exec_command_begin", call_id="exec1", command=command, cwd="/work"))
        initial = transcript.events
        first = event("exec_command_output_delta", call_id="exec1", event_id="delta1",
                      stream="stdout", delta="hello\n")
        transcript.consume(first)
        transcript.consume(event("exec_command_output_delta", call_id="exec1", event_id="delta2",
                                 stream="stdout", delta="hello\n"))
        transcript.consume(first)
        self.assertIn("hello\nhello\n", transcript.events[0]["body"])
        self.assertNotIn("hello\nhello\nhello\n", transcript.events[0]["body"])
        self.assertNotIn("Stdout:", initial[0]["body"])
        transcript.consume(event("exec_command_output_delta", call_id="exec1", stream="stderr", delta="oops\n"))
        transcript.consume(event("exec_command_end", call_id="exec1", exit_code=1,
                                 stdout="hello\nhello\n", stderr="oops\n",
                                 aggregated_output="hello\nhello\noops\n"))
        row = transcript.events[0]
        self.assertEqual(len(transcript.events), 1)
        self.assertIn(shlex.join(command), row["body"])
        self.assertEqual(row["body"].count("hello\n"), 2)
        self.assertEqual(row["body"].count("oops\n"), 1)
        self.assertIn("Exit code:\n1", row["body"])
        self.assertEqual(row["title"], "exec_command_end")

    def test_identical_unidentified_chunks_are_legitimate(self):
        transcript = Transcript()
        for _ in range(3):
            transcript.consume(event("exec_command_output_delta", call_id="exec1", delta="x\n"))
        self.assertIn("x\nx\nx\n", transcript.events[0]["body"])

    def test_same_delta_id_with_updated_text_is_not_discarded(self):
        transcript = Transcript()
        transcript.consume(event("exec_command_output_delta", call_id="exec1", event_id="delta1", delta="a"))
        transcript.consume(event("exec_command_output_delta", call_id="exec1", event_id="delta1", delta="b"))
        self.assertIn("ab", transcript.events[0]["body"])

    def test_command_item_core_schema_and_explicit_alias_bridge(self):
        transcript = Transcript()
        transcript.consume(event("exec_command_begin", call_id="call1", command="echo done"))
        transcript.consume(item("CommandExecution", id="exec-item", call_id="call1",
                                command=["bash", "-c", "echo done"], cwd="/work",
                                stdout="done\n", stderr="", aggregated_output="done\n",
                                formatted_output="Wall time: 0.1\nOutput:\ndone\n",
                                exit_code=0, duration={"secs": 0, "nanos": 100}, status="completed"))
        transcript.consume(item("CommandExecution", id="exec-item", stdout="done\nmore\n",
                                aggregated_output="done\nmore\n"))
        self.assertEqual(len(transcript.events), 1)
        self.assertEqual(transcript.events[0]["call_id"], "call1")
        body = transcript.events[0]["body"]
        self.assertIn("done\nmore\n", body)
        self.assertEqual(body.count("done\n"), 1)
        self.assertIn("Working directory:\n/work", body)
        self.assertIn("Status:\ncompleted", body)
        self.assertIn("Exit code:\n0", body)

    def test_item_output_before_command_and_formatted_fallback(self):
        transcript = Transcript()
        transcript.consume(item("CommandExecution", id="exec1", formatted_output="  complete output\n"))
        transcript.consume(item("CommandExecution", id="exec1", command="echo hi"))
        self.assertEqual(len(transcript.events), 1)
        self.assertIn("  complete output\n", transcript.events[0]["body"])
        self.assertIn("echo hi", transcript.events[0]["body"])

    def test_public_only_no_reasoning_instructions_or_transport(self):
        transcript = Transcript()
        for outer, payload in [
            ("session_meta", {"base_instructions": "HIDDEN"}),
            ("turn_context", {"instructions": "HIDDEN"}),
            ("response_item", {"type": "reasoning", "summary": "HIDDEN", "encrypted_content": "HIDDEN"}),
            ("response_item", {"type": "message", "role": "developer", "content": "HIDDEN"}),
            ("response_item", {"type": "message", "role": "user", "content": "HIDDEN"}),
            ("response_item", {"type": "message", "role": "assistant", "channel": "analysis", "content": "HIDDEN"}),
            ("event_msg", {"type": "item_completed", "item": {"type": "Reasoning", "raw_content": "HIDDEN"}}),
            ("event_msg", {"type": "agent_reasoning", "text": "HIDDEN"}),
        ]:
            transcript.consume({"type": outer, "payload": payload})
        self.assertEqual(transcript.events, [])
        transcript.consume(response("message", role="assistant", id="public", content=[
            {"type": "output_text", "text": "VISIBLE\n"},
            {"type": "output_text", "channel": "analysis", "text": "HIDDEN"},
            {"type": "reasoning", "text": "HIDDEN"},
            {"type": "encrypted_content", "text": "HIDDEN"},
        ]))
        self.assertEqual(transcript.events[0]["body"], "VISIBLE\n")
        transcript.consume(response("function_call_output", call_id="call", output={
            "result": "VISIBLE", "encrypted_body": "HIDDEN", "instructions": "HIDDEN",
            "nested": {"reasoning": "HIDDEN"}}))
        self.assertNotIn("HIDDEN", json.dumps(transcript.events))

    def test_fork_scope_is_callers_responsibility(self):
        # Model Tail's ownership gate. session_id is shared and NOT identity.
        transcript = Transcript()
        own, owner = "child", "child"
        records = [
            {"type": "session_meta", "payload": {"id": "child", "session_id": "parent"}},
            {"type": "session_meta", "payload": {"id": "parent"}},
            response("message", role="assistant", content="INHERITED"),
            event("agent_message", thread_id="parent", message="OTHER THREAD"),
            event("agent_message", thread_id="child", message="OWN PUBLIC"),
            response("custom_tool_call", call_id="owned", input="owned()"),
        ]
        for record in records:
            payload = record["payload"]
            if record["type"] == "session_meta":
                owner = payload["id"]
                continue
            if payload.get("thread_id") not in (None, own):
                continue
            if record["type"] == "event_msg" and payload.get("thread_id") == own:
                owner = own
            if owner == own:
                transcript.consume(record)
        self.assertEqual([r["body"] for r in transcript.events], ["OWN PUBLIC", "owned()"])

    def test_live_append_complete_records_in_order(self):
        transcript = Transcript()
        lines = [json.dumps(response("custom_tool_call", call_id="a", input="run()")) + "\n",
                 json.dumps(response("custom_tool_call_output", call_id="a", output="one\n")) + "\n"]
        pending = ""
        # Tail owns incomplete-line buffering, not Transcript.
        for chunk in [lines[0], lines[1][:25], lines[1][25:]]:
            pending += chunk
            while "\n" in pending:
                line, pending = pending.split("\n", 1)
                transcript.consume(json.loads(line))
        self.assertEqual([r["body"] for r in transcript.events], ["run()", "one\n"])

    def test_lifecycle_labels_and_timestamps(self):
        transcript = Transcript()
        for label in ("task_started", "turn_aborted", "error", "task_complete"):
            record = event(label, message="raw public message", instructions="HIDDEN")
            record["timestamp"] = 1750000000.25
            transcript.consume(record)
        self.assertEqual([e["title"] for e in transcript.events],
                         ["task_started", "turn_aborted", "error", "task_complete"])
        self.assertTrue(all(e["timestamp"] == 1750000000.25 for e in transcript.events))
        self.assertNotIn("HIDDEN", json.dumps(transcript.events))

    def test_final_message_completion_with_explicit_message_id(self):
        transcript = Transcript()
        transcript.consume(response("message", role="assistant", id="msg-final",
                                    phase="final_answer", content="done\n"))
        transcript.consume(event("task_complete", last_agent_message="done\n",
                                 last_agent_message_id="msg-final"))
        messages = [e for e in transcript.events if e["kind"] == "assistant"]
        self.assertEqual(len(messages), 1)
        self.assertEqual(messages[0]["body"], "done\n")

    def test_immediate_completion_mirror_same_turn_only(self):
        transcript = Transcript()
        transcript.consume(event("task_started", turn_id="turn1"))
        transcript.consume(item("AgentMessage", id="final1", phase="final_answer", content="done"))
        transcript.consume(response("message", id="final1", role="assistant",
                                    phase="final_answer", content="done"))
        transcript.consume(event("task_complete", turn_id="turn1", last_agent_message="done"))
        self.assertEqual(len([e for e in transcript.events if e["kind"] == "assistant"]), 1)
        transcript.consume(event("task_started", turn_id="turn2"))
        transcript.consume(event("task_complete", turn_id="turn2", last_agent_message="done"))
        transcript.consume(response("message", id="legitimate", role="assistant", content="done"))
        self.assertEqual(len([e for e in transcript.events if e["kind"] == "assistant"]), 3)

    def test_final_item_turn_survives_response_mirror_without_turn(self):
        transcript = Transcript()
        final = item("AgentMessage", id="final", phase="final_answer", content="done")
        final["payload"]["turn_id"] = "owned-turn"
        transcript.consume(final)
        transcript.consume(response("message", id="final", role="assistant", phase="final_answer", content="done"))
        transcript.consume(event("task_complete", turn_id="owned-turn", last_agent_message="done"))
        self.assertEqual(len([e for e in transcript.events if e["kind"] == "assistant"]), 1)

    def test_command_duration_ms_has_explicit_unit(self):
        transcript = Transcript()
        transcript.consume(event("exec_command_end", call_id="a", duration_ms=125, output="done"))
        self.assertIn("Duration (ms):\n125", transcript.events[0]["body"])

    def test_no_covert_default_truncation(self):
        transcript = Transcript()
        body = "  original output\n" * 10000
        transcript.consume(response("function_call_output", call_id="a", output=body))
        self.assertEqual(transcript.events[0]["body"], body)

    def test_memory_and_indexes_bounded_with_explicit_omissions(self):
        transcript = Transcript(max_bytes=4096, max_events=3)
        for number in range(100):
            transcript.consume(response("function_call_output", call_id=str(number),
                                        output="line\n" * 80))
        self.assertEqual(transcript.events[0]["kind"], "omission")
        self.assertLessEqual(len(transcript._entries), 3)
        self.assertLessEqual(len(transcript._aliases), 3)
        self.assertLessEqual(transcript._bytes + len(transcript._seen) * 128 + 512, 4096)
        transcript.consume(response("function_call_output", call_id="huge", output="á" * 20000 + "END\n"))
        visible = "\n".join(e["body"] for e in transcript.events)
        self.assertIn("OMITTED", visible)
        self.assertIn("END\n", visible)
        self.assertLessEqual(transcript._bytes + len(transcript._seen) * 128 + 512, 4096)

    def test_control_and_secret_split_across_deltas(self):
        transcript = Transcript()
        for chunk in ("\x1b[", "31mred\x1b[0m\nAPI_", "KEY=secret", "suffix\n"):
            transcript.consume(event("exec_command_output_delta", call_id="x", delta=chunk))
        body = transcript.events[0]["body"]
        self.assertIn("red\nAPI_KEY=[REDACTED]\n", body)
        self.assertNotIn("secret", body)
        self.assertNotIn("suffix", body)
        self.assertNotIn("\x1b", body)

    def test_malformed_and_unrecognized_envelopes_ignored(self):
        transcript = Transcript()
        for record in (None, [], "bad", {}, {"payload": []},
                       event("item_completed", item=None), event("unknown", body="HIDDEN")):
            transcript.consume(record)
        self.assertEqual(transcript.events, [])


class SanitizerTests(unittest.TestCase):
    def test_layout_and_language_not_normalized(self):
        body = "Token share\n\n  español 中文\n\ttoken count\n"
        self.assertEqual(safe_text(body), body)

    def test_terminal_controls(self):
        body = "\x1b[31mred\x1b[0m\n\x1b]0;hidden title\x07ok\t!\x00\x08\r"
        self.assertEqual(safe_text(body), "red\nok\t!")
        self.assertEqual(safe_text("a\x1b]52;c;clipboard\x1b\\b"), "ab")
        self.assertEqual(safe_text("a\x1bPprivate\x1b\\b"), "ab")
        self.assertEqual(safe_text("a\x9b31mb\x9b0m"), "ab")
        self.assertEqual(safe_text("a\x1b]unfinished"), "a")

    def test_known_assignment_redaction_only(self):
        text = ('Token share token count tokenization bearer prose\n'
                'OPENAI_API_KEY=abc password="two words"; token: value\n'
                '{"api_key": "secret", "limit": 7}\n')
        safe = safe_text(text)
        self.assertIn("Token share token count tokenization bearer prose\n", safe)
        self.assertIn('OPENAI_API_KEY=[REDACTED] password="[REDACTED]"; token: [REDACTED]', safe)
        self.assertIn('{"api_key": "[REDACTED]", "limit": 7}', safe)
        self.assertNotIn("two words", safe)


if __name__ == "__main__":
    unittest.main()
