"""Bindings and reversible pane parking, confined to a codex-panel server."""
import fcntl
import json
from pathlib import Path
import shlex
import subprocess
import sys

SCRIPT = str(Path(__file__).with_name("codex_panel.py"))


def ui_command(args):
    terminfo = str(Path(__file__).with_name("terminfo"))
    return shlex.join(["env", "TERM=codex-panel-direct", "TERMINFO=" + terminfo, *args])


def style_color(look, role):
    from appearance import interface_appearance
    look = interface_appearance(look)
    indexed = look.get("terminal_colors", {}).get(role)
    if type(indexed) is int:
        return "colour" + str(indexed)
    return look.get("colors", {}).get(role) or "default"


def tmux(state, *args):
    return subprocess.check_output([state["tmux"], "-L", state["session"], *args],
                                   text=True, stderr=subprocess.PIPE).strip()


def install(binary, session, runtime, left, right, width, profile):
    state = {"tmux": binary, "session": session, "left": left, "right": right,
             "width": width, "profile": profile}
    (runtime / "control.json").write_text(json.dumps(state))
    command = shlex.join([sys.executable, SCRIPT, "toggle", str(runtime)])
    condition = "#{||:#{==:#{pane_id}," + left + "},#{==:#{pane_id}," + right + "}}"
    # The binding acts ONLY on these two panes, even inside this dedicated server.
    tmux(state, "bind-key", "-n", "C-s", "if-shell", "-F", condition,
         "run-shell " + shlex.quote(command), "send-keys C-s")
    tmux(state, "set-option", "-t", session, "status-left", " Codex ")
    # The parked monitor's window must not look like a second conversation tab.
    tmux(state, "set-window-option", "-g", "window-status-format", "")
    tmux(state, "set-window-option", "-g", "window-status-current-format", "")
    return state


def toggle(runtime):
    runtime = Path(runtime)
    with (runtime / "toggle.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        state = json.loads((runtime / "control.json").read_text())
        left_window = tmux(state, "display-message", "-p", "-t", state["left"], "#{window_id}")
        right_window = tmux(state, "display-message", "-p", "-t", state["right"], "#{window_id}")
        if left_window == right_window:
            tmux(state, "break-pane", "-d", "-s", state["right"], "-n", "__subagents_hidden")
            tmux(state, "select-window", "-t", left_window)
            tmux(state, "select-pane", "-t", state["left"])
        else:
            available = int(tmux(state, "display-message", "-p", "-t", state["left"], "#{window_width}"))
            width = min(state["width"], max(20, available // 3))
            tmux(state, "join-pane", "-h", "-l", str(width), "-s", state["right"], "-t", state["left"])
            tmux(state, "select-window", "-t", left_window)
            tmux(state, "select-pane", "-t", state["right"])


def popup(runtime, agent_id, fallback_root=None, profile="personal"):
    from appearance import Appearance
    import os
    state = json.loads((Path(runtime) / "control.json").read_text())
    args = [sys.executable, SCRIPT, "watch", "--profile", state.get("profile", profile), "--detail", agent_id]
    if fallback_root:
        args += ["--thread", fallback_root, "--owner-pid", (Path(runtime) / "pid").read_text().strip()]
    else:
        args += ["--runtime", str(runtime)]
    command = ui_command(args)
    look = Appearance(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")), state.get("profile", profile)).read()
    fg, bg, accent = (style_color(look, role) for role in ("fg", "bg", "accent"))
    # A real tmux overlay: closing it never affects Codex or the sidebar process.
    tmux(state, "display-popup", "-E", "-w", "88%", "-h", "82%", "-t", state["right"],
         "-T", " Actividad " if look.get("language") == "es" else " Agent activity ",
         "-s", f"fg={fg},bg={bg}", "-S", f"fg={accent},bg={bg}", command)


def sync_style(runtime, look):
    """Apply palette only to this panel's session/window, not other tmux servers."""
    state = json.loads((Path(runtime) / "control.json").read_text())
    bg, muted, accent = (style_color(look, role) for role in ("bg", "muted", "accent"))
    tmux(state, "set-option", "-t", state["session"], "status-style", f"fg={muted},bg={bg}")
    # Pane-scoped defaults: do not overwrite Main or any sibling window/session.
    tmux(state, "set-option", "-p", "-t", state["right"], "window-style", "fg=default,bg=default")
    tmux(state, "set-option", "-p", "-t", state["right"], "window-active-style", "fg=default,bg=default")
    tmux(state, "set-window-option", "-t", state["left"], "pane-border-style", f"fg={muted}")
    tmux(state, "set-window-option", "-t", state["left"], "pane-active-border-style", f"fg={accent}")
    hint = "Ctrl-S panel | Ctrl-b arrows: focus | Ctrl-b d: detach"
    if look.get("language") == "es":
        hint = "Ctrl-S panel | Ctrl-b flechas: foco | Ctrl-b d: separar"
    tmux(state, "set-option", "-t", state["session"], "status-right", hint)
