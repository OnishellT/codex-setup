from types import SimpleNamespace
import unittest
from unittest.mock import patch

from panel_ui import LABELS, card_rows, communication, context_input, detail_lines, draw_cards, number, duration, run


def snapshot():
    main = {"id": "root", "task": "principal", "tokens": 700, "state": "completado", "seconds": 20,
            "communication_events": [{"id": "b", "from": "/root/review", "to": "/root", "timestamp": "2"}]}
    agent = {"id": "child", "task": "review", "nickname": "Banach", "tokens": 300,
             "state": "completado", "model": "gpt-5.6-terra", "effort": "medium", "seconds": 12,
             "token_share": 30, "quota_share": None, "history": [], "result": "",
             "communication_events": [{"id": "a", "from": "/root", "to": "/root/review", "timestamp": "1"}]}
    return {"thread": "root", "principal": main, "agents": [agent], "total_tokens": 1000}


class Screen:
    def __init__(self, size=(40, 48), keys=()):
        self.size, self.writes, self.keys = size, [], iter(keys)
    def getmaxyx(self): return self.size
    def erase(self): pass
    def timeout(self, ms): pass
    def keypad(self, value): pass
    def bkgd(self, *args): pass
    def refresh(self): pass
    def clearok(self, *args): pass
    def getch(self): return next(self.keys)
    def addnstr(self, y, x, value, width, attrs):
        assert 0 <= y < self.size[0]
        assert 0 <= x < self.size[1]
        self.writes.append(value)


class UItests(unittest.TestCase):
    def test_palette_changes_update_rgb_and_indexed_pairs(self):
        from panel_ui import Painter
        with patch("panel_ui.curses.start_color"), patch("panel_ui.curses.use_default_colors"), \
             patch("panel_ui.curses.COLORS", 16777472, create=True), \
             patch("panel_ui.curses.init_pair") as pair, patch("panel_ui.curses.color_pair", return_value=0):
            painter = Painter(Screen())
            rgb = {"colors": {"fg": "#f8f8f2", "bg": "#282a36"}}
            painter.theme(rgb)
            pair.assert_any_call(1, 0xf8f8f2, 0x282a36)
            pair.reset_mock()
            painter.theme(rgb)
            pair.assert_not_called()
            painter.theme({"colors": {}, "terminal_colors": {"fg": 15, "bg": 0}})
            pair.assert_any_call(1, 16777216 + 15, 16777216)

    def test_local_terminfo_distinguishes_rgb_from_indexed(self):
        import os
        import subprocess
        import sys
        from pathlib import Path
        result = subprocess.check_output([sys.executable, "-c",
            "import curses; curses.setupterm(); "
            "assert curses.tigetnum('colors') == 16777472; "
            "assert curses.tparm(curses.tigetstr('setaf'), 0x282a36) == b'\\x1b[38:2::40:42:54m'; "
            "assert curses.tparm(curses.tigetstr('setaf'), 16777216+15) == b'\\x1b[38;5;15m'"],
            env={**os.environ, "TERM": "codex-panel-direct", "TERMINFO": str(Path(__file__).parent / "terminfo")})
        self.assertEqual(result, b"")

    def test_communication_both_directions_across_logs(self):
        data = snapshot()
        self.assertEqual(communication(data, data["agents"][0]), ["Main → Banach", "Banach → Main"])

    def test_duplicate_and_unrelated_messages_hidden(self):
        data = snapshot()
        data["principal"]["communication_events"] += data["agents"][0]["communication_events"]
        data["principal"]["communication_events"].append({"from": "/root", "to": "/root/other", "timestamp": "3"})
        self.assertEqual(len(communication(data, data["agents"][0])), 2)

    def test_quota_not_misrepresented_as_token_share(self):
        data = snapshot()
        data["agents"][0]["estimated_quota_share"] = 20
        rows = card_rows(data, data["agents"][0], LABELS["en"])
        text = " ".join(row[0] for row in rows)
        self.assertIn("Est. quota share ~20.0% of session", text)
        self.assertIn("Tokens 300", text)
        self.assertNotIn("Quota cost", text)

    def test_context_input_uses_latest_request_not_session_total(self):
        data = snapshot()
        main = data["principal"]
        main.update({"context_input_tokens": 127_593, "model_context_window": 258_400})
        self.assertEqual(context_input(main, LABELS["en"]), "Context input 127.6k / 258.4k (49.4%)")
        self.assertEqual(context_input({}, LABELS["es"]), "Entrada contexto —")

    def test_account_meter_zero_stale_missing_and_reset(self):
        from panel_ui import quota_rows
        data = {"status": "ok", "updated_at": 100, "windows": [
            {"name": "primary", "used_percent": 0, "window_minutes": 300, "resets_at": 200},
            {"name": "secondary", "used_percent": 95, "window_minutes": 10080, "resets_at": 300}]}
        rows = quota_rows(data, LABELS["en"], 44, now=101)
        text = " ".join(r[0] for r in rows)
        self.assertIn("0% used", text)
        self.assertIn("95% used", text)
        self.assertIn("5h00m", text)
        self.assertEqual(rows[-1][1], "warning")
        data["status"] = "stale"
        rows = quota_rows(data, LABELS["en"], 44, now=101)
        self.assertIn("STALE", rows[0][0])
        text = " ".join(r[0] for r in quota_rows(data, LABELS["en"], 44, now=400))
        self.assertNotIn("95%", text)
        self.assertIn("unavailable", text)
        self.assertIn("Loading", quota_rows(None, LABELS["en"], 44)[1][0])

    def test_card_compactness(self):
        data = snapshot()
        data["agents"][0]["activity"] = "DO NOT DISPLAY LONG PROSE " * 40
        rows = card_rows(data, data["agents"][0], LABELS["en"])
        self.assertEqual(len(rows), 4)
        self.assertNotIn("→", " ".join(row[0] for row in rows))

    def test_render_narrow_and_normal(self):
        from panel_ui import Painter
        for size in [(40, 48), (12, 20), (5, 8), (1, 1)]:
            screen = Screen(size)
            with patch("panel_ui.curses.start_color"), patch("panel_ui.curses.use_default_colors"):
                draw_cards(Painter(screen), snapshot(), 0, LABELS["en"], "dracula")
            self.assertNotIn("credit-rate proxy, not measured", "\n".join(screen.writes))

    def test_detail_contains_live_history_not_only_final(self):
        data = snapshot()
        data["agents"][0]["history"] = [{"timestamp": 100, "text": "Herramienta: exec"}]
        data["agents"][0]["transcript"] = [{"timestamp": 100, "title": "exec output", "body": "def hello():\n    return 42\n\nToken share: 30.0%"}]
        text = "\n".join(detail_lines(data, data["agents"][0], LABELS["en"], 100))
        self.assertIn("def hello():\n    return 42\n\nToken share: 30.0%", text)
        self.assertNotIn("Herramienta", text)

    def test_interface_uses_terminal_defaults_and_theme_accents(self):
        from appearance import interface_appearance
        source = {"colors": {"fg": "#eeeeee", "bg": "#112233", "accent": "#ff0000", "muted": "#777777"},
                  "terminal_colors": {"fg": 7, "bg": 0, "muted": 8}}
        look = interface_appearance(source)
        self.assertIsNone(look["colors"]["bg"])
        self.assertIsNone(look["colors"]["fg"])
        self.assertIsNone(look["colors"]["muted"])
        self.assertEqual(look["colors"]["accent"], "#ff0000")
        self.assertEqual(look["terminal_colors"], {})
        self.assertEqual(source["colors"]["bg"], "#112233")

    def test_long_original_lines_are_wrapped_not_truncated(self):
        from panel_ui import wrap_original
        original = "    " + "x" * 3000
        self.assertEqual("".join(wrap_original(original, 70)), original)

    def test_popup_live_refresh_and_no_ui_redaction(self):
        data = snapshot()
        data["agents"][0]["transcript"] = [{"timestamp": 100, "title": "exec", "body": "ORIGINAL_LINE"}]
        screen = Screen(size=(40, 140), keys=[-1, 27, ord("q"), 10])
        calls = []
        def refresh():
            calls.append(1)
            if len(calls) > 1:
                data["agents"][0]["transcript"].append({"timestamp": 101, "title": "output", "body": "LIVE_ADDED_LINE"})
            return data
        monitor = SimpleNamespace(home="/tmp", refresh=refresh)
        options = SimpleNamespace(profile="personal", detail="child", runtime=None)
        with patch("panel_ui.Appearance") as appearance, patch("panel_ui.Painter.theme"), \
             patch("panel_ui.curses.curs_set"), patch("panel_ui.curses.start_color"), \
             patch("panel_ui.curses.use_default_colors"), patch("time.monotonic", side_effect=[0, 2, 3, 3, 3]):
            appearance.return_value.read.return_value = {"theme": "dracula", "language": "en", "colors": {}}
            run(screen, monitor, options)
        rendered = "\n".join(screen.writes)
        self.assertIn("ORIGINAL_LINE", rendered)
        self.assertIn("LIVE_ADDED_LINE", rendered)
        self.assertIn("Token share 30.0%", rendered)
        self.assertNotIn("[oculto]", rendered)
        self.assertEqual(sum("◈ Banach" in line for line in screen.writes), 4)
        self.assertIn("Enter close", rendered)
        self.assertNotIn("Esc/Enter", rendered)

    def test_keyboard_selection_and_enter(self):
        import curses
        data = snapshot()
        data["agents"].append({**data["agents"][0], "id": "second", "nickname": "Carver"})
        screen = Screen(keys=[curses.KEY_DOWN, 10, 19, 27])
        monitor = SimpleNamespace(home="/tmp", refresh=lambda: data)
        options = SimpleNamespace(profile="personal", detail=None, runtime="/test-runtime")
        with patch("panel_ui.Appearance") as appearance, patch("panel_ui.Painter.theme"), \
             patch("panel_ui.curses.curs_set"), patch("panel_ui.curses.start_color"), \
             patch("panel_ui.curses.use_default_colors"), patch("panel_control.popup") as popup, \
             patch("panel_control.toggle", side_effect=[None, KeyboardInterrupt]) as toggle:
            appearance.return_value.read.return_value = {"theme": "dracula", "language": "en", "colors": {}}
            with self.assertRaises(KeyboardInterrupt):
                run(screen, monitor, options)
            self.assertEqual(popup.call_args.args[1], "second")
            self.assertEqual(toggle.call_count, 2)

    def test_formatting(self):
        self.assertEqual(number(None), "—")
        self.assertEqual(number(12345), "12.3k")
        self.assertEqual(duration(3661), "1:01:01")


if __name__ == "__main__": unittest.main()
