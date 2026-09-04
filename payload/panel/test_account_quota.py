"""Offline tests only: injected results and harmless Python stdio peers."""

import contextlib
import io
import os
import subprocess
import sys
import threading
import time
import unittest
from unittest.mock import Mock, patch

from account_quota import AccountQuota


def result(used=25, secondary=None):
    return {"rateLimits": {"primary": {"usedPercent": used,
            "windowDurationMins": 300, "resetsAt": 1800000000}, "secondary": secondary}}


class NormalizationTests(unittest.TestCase):
    def quota(self, payload):
        quota = AccountQuota("/exact/home", "selected", autostart=False,
                             fetch=lambda: payload)
        self.addCleanup(quota.close)
        quota._refresh()
        return quota

    def test_map_preferred_codex_only_both_windows(self):
        payload = result(99)
        payload["rateLimitsByLimitId"] = {
            "other": result(88)["rateLimits"],
            "codex": result(0, {"usedPercent": 100, "windowDurationMins": 10080,
                                 "resetsAt": 0})["rateLimits"]}
        state = self.quota(payload).snapshot()
        self.assertEqual(state["status"], "ok")
        self.assertEqual(state["windows"], [
            {"name": "primary", "used_percent": 0, "remaining_percent": 100,
             "window_minutes": 300, "resets_at": 1800000000},
            {"name": "secondary", "used_percent": 100, "remaining_percent": 0,
             "window_minutes": 10080, "resets_at": 0}])

    def test_legacy_fallback_missing_or_null_map(self):
        for payload in (result(), dict(result(), rateLimitsByLimitId=None)):
            self.assertEqual(self.quota(payload).snapshot()["windows"][0]
                             ["remaining_percent"], 75)

    def test_present_map_never_substitutes_legacy_or_other_bucket(self):
        for buckets in ({}, {"other": result()["rateLimits"]}, {"codex": None}, []):
            state = self.quota(dict(result(), rateLimitsByLimitId=buckets)).snapshot()
            self.assertEqual(state["status"], "unknown")
            self.assertEqual(state["windows"], [])

    def test_null_fields_and_zero_metadata_remain_distinct(self):
        payload = result(0, {"usedPercent": None, "windowDurationMins": 0,
                             "resetsAt": None})
        payload["rateLimits"]["primary"]["windowDurationMins"] = None
        windows = self.quota(payload).snapshot()["windows"]
        self.assertIsNone(windows[0]["window_minutes"])
        self.assertEqual(windows[0]["remaining_percent"], 100)
        self.assertIsNone(windows[1]["used_percent"])
        self.assertIsNone(windows[1]["remaining_percent"])
        self.assertEqual(windows[1]["window_minutes"], 0)
        self.assertIsNone(windows[1]["resets_at"])

    def test_missing_invalid_and_nonfinite_are_unknown_not_zero(self):
        for payload in ({}, None, [], {"rateLimits": None}, result(None),
                        result(True), result("0"), result(float("nan")),
                        result(float("inf")), {"rateLimits": {"primary": {}}}):
            state = self.quota(payload).snapshot()
            self.assertEqual(state["status"], "unknown")
            self.assertIsNone(state["updated_at"])
            self.assertEqual(state["windows"], [])

    def test_remaining_clamped_used_preserved(self):
        for used, remaining in ((-5, 100), (110, 0), (12.5, 87.5)):
            window = self.quota(result(used)).snapshot()["windows"][0]
            self.assertEqual(window["used_percent"], used)
            self.assertEqual(window["remaining_percent"], remaining)

    def test_failure_retains_stale_timestamp_and_recovery_clears_error(self):
        quota = self.quota(result())
        before = quota.snapshot()
        quota._fetch = Mock(side_effect=RuntimeError("SECRET service credentials"))
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            quota._refresh()
        stale = quota.snapshot()
        self.assertEqual(stale["status"], "stale")
        self.assertEqual(stale["windows"], before["windows"])
        self.assertEqual(stale["updated_at"], before["updated_at"])
        self.assertEqual(stale["error"], "quota_unavailable")
        self.assertEqual(output.getvalue(), "")
        self.assertNotIn("SECRET", repr(stale))
        quota._fetch = lambda: result(None)
        quota._refresh()
        self.assertEqual(quota.snapshot(), stale)
        quota._fetch = lambda: result(50)
        quota._refresh()
        self.assertEqual(quota.snapshot()["status"], "ok")
        self.assertNotIn("error", quota.snapshot())

    def test_snapshot_is_detached(self):
        quota = self.quota(result())
        quota.snapshot()["windows"][0]["used_percent"] = 999
        self.assertEqual(quota.snapshot()["windows"][0]["used_percent"], 25)


class LifecycleTests(unittest.TestCase):
    def test_disabled_lazy_single_worker_and_fast_snapshot(self):
        entered, release = threading.Event(), threading.Event()

        def fetch():
            entered.set()
            release.wait(2)
            return result()

        with AccountQuota("/home", fetch=fetch, autostart=False) as quota:
            self.assertEqual(quota.snapshot()["status"], "unknown")
            self.assertIsNone(quota._thread)
        with AccountQuota("/home", fetch=fetch) as quota:
            self.addCleanup(release.set)
            self.assertIsNone(quota._thread)
            quota.snapshot()
            self.assertTrue(entered.wait(1))
            worker = quota._thread
            started = time.monotonic()
            for _ in range(100):
                self.assertEqual(quota.snapshot()["status"], "unknown")
            self.assertLess(time.monotonic() - started, .2)
            self.assertIs(quota._thread, worker)
            release.set()
        self.assertFalse(worker.is_alive())
        quota.close()
        quota.snapshot()
        self.assertIs(quota._thread, worker)

    def test_poll_interval_is_30_seconds_without_real_wait(self):
        quota = AccountQuota("/home", autostart=False, fetch=Mock(return_value=result()))
        quota._stop = Mock()
        quota._stop.is_set.return_value = False
        quota._stop.wait.side_effect = [False, True]
        quota._run()
        self.assertEqual(quota._fetch.call_count, 2)
        self.assertEqual([c.args for c in quota._stop.wait.call_args_list], [(30.0,), (30.0,)])


class TransportTests(unittest.TestCase):
    def peer(self, script, stack=None):
        real_popen = subprocess.Popen
        processes = []

        def launch(command, **kwargs):
            self.assertEqual(command, ["codex", "app-server", "--listen", "stdio://"])
            self.assertEqual(kwargs["env"]["CODEX_HOME"], "/exact/quota home")
            self.assertEqual(kwargs["env"]["QUOTA_TEST_MARKER"], "inherited")
            self.assertEqual(kwargs["stderr"], subprocess.DEVNULL)
            process = real_popen([sys.executable, "-u", "-c", script], **kwargs)
            processes.append(process)
            return process

        enter = stack.enter_context if stack is not None else self.enterContext
        enter(patch.dict(os.environ, {"CODEX_HOME": "/wrong",
                                     "QUOTA_TEST_MARKER": "inherited"}))
        enter(patch("account_quota.subprocess.Popen", side_effect=launch))
        quota = AccountQuota("/exact/quota home", "chosen profile", autostart=False)
        quota.TIMEOUT_SECONDS = .3
        self.addCleanup(quota.close)
        return quota, processes

    def assert_reaped(self, processes):
        self.assertTrue(processes)
        for process in processes:
            self.assertIsNotNone(process.poll())
            self.assertTrue(process.stdin.closed)
            self.assertTrue(process.stdout.closed)

    def test_handshake_fragmented_frames_notifications_and_read_only_methods(self):
        script = '''
import json, os, sys
def read(): return json.loads(sys.stdin.readline())
def send(value): print(json.dumps(value), flush=True)
init = read()
assert init["method"] == "initialize" and "jsonrpc" not in init
assert init["params"]["clientInfo"]["name"] == "codex_panel_quota"
send({"method": "warning", "params": {"message": "SECRET"}})
send({"id": 999, "result": {}})
send({"id": init["id"], "result": {}})
assert read() == {"method": "initialized"}
request = read()
assert request == {"id": 2, "method": "account/rateLimits/read"}
encoded = (json.dumps({"id": 2, "result": PAYLOAD}) + "\\n").encode()
for byte in encoded: os.write(1, bytes([byte]))
sys.stdin.read()
'''.replace("PAYLOAD", repr(result(0)))
        quota, processes = self.peer(script)
        quota._refresh()
        self.assertEqual(quota.snapshot()["status"], "ok")
        self.assertEqual(quota.snapshot()["windows"][0]["remaining_percent"], 100)
        self.assert_reaped(processes)
        self.assertEqual(os.environ["CODEX_HOME"], "/wrong")

    def test_timeout_eof_malformed_oversized_and_rpc_error_are_sanitized(self):
        scripts = ["import time; time.sleep(10)", "pass",
                   "print('SECRET invalid json', flush=True)",
                   "print('x' * 20000, flush=True)",
                   'print(\'{"id":1,"error":{"message":"SECRET credentials"}}\', flush=True)']
        for script in scripts:
            with self.subTest(script=script), contextlib.ExitStack() as stack:
                quota, processes = self.peer(script, stack)
                quota.MAX_BYTES = 4096
                started = time.monotonic()
                quota._refresh()
                self.assertLess(time.monotonic() - started, 1)
                self.assertEqual(quota.snapshot(), {"windows": [], "updated_at": None,
                    "status": "unknown", "error": "quota_unavailable"})
                self.assert_reaped(processes)

    def test_read_deadline_after_initialize_and_force_kill(self):
        quota, processes = self.peer('''
import json, signal, sys, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
request = json.loads(sys.stdin.readline())
print(json.dumps({"id": request["id"], "result": {}}), flush=True)
assert json.loads(sys.stdin.readline())["method"] == "initialized"
assert json.loads(sys.stdin.readline())["method"] == "account/rateLimits/read"
time.sleep(10)
''')
        started = time.monotonic()
        quota._refresh()
        self.assertLess(time.monotonic() - started, 1)
        self.assertEqual(quota.snapshot()["status"], "unknown")
        self.assert_reaped(processes)
        self.assertEqual(processes[0].returncode, -9)

    def test_unavailable_executable_is_sanitized(self):
        quota = AccountQuota("/home", autostart=False)
        with patch("account_quota.subprocess.Popen", side_effect=OSError("SECRET")):
            quota._refresh()
        self.assertEqual(quota.snapshot(), {"windows": [], "updated_at": None,
                         "status": "unknown", "error": "quota_unavailable"})

    def test_write_backpressure_obeys_deadline(self):
        quota = AccountQuota("/home", autostart=False)
        process = Mock()
        with patch("account_quota.select.select", return_value=([], [], [])), \
                patch("account_quota.time.monotonic", side_effect=[0, 2]):
            with self.assertRaises(TimeoutError):
                quota._exchange(process, {"method": "initialized"}, None, 1)

    def test_notification_stream_has_total_byte_bound(self):
        quota, processes = self.peer('''
import json
while True:
    print(json.dumps({"method": "notice", "params": "x" * 256}), flush=True)
''')
        quota.MAX_BYTES = 4096
        quota._refresh()
        self.assertEqual(quota.snapshot()["status"], "unknown")
        self.assert_reaped(processes)

    def test_close_cancels_blocked_rpc_and_reaps(self):
        quota, processes = self.peer("import time; time.sleep(10)")
        quota.TIMEOUT_SECONDS = 8
        launched = threading.Event()
        original = quota._ready

        def ready(*args):
            launched.set()
            return original(*args)

        quota._ready = ready
        quota.start()
        self.assertTrue(launched.wait(1))
        started = time.monotonic()
        quota.close()
        self.assertLess(time.monotonic() - started, 1)
        self.assertFalse(quota._thread.is_alive())
        self.assert_reaped(processes)
        self.assertEqual(quota.snapshot()["status"], "unknown")


if __name__ == "__main__":
    unittest.main()
