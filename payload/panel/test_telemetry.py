"""Run: python -B -m unittest -v test_telemetry (from this directory)."""

import json
from pathlib import Path
import unittest

from telemetry import Telemetry


def counts(input=100, cached=60, output=20, reasoning=5):
    return {"input_tokens": input, "cached_input_tokens": cached,
            "output_tokens": output, "reasoning_output_tokens": reasoning,
            "total_tokens": input + output}


def request(response="r1", usage=None, total=None, ordinal=1):
    payload = {"thread_id": "child", "session_id": "main",
               "response_id": response, "usage": usage or counts()}
    if total is not None:
        payload["thread_token_usage"] = total
    return {"type": "token_usage_record", "ordinal": ordinal, "payload": payload}


def snapshot(total=None, ordinal=2, last=None, window=None):
    info = {"total_token_usage": total if total is not None else counts(),
            "last_token_usage": last if last is not None else counts()}
    if window is not None:
        info["model_context_window"] = window
    return {"type": "event_msg", "ordinal": ordinal,
            "payload": {"type": "token_count", "info": info}}


def message(id="m1", sender="/root", recipient="/root/child", timestamp="now"):
    return {"type": "response_item", "timestamp": timestamp,
            "payload": {"type": "agent_message", "id": id, "author": sender,
                        "recipient": recipient, "content": "DO NOT READ",
                        "internal_chat_message_metadata_passthrough": "OPAQUE"}}


class TelemetryTests(unittest.TestCase):
    def test_empty_and_malformed_are_unknown(self):
        t = Telemetry("main")
        for record in (None, [], "bad", {}, {"payload": []},
                       {"type": "event_msg", "payload": {"type": "token_count", "info": None}},
                       snapshot({"input_tokens": True, "output_tokens": -1,
                                 "total_tokens": "20", "cached_input_tokens": 1.5})):
            t.consume(record)
        for key in ("tokens", "input_tokens", "cached_input_tokens", "output_tokens",
                    "reasoning_output_tokens"):
            self.assertIsNone(t.summary()[key])

    def test_zero_is_known(self):
        t = Telemetry("main")
        t.consume(snapshot(counts(0, 0, 0, 0)))
        self.assertEqual(t.summary()["tokens"], 0)

    def test_inclusive_counts_do_not_add_subsets(self):
        t = Telemetry("child")
        t.consume(request(total=counts()))
        self.assertEqual(t.summary(), {
            "identity": "child", "tokens": 120, "input_tokens": 100,
            "cached_input_tokens": 60, "output_tokens": 20,
            "reasoning_output_tokens": 5, "context_input_tokens": None,
            "model_context_window": None, "communication_events": []})

    def test_context_input_uses_latest_token_count_not_cumulative_total(self):
        t = Telemetry("main")
        t.consume(snapshot({"total_tokens": 1_406_890}, ordinal=4,
                           last={"input_tokens": 127_593}, window=258_400))
        t.consume(snapshot({"total_tokens": 4}, ordinal=3,
                           last={"input_tokens": 4}, window=100))
        summary = t.summary()
        self.assertEqual(summary["context_input_tokens"], 127_593)
        self.assertEqual(summary["model_context_window"], 258_400)

    def test_repeated_and_reordered_snapshots_never_add(self):
        records = [request(total=counts()), snapshot(), snapshot(),
                   request("r2", total=counts(200, 120, 40, 10), ordinal=4),
                   snapshot(counts(200, 120, 40, 10), ordinal=5)]
        for sequence in (records, list(reversed(records)), records * 3):
            t = Telemetry("child")
            for record in sequence:
                t.consume(record)
            self.assertEqual(t.summary()["tokens"], 240)
            self.assertEqual(t.summary()["reasoning_output_tokens"], 10)

    def test_unique_requests_with_identical_counts(self):
        t = Telemetry("child")
        for record in (request(), request(), request("r2", ordinal=3)):
            t.consume(record)
        self.assertEqual(t.summary()["tokens"], 240)

    def test_response_id_dedup_ignores_changed_timestamp_and_ordinal(self):
        t = Telemetry("child")
        t.consume(request())
        t.consume(request(ordinal=100))
        self.assertEqual(t.summary()["tokens"], 120)

    def test_snapshot_after_request_deltas_does_not_double_count(self):
        t = Telemetry("child")
        t.consume(request())
        t.consume(request("r2", ordinal=3))
        t.consume(snapshot(counts(200, 120, 40, 10), ordinal=4))
        t.consume(request("r2", ordinal=3))
        self.assertEqual(t.summary()["tokens"], 240)

    def test_lagging_first_snapshot_does_not_erase_request_usage(self):
        t = Telemetry("child")
        t.consume(request())
        t.consume(request("r2", ordinal=3))
        t.consume(snapshot())
        self.assertEqual(t.summary()["tokens"], 240)
        t.consume(request("r3", ordinal=5))
        self.assertEqual(t.summary()["tokens"], 360)

    def test_uncorrelated_delta_after_snapshot_is_unknown_until_covered(self):
        t = Telemetry("child")
        t.consume(snapshot())
        t.consume(request("r2", ordinal=3))
        self.assertIsNone(t.summary()["tokens"])
        t.consume(snapshot())  # A replay cannot resolve the ambiguity.
        self.assertIsNone(t.summary()["tokens"])
        t.consume(snapshot(counts(200, 120, 40, 10), ordinal=4))
        self.assertEqual(t.summary()["tokens"], 240)

    def test_last_usage_and_turn_total_are_not_lifetime_snapshots(self):
        t = Telemetry("child")
        t.consume(snapshot({}))
        self.assertIsNone(t.summary()["tokens"])
        record = request()
        record["payload"]["turn_token_usage"] = counts(900, 0, 100, 0)
        t.consume(record)
        self.assertEqual(t.summary()["tokens"], 120)

    def test_new_turn_does_not_reset_thread_total(self):
        t = Telemetry("child")
        t.consume(request(total=counts()))
        second = request("r2", total=counts(200, 120, 40, 10), ordinal=3)
        second["payload"]["turn_id"] = "second-turn"
        second["payload"]["turn_token_usage"] = counts()
        t.consume(second)
        self.assertEqual(t.summary()["tokens"], 240)

    def test_missing_detail_is_not_zero(self):
        t = Telemetry("child")
        t.consume(request(usage={"input_tokens": 10, "output_tokens": 2}))
        t.consume(request("r2", ordinal=3))
        self.assertEqual(t.summary()["tokens"], 132)
        self.assertIsNone(t.summary()["cached_input_tokens"])
        self.assertIsNone(t.summary()["reasoning_output_tokens"])

    def test_total_only_snapshot(self):
        t = Telemetry("main")
        t.consume(snapshot({"total_tokens": 50}))
        self.assertEqual(t.summary()["tokens"], 50)
        self.assertIsNone(t.summary()["input_tokens"])

    def test_new_snapshot_missing_detail_does_not_reuse_old_detail(self):
        t = Telemetry("main")
        t.consume(snapshot())
        t.consume(snapshot({"total_tokens": 240}, ordinal=4))
        self.assertEqual(t.summary()["tokens"], 240)
        self.assertIsNone(t.summary()["input_tokens"])
        self.assertIsNone(t.summary()["cached_input_tokens"])
        t.consume(snapshot())  # Old detail is not detail for the new total.
        self.assertIsNone(t.summary()["input_tokens"])
        t.consume(snapshot(counts(200, 120, 40, 10), ordinal=5))
        self.assertEqual(t.summary()["input_tokens"], 200)

    def test_unidentified_delta_does_not_leave_a_partial_sum(self):
        t = Telemetry("main")
        t.consume(request())
        unidentified = request(None)
        del unidentified["ordinal"]
        t.consume(unidentified)
        t.consume(request("r3", ordinal=3))
        self.assertIsNone(t.summary()["tokens"])
        t.consume(snapshot(counts(300, 180, 60, 15), ordinal=4))
        self.assertEqual(t.summary()["tokens"], 360)

    def test_several_ambiguous_deltas_need_a_covering_snapshot(self):
        t = Telemetry("main")
        t.consume(snapshot())
        t.consume(request("r2", ordinal=3))
        t.consume(request("r3", ordinal=4))
        t.consume(snapshot(counts(200, 120, 40, 10), ordinal=5))
        self.assertIsNone(t.summary()["tokens"])
        t.consume(snapshot(counts(300, 180, 60, 15), ordinal=6))
        self.assertEqual(t.summary()["tokens"], 360)

    def test_request_without_identity_does_not_guess(self):
        t = Telemetry("main")
        record = request(None)
        del record["ordinal"]
        t.consume(record)
        self.assertIsNone(t.summary()["tokens"])

    def test_bidirectional_communication_and_replay(self):
        t = Telemetry("main")
        out = message()
        back = message("m2", "/root/child", "/root", "later")
        for record in (out, back, out, back):
            t.consume(record)
        events = t.summary()["communication_events"]
        self.assertEqual(events, [
            {"id": "m1", "from": "/root", "to": "/root/child", "timestamp": "now"},
            {"id": "m2", "from": "/root/child", "to": "/root", "timestamp": "later"}])
        events[0]["from"] = "mutated"
        self.assertEqual(t.summary()["communication_events"][0]["from"], "/root")
        self.assertNotIn("OPAQUE", repr(t.summary()))

    def test_bodies_are_never_accessed(self):
        class MetadataOnly(dict):
            def get(self, key, default=None):
                if key in ("content", "internal_chat_message_metadata_passthrough"):
                    raise AssertionError("message body accessed")
                return super().get(key, default)
        t = Telemetry("main")
        record = message(sender="main-id", recipient="child-id", timestamp=None)
        record["payload"] = MetadataOnly(record["payload"])
        t.consume(record)
        self.assertIsNone(t.summary()["communication_events"][0]["timestamp"])

    def test_events_bounded_and_evicted_replay_not_reinserted(self):
        t = Telemetry("main")
        for i in range(t.EVENT_LIMIT + 10):
            t.consume(message(str(i)))
        t.consume(message("0"))
        events = t.summary()["communication_events"]
        self.assertEqual(len(events), t.EVENT_LIMIT)
        self.assertEqual(events[0]["id"], "10")

    def test_metadata_without_endpoints_and_tools_are_not_messages(self):
        t = Telemetry("main")
        for record in ({"type": "inter_agent_communication_metadata",
                        "payload": {"trigger_turn": True}},
                       {"type": "response_item", "payload": {"type": "function_call",
                        "name": "send_message", "arguments": "DO NOT READ"}},
                       message(sender={"id": "not-a-string"})):
            t.consume(record)
        self.assertEqual(t.summary()["communication_events"], [])

    def test_rate_limits_are_not_agent_cost(self):
        t = Telemetry("main")
        record = snapshot()
        record["payload"]["rate_limits"] = {"primary": {"used_percent": 13.0}}
        t.consume(record)
        self.assertFalse(any("quota" in key or "cost" in key or "percent" in key
                             for key in t.summary()))


class ScopedRolloutTests(unittest.TestCase):
    """Optional read-only integration test; never searches other session logs."""

    def test_exact_scoped_rollouts(self):
        base = Path.home() / ".codex/sessions/2026/09/03"
        examples = [
            ("26-01a0691a-eaf4-7290-9f43-f92cced63291", (458104, 412032, 4604, 1393), 4),
            ("36-01a0691b-11c9-7b53-a095-ba50dd28d936", (328951, 270336, 1852, 277), 1),
            ("42-01a0691b-25f5-75a1-8ec3-a486b7c0ed91", (345803, 306688, 2153, 288), 1),
        ]
        for suffix, expected, event_count in examples:
            path = base / ("rollout-2026-09-03T17-09-" + suffix + ".jsonl")
            if not path.is_file():
                self.skipTest("scoped example logs are not installed")
            with path.open() as stream:
                records = [json.loads(line) for line in stream]
            identity = records[0]["payload"]["id"]
            start = records[0]["payload"].get("subagent_history_start_ordinal", 0)
            # Simulate the caller's ownership filter, outside Telemetry.
            owned = [r for r in records if r.get("ordinal", -1) >= start]
            for mode in ("both", "usage_only", "counts_only"):
                with self.subTest(agent=identity, mode=mode):
                    t = Telemetry(identity)
                    for record in owned * 2:  # Entire-file replay too.
                        if mode == "usage_only" and record["type"] == "event_msg":
                            continue
                        if mode == "counts_only" and record["type"] == "token_usage_record":
                            continue
                        t.consume(record)
                    summary = t.summary()
                    actual = tuple(summary[k] for k in (
                        "input_tokens", "cached_input_tokens", "output_tokens",
                        "reasoning_output_tokens"))
                    self.assertEqual(actual, expected)
                    self.assertEqual(summary["tokens"], expected[0] + expected[2])
                    self.assertEqual(len(summary["communication_events"]), event_count)


if __name__ == "__main__":
    unittest.main()
