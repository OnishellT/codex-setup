"""Compact cards, keyboard selection and a live popup, without model calls."""
import curses
import datetime as dt
import json
import textwrap
import subprocess
import time

from appearance import Appearance, interface_appearance


LABELS = {
    "en": {"title": "SUBAGENTS", "empty": "No subagents yet", "waiting": "Waiting for your first Codex message",
           "tokens": "Tokens", "share": "Token share", "quota": "Quota cost", "unavailable": "not exposed",
           "time": "Runtime", "select": "↑↓ select · Enter inspect", "hide": "Ctrl-S hide / show",
           "hint": "Est. share: credit-rate proxy, not measured", "detail": "LIVE ACTIVITY", "quiet": "No new events >30s",
           "estimate": "Est. quota share", "of_session": "of session", "account": "ACCOUNT QUOTA", "used": "used",
           "loading_quota": "Loading account quota…", "quota_unknown": "Account quota unavailable", "stale": "STALE",
           "detail_keys": "↑↓ scroll · End follow · Enter close", "follow": "Following", "paused": "Paused",
           "events": "COMMUNICATION", "none": "No recorded messages", "model": "Model", "missing": "Agent not available",
           "done": "Done", "running": "Running", "error": "Error", "stopped": "Stopped", "unknown": "Unknown",
           "result": "Result", "activity": "Activity", "in": "in", "out": "out", "cached": "cached"},
    "es": {"title": "SUBAGENTES", "empty": "Sin subagentes todavía", "waiting": "Envía tu primer mensaje a Codex",
           "tokens": "Tokens", "share": "% de tokens", "quota": "Coste cuota", "unavailable": "no disponible",
           "time": "Duración", "select": "↑↓ elegir · Enter inspeccionar", "hide": "Ctrl-S ocultar / mostrar",
           "hint": "Reparto estimado por créditos; no medido", "detail": "ACTIVIDAD EN VIVO", "quiet": "Sin novedades >30s",
           "estimate": "Reparto estimado", "of_session": "de la sesión", "account": "CUOTA DE CUENTA", "used": "usado",
           "loading_quota": "Cargando cuota de cuenta…", "quota_unknown": "Cuota de cuenta no disponible", "stale": "DESACTUALIZADO",
           "detail_keys": "↑↓ desplazar · End seguir · Enter cerrar", "follow": "Siguiendo", "paused": "Pausado",
           "events": "COMUNICACIÓN", "none": "Sin mensajes registrados", "model": "Modelo", "missing": "Agente no disponible",
           "done": "Listo", "running": "Ejecutando", "error": "Error", "stopped": "Detenido", "unknown": "Desconocido",
           "result": "Resultado", "activity": "Actividad", "in": "entrada", "out": "salida", "cached": "caché"},
}


def number(value):
    if value is None:
        return "—"
    if value >= 1_000_000:
        return f"{value / 1_000_000:.2f}M"
    if value >= 10_000:
        return f"{value / 1000:.1f}k"
    return f"{value:,}"


def duration(seconds):
    seconds = max(0, int(seconds))
    hours, remaining = divmod(seconds, 3600)
    minutes, seconds = divmod(remaining, 60)
    return f"{hours}:{minutes:02}:{seconds:02}" if hours else f"{minutes:02}:{seconds:02}"


def exact_tokens(value):
    return f"{value:,}" if value is not None else "—"


def short_time(seconds):
    seconds = max(0, int(seconds))
    if seconds >= 86400:
        return f"{seconds // 86400}d{seconds % 86400 // 3600}h"
    if seconds >= 3600:
        return f"{seconds // 3600}h{seconds % 3600 // 60:02}m"
    return f"{seconds // 60}m" if seconds >= 60 else f"{seconds}s"


def quota_rows(quota, words, width, now=None):
    now = time.time() if now is None else now
    quota = quota or {}
    stamp = quota.get("updated_at")
    status = quota.get("status", "loading")
    suffix = f" · {short_time(now - stamp)} ago" if isinstance(stamp, (int, float)) else ""
    if words is LABELS["es"]:
        suffix = f" · hace {short_time(now - stamp)}" if isinstance(stamp, (int, float)) else ""
    stale = status not in ("ok", "live", "ready")
    if stale and stamp is not None:
        suffix = " · " + words["stale"] + suffix
    rows = [(words["account"] + suffix, "accent" if not stale else "muted")]
    windows = quota.get("windows", [])
    for window in windows[:2]:
        minutes = window.get("window_minutes")
        label = short_time(minutes * 60) if isinstance(minutes, (int, float)) else window.get("name", "?")
        used = window.get("used_percent")
        reset = window.get("resets_at")
        expired = isinstance(reset, (int, float)) and now >= reset
        if used is None or expired:
            rows.append((f"{label}  —  " + words["quota_unknown"], "muted"))
            continue
        bar_width = max(3, min(10, width - 31))
        filled = round(min(100, max(0, used)) / 100 * bar_width)
        bar = "█" * filled + "░" * (bar_width - filled)
        remaining = " · ↻" + short_time(reset - now) if isinstance(reset, (int, float)) else ""
        rows.append((f"{label} [{bar}] {used:g}% {words['used']}" + remaining,
                     "muted" if stale else ("warning" if used >= 90 else "normal")))
    if not windows:
        rows.append((words["loading_quota"] if status in ("loading", "starting") else words["quota_unknown"], "muted"))
    return rows


def status(agent, words):
    return words.get({"trabajando": "running", "completado": "done", "error": "error",
                      "interrumpido": "stopped"}.get(agent.get("state"), "unknown"))


def nickname(agent):
    return agent.get("nickname") or agent.get("task") or "Agent"


def percent(value):
    return "—" if value is None else f"{value:.1f}%"


def communication(snapshot, agent):
    """Render direction only, never decrypted transport bodies."""
    members = ([snapshot["principal"]] if snapshot.get("principal") else []) + snapshot["agents"]
    names = {a["id"]: ("Main" if a["id"] == snapshot["thread"] else nickname(a)) for a in members}
    names.update({"/root": "Main", "main": "Main"})
    for item in members:
        names["/root/" + item.get("task", "")] = nickname(item)
    events, seen = [], set()
    endpoints = {agent['id'], '/root/' + agent.get('task', '')}
    candidates = [event for member in members for event in member.get("communication_events", [])]
    candidates.sort(key=lambda event: event.get("timestamp") or "")
    for event in candidates:
        source = event.get("from", event.get("sender", "?"))
        target = event.get("to", event.get("recipient", "?"))
        if source not in endpoints and target not in endpoints:
            continue
        key = event.get("id") or (source, target, event.get("timestamp"))
        if key in seen:
            continue
        seen.add(key)
        source = names.get(source, source)
        target = names.get(target, target)
        if not source or not target:
            continue
        direction = f"{source} → {target}"
        if not events or events[-1] != direction:
            events.append(direction)
    return events


def card_rows(snapshot, agent, words):
    model = agent.get("model", "?").replace("gpt-5.6-", "")
    return [
        (f"{nickname(agent)} · {status(agent, words)}", "title"),
        (f"{agent.get('task', '')} · {model}/{agent.get('effort', '?')}", "muted"),
        (f"{words['time']} {duration(agent['seconds'])}   {words['tokens']} {exact_tokens(agent.get('tokens'))}", "normal"),
        (f"{words['estimate']} " + ("~" if agent.get('estimated_quota_share') is not None else "")
         + percent(agent.get('estimated_quota_share')) + " " + words['of_session'], "muted"),
    ]


class Painter:
    def __init__(self, screen):
        self.screen, self.last = screen, None
        self.styles = {key: 0 for key in ("normal", "accent", "muted", "success", "selected", "warning")}
        try:
            curses.start_color()
            curses.use_default_colors()
        except curses.error:
            pass

    def theme(self, appearance):
        colors = appearance.get("colors", {})
        indexed = appearance.get("terminal_colors", {})
        signature = json.dumps([colors, indexed], sort_keys=True)
        if signature == self.last:
            return
        self.last = signature

        def color(role):
            index = indexed.get(role)
            if type(index) is int:
                if getattr(curses, "COLORS", 0) == 16777472:
                    return 16777216 + index
                if getattr(curses, "COLORS", 0) < 16777216 or index < 8:
                    return index
                return -1
            value = colors.get(role)
            if not value:
                return -1
            rgb = int(value.lstrip("#")[:6], 16)
            if getattr(curses, "COLORS", 0) >= 16777216:
                return rgb
            # Conservative xterm-256 approximation only when direct color is unavailable.
            r, g, b = (rgb >> 16) & 255, (rgb >> 8) & 255, rgb & 255
            return 16 + round(r / 255 * 5) * 36 + round(g / 255 * 5) * 6 + round(b / 255 * 5)

        bg, fg = color("bg"), color("fg")
        for index, key in enumerate(self.styles, 1):
            foreground = fg if key in ("normal", "selected") else color(key)
            background = color("selection") if key == "selected" else bg
            try:
                curses.init_pair(index, foreground, background)
                self.styles[key] = curses.color_pair(index)
                if key == "selected" and background == -1:
                    self.styles[key] |= curses.A_REVERSE
                elif key == "muted" and foreground == -1:
                    self.styles[key] |= curses.A_DIM
            except (curses.error, ValueError):
                self.styles[key] = curses.A_REVERSE if key == "selected" else 0
        self.screen.bkgd(" ", self.styles["normal"])

    def text(self, y, x, value, style="normal", bold=False, width=None):
        h, w = self.screen.getmaxyx()
        if y < 0 or y >= h or x >= w:
            return
        limit = min(w - x - 1, width if width is not None else w - x - 1)
        if limit <= 0:
            return
        value = str(value)
        value = "".join(c for c in value if c.isprintable())
        if len(value) > limit:
            value = value[:max(0, limit - 1)] + "…"
        try:
            self.screen.addnstr(y, x, value, limit, self.styles[style] | (curses.A_BOLD if bold else 0))
        except curses.error:
            pass


def draw_cards(paint, snapshot, selection, words, theme):
    screen = paint.screen
    h, w = screen.getmaxyx()
    screen.erase()
    paint.text(0, 2, "◈  " + words["title"], "accent", True)
    paint.text(1, 2, f"{snapshot.get('thread') or '—'}", "muted")
    root = snapshot.get("principal")
    if root:
        paint.text(3, 2, f"Main  {status(root, words)} · {duration(root['seconds'])}", "normal", True)
        paint.text(4, 2, f"{words['tokens']} {number(snapshot.get('total_tokens'))} total · {number(root.get('tokens'))} Main", "muted")
    else:
        paint.text(3, 2, words["waiting"], "muted")
    paint.text(5, 1, "─" * max(1, w - 3), "muted")
    agents = snapshot["agents"]
    if not agents:
        paint.text(8, 2, words["empty"], "muted")
    y = 7
    account_rows = quota_rows(snapshot.get("account_quota"), words, w - 4)
    footer_y = h - len(account_rows) - 4
    visible_count = max(0, (footer_y - 7) // 7)
    first = max(0, selection - visible_count + 1)
    for index in range(first, min(len(agents), first + visible_count)):
        chosen = index == selection
        agent = agents[index]
        border_style = "accent" if chosen else "muted"
        paint.text(y, 1, "╭" + "─" * max(1, w - 5) + "╮", border_style)
        rows = card_rows(snapshot, agent, words)
        # Four compact metrics rows; communication remains in the original logs.
        for offset in range(4):
            line, style = rows[offset] if offset < len(rows) else ("", "normal")
            paint.text(y + offset + 1, 1, "│", border_style)
            if offset == 0:
                line = ("› " if chosen else "  ") + line
                style = "selected" if chosen else ("success" if agent["state"] == "completado" else "normal")
            paint.text(y + offset + 1, 3, line.ljust(max(0, w - 7)), style, offset == 0, width=w - 7)
            paint.text(y + offset + 1, w - 3, "│", border_style)
        paint.text(y + 5, 1, "╰" + "─" * max(1, w - 5) + "╯", border_style)
        y += 7
    paint.text(footer_y, 1, "─" * max(1, w - 3), "muted")
    for offset, (line, style) in enumerate(account_rows, 1):
        paint.text(footer_y + offset, 2, line, style)
    paint.text(h - 3, 2, words["select"], "accent")
    paint.text(h - 2, 2, words["hide"] + " · " + theme, "muted")


def transcript_rows(agent, width):
    """Render original public records, retaining newlines/indentation, not summaries."""
    from transcript import safe_text
    rows = []
    for event in agent.get("transcript", []):
        stamp = event.get("timestamp")
        try:
            timestamp = (dt.datetime.fromtimestamp(stamp) if isinstance(stamp, (int, float))
                         else dt.datetime.fromisoformat(str(stamp).replace("Z", "+00:00")).astimezone()).strftime("%H:%M:%S")
        except (ValueError, TypeError, OSError):
            timestamp = "--:--:--"
        title = safe_text(event.get("title") or event.get("kind", "event"))
        call_id = str(event.get("call_id") or "")
        title = f"{timestamp}  {title}" + (f"  [{call_id[-12:]}]" if call_id else "")
        rows.append(("─" * max(1, min(width, 76)), "muted"))
        rows.extend((line, "accent") for line in wrap_original(title, width))
        rows.extend((line, "normal") for line in wrap_original(safe_text(event.get("body", "")), width))
        rows.append(("", "normal"))
    return rows or [("Waiting for recorded public events…", "muted")]


def wrap_original(text, width):
    # No whitespace collapsing, prose rewriting or ellipsis. Wrapping is visual only.
    return [wrapped for line in str(text).expandtabs(4).split("\n")
            for wrapped in (textwrap.wrap(line, max(1, width), replace_whitespace=False,
                            drop_whitespace=False, break_on_hyphens=False) or [""])]


def detail_lines(snapshot, agent, words, width):
    """Text-only representation for diagnostics/tests; no legacy summary fallback."""
    return [line for line, _ in transcript_rows(agent, width)]


def run(screen, monitor, options):
    from panel_control import popup, toggle, sync_style
    try:
        curses.curs_set(0)
    except curses.error:
        pass
    screen.timeout(500)
    # ncurses otherwise waits 1000 ms after a lone Esc, unlike Enter. This is
    # process-local, not a tmux/global terminal setting. Keep a small nonzero
    # interval for arrow/Home/Page key sequences arriving in multiple bytes.
    curses.set_escdelay(25)
    screen.keypad(True)
    appearance = Appearance(monitor.home, options.profile)
    painter = Painter(screen)
    selection, selected_id, scroll = 0, None, 0
    follow = True
    styled = None
    snapshot, next_refresh = None, 0
    while True:
        if snapshot is None or time.monotonic() >= next_refresh:
            snapshot = monitor.refresh()
            next_refresh = time.monotonic() + 1
        quota = getattr(options, "quota", None)
        if quota is not None:
            snapshot["account_quota"] = quota.snapshot()
        look = interface_appearance(appearance.read())
        words = LABELS.get(look.get("language"), LABELS["en"])
        painter.theme(look)
        signature = json.dumps(look, sort_keys=True)
        if options.runtime and not options.detail and signature != styled:
            try:
                sync_style(options.runtime, look)
                styled = signature
            except (OSError, ValueError, subprocess.CalledProcessError):
                pass
        agents = snapshot["agents"]
        if selected_id and any(a["id"] == selected_id for a in agents):
            selection = next(i for i, a in enumerate(agents) if a["id"] == selected_id)
        selection = min(selection, max(0, len(agents) - 1))
        selected_id = agents[selection]["id"] if agents else None
        if options.detail:
            h, w = screen.getmaxyx()
            screen.erase()
            agent = next((a for a in agents if a["id"] == options.detail), None)
            heading = f"{nickname(agent)} · {status(agent, words)} · {duration(agent['seconds'])}" if agent else words["missing"]
            painter.text(0, 2, "◈ " + heading, "accent", True)
            if agent:
                painter.text(1, 2, f"{agent['model']} / {agent['effort']} · {words['tokens']} {exact_tokens(agent.get('tokens'))} · {words['share']} {percent(agent.get('token_share'))}", "muted")
                painter.text(2, 2, f"{words['estimate']} ~{percent(agent.get('estimated_quota_share'))} {words['of_session']} · {agent.get('quota_estimate_basis', '')}", "muted")
            body = transcript_rows(agent, w - 5) if agent else [(words["missing"], "normal")]
            capacity = max(1, h - 6)
            scroll = max(0, len(body) - capacity) if follow else min(scroll, max(0, len(body) - capacity))
            for y, (line, style) in enumerate(body[scroll:scroll + capacity], 3):
                painter.text(y, 2, line, style)
            painter.text(h - 2, 2, words["detail_keys"] + " · " + words["follow" if follow else "paused"] + f" · {scroll + 1}–{min(len(body), scroll + capacity)}/{len(body)}", "muted")
        else:
            theme_label = look.get("theme", "terminal")
            if not any(look.get("colors", {}).values()) and not look.get("terminal_colors"):
                theme_label += " · terminal fallback"
            draw_cards(painter, snapshot, selection, words, theme_label)
            if look.get("fallback_reason") and not look.get("terminal_colors"):
                painter.text(screen.getmaxyx()[0] - 5, 2, look["fallback_reason"], "warning")
        screen.refresh()
        key = screen.getch()
        if options.detail:
            if key in (10, 13, curses.KEY_ENTER):
                return
            if key in (curses.KEY_UP, curses.KEY_PPAGE):
                scroll = max(0, scroll - (1 if key == curses.KEY_UP else 10))
                follow = False
            elif key in (curses.KEY_DOWN, curses.KEY_NPAGE):
                scroll += 1 if key == curses.KEY_DOWN else 10
                follow = False
            elif key == curses.KEY_END:
                follow = True
            elif key == curses.KEY_HOME:
                follow, scroll = False, 0
            continue
        if key in (19, ord("q"), 27) and options.runtime:
            toggle(options.runtime)
        elif key in (curses.KEY_UP, ord("k")):
            selection = max(0, selection - 1)
            selected_id = agents[selection]["id"] if agents else None
        elif key in (curses.KEY_DOWN, ord("j")):
            selection = min(max(0, len(agents) - 1), selection + 1)
            selected_id = agents[selection]["id"] if agents else None
        elif key in (10, 13, curses.KEY_ENTER) and selected_id:
            if options.runtime:
                try:
                    popup(options.runtime, selected_id, snapshot["thread"], options.profile)
                except (OSError, ValueError, subprocess.CalledProcessError):
                    pass
                screen.clearok(True)
                next_refresh = 0
        elif key in (27, ord("q")) and not options.runtime:
            return
