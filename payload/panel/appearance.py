"""Read-only appearance adapter for Codex CLI 0.153.0 (Python 3.11+).

Call Appearance(CODEX_HOME, profile).read() on the UI's one-second tick. No
thread, config writes, terminal probes, subprocesses, or network requests occur.
``home`` means the Codex config directory, not the user's login directory.
The result includes theme/language/colors plus source/fallback_reason and
terminal_colors for the UI to display diagnostics or resolve indexed colors.
The source/fallback_reason attributes remain available for existing callers.

Evidence: https://learn.chatgpt.com/docs/cli-customization documents tui.theme
and $CODEX_HOME/themes/*.tmTheme. Local 0.153.0 --help documents the optional
<profile>.config.toml overlay. CLI UI is English; OS locale is NOT its setting.
Desktop appearance and the IDE's chatgpt.localeOverride are different surfaces.

All 32 built-in themes were decoded, read-only, from @openai/codex-linux-x64
vendor/x86_64-unknown-linux-musl/bin/codex on 2026-09-03. bundled_palettes.json
retains exact source RGBA, representative scope sources, offsets and hashes:
 binary SHA256: fce635028842bfe9257140e8b7d53162732945e2f356fc35225be0702b4974be
 embedded theme map: file offset 0xcf857ad; Dracula zlib payload: 0xcf8af8b,
 1254 compressed bytes, 6407 inflated bytes (Syntect serialized theme).
 inflated SHA256: 8ea6ddcc6a1db432942c245ac30ee4bcce0794f8c65d9af7c3e365b210138d98

These are exact theme color values, NOT a claim that all CLI chrome uses them.
Panel semantic roles are our mapping: accent=constant.numeric,
muted=comment, success=markup.inserted, warning=markup.changed. fg/bg/selection
are theme settings. /theme primarily controls syntax, not terminal defaults.
There is no documented palette export API. The small bundled JSON is loaded
once per instance; the executable is NEVER parsed at runtime. The bounded
offline decoder/provenance verifier lives only in test_appearance.py. This
snapshot must be revalidated for future CLI releases (no auto-version claim).
Picker IDs and display-name aliases are accepted case-insensitively, ignoring
spaces/hyphens/dots/parentheses. The ansi/base16/base16-256 themes contain
terminal references rather than fixed RGB. Their colors are None and their
exact indices/default markers are exposed separately as terminal_colors.

Custom themes: use unscoped settings and simple positive scope selectors for
representative panel colors, not a full Syntect/TextMate syntax highlighter.
Complex scope expressions are ignored. Opaque colors are exact; translucent
ones are composited against an available theme background, otherwise None.
CLI -c/project/remote/in-memory preview state cannot be inferred here.
"""

from __future__ import annotations

import os
import json
from pathlib import Path
import plistlib
import re
import tomllib
from xml.parsers.expat import ExpatError


ROLES = ("fg", "bg", "accent", "muted", "success", "selection", "warning")
_CATALOG_PATH = Path(__file__).with_name("bundled_palettes.json")
_SCOPE_ROLES = {
    "accent": ("constant.numeric", "constant.language", "keyword", "entity.name.function"),
    "muted": ("comment",),
    "success": ("markup.inserted", "diff.inserted", "entity.name.function"),
    "warning": ("markup.changed", "diff.changed", "string"),
}
_LIMIT = 2 * 1024 * 1024
_NAME = re.compile(r"[\w][\w .()-]*", re.UNICODE)
_SCOPE = re.compile(r"[A-Za-z_][\w-]*(?:\.[\w-]+)*")


def _read_bytes(path: Path) -> bytes:
    with path.open("rb") as stream:
        data = stream.read(_LIMIT + 1)
    if len(data) > _LIMIT:
        raise ValueError("appearance file exceeds 2 MiB")
    return data


def _toml(path: Path) -> dict:
    try:
        return tomllib.loads(_read_bytes(path).decode("utf-8"))
    except FileNotFoundError:
        return {}


def _alias_key(name: str) -> str:
    return re.sub(r"[^a-z0-9]", "", name.casefold())


def _load_catalog() -> tuple[dict, str]:
    """Validate the bundled data before handing any colors to curses."""
    data = json.loads(_read_bytes(_CATALOG_PATH))
    if not isinstance(data, dict) or data.get("schema_version") != 1:
        raise ValueError("unsupported palette catalog schema")
    provenance, palettes = data.get("provenance"), data.get("palettes")
    if not isinstance(provenance, dict) or not isinstance(palettes, dict):
        raise ValueError("invalid palette catalog structure")
    version = provenance.get("cli_version")
    if not isinstance(version, str) or not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("invalid catalog CLI version")
    if not isinstance(provenance.get("binary_sha256"), str) or not re.fullmatch(r"[a-f0-9]{64}", provenance["binary_sha256"]):
        raise ValueError("missing catalog binary provenance")
    if not 1 <= len(palettes) <= 256 or provenance.get("theme_count") != len(palettes):
        raise ValueError("palette catalog count mismatch")
    lookup = {}
    for slug, entry in palettes.items():
        if not isinstance(entry, dict):
            raise ValueError("invalid catalog palette")
        colors, aliases = entry.get("colors"), entry.get("aliases")
        if not isinstance(colors, dict) or set(colors) != set(ROLES):
            raise ValueError("invalid catalog color roles")
        if any(value is not None and (not isinstance(value, str) or not re.fullmatch(r"#[0-9a-f]{6}", value)) for value in colors.values()):
            raise ValueError("invalid catalog RGB color")
        if not isinstance(aliases, list) or not aliases or len(aliases) > 32:
            raise ValueError("invalid catalog aliases")
        terminal = entry.get("terminal_colors", {})
        if not isinstance(terminal, dict) or not set(terminal) <= set(ROLES):
            raise ValueError("invalid catalog terminal color roles")
        if any(value != "default" and (type(value) is not int or not 0 <= value <= 255) for value in terminal.values()):
            raise ValueError("invalid terminal color reference")
        for alias in [slug, *aliases]:
            if not isinstance(alias, str) or not _NAME.fullmatch(alias):
                raise ValueError("invalid palette alias")
            key = _alias_key(alias)
            if key in lookup and lookup[key][0] != slug:
                raise ValueError("ambiguous palette alias")
            lookup[key] = (slug, entry)
    return lookup, version


def _theme(config: dict) -> str | None:
    tui = config.get("tui", {})
    if not isinstance(tui, dict):
        raise ValueError("tui must be a table")
    value = tui.get("theme")
    if value is not None and (not isinstance(value, str) or not value.strip()):
        raise ValueError("tui.theme must be a nonempty string")
    return value


def _color(value: object, background: str | None = None) -> str | None:
    if not isinstance(value, str) or not re.fullmatch(r"#[0-9a-fA-F]{6}(?:[0-9a-fA-F]{2})?", value):
        return None
    value = value.lower()
    if len(value) == 7 or value[7:] == "ff":
        return value[:7]
    alpha = int(value[7:], 16)
    if alpha == 0 or background is None:
        return None
    channels = [
        (int(value[i:i + 2], 16) * alpha + int(background[i:i + 2], 16) * (255 - alpha) + 127) // 255
        for i in (1, 3, 5)
    ]
    return "#" + "".join(f"{channel:02x}" for channel in channels)


def _custom_colors(path: Path) -> dict:
    theme = plistlib.loads(_read_bytes(path))
    if not isinstance(theme, dict) or not isinstance(theme.get("settings"), list):
        raise ValueError("tmTheme requires a settings array")
    base, scoped = {}, []
    for entry in theme["settings"]:
        if not isinstance(entry, dict) or not isinstance(entry.get("settings"), dict):
            raise ValueError("invalid tmTheme settings entry")
        scope, settings = entry.get("scope"), entry["settings"]
        if not scope:
            base.update(settings)
        elif isinstance(scope, str):
            for selector in scope.split(","):
                selector = selector.strip()
                if _SCOPE.fullmatch(selector):
                    scoped.append((selector, settings))
    bg = _color(base.get("background"))
    colors = dict.fromkeys(ROLES)
    colors.update(fg=_color(base.get("foreground"), bg), bg=bg,
                  selection=_color(base.get("selection"), bg))
    for role, targets in _SCOPE_ROLES.items():
        for target in targets:
            matches = [(selector.count("."), index, settings)
                       for index, (selector, settings) in enumerate(scoped)
                       if target == selector or target.startswith(selector + ".")]
            for _, _, settings in sorted(matches, reverse=True):
                color = _color(settings.get("foreground"), bg)
                if color is not None:
                    colors[role] = color
                    break
            if colors[role] is not None:
                break
    return colors


def interface_appearance(look):
    """Syntax palettes are not terminal chrome: Codex leaves fg/bg at default.

    Keep saved-theme accent colors, but never paint the whole terminal with a
    syntax editor background. None maps to curses -1 / tmux default. Selection
    uses reverse video, and secondary text uses dim, both terminal-relative.
    """
    result = {**look, "colors": look.get("colors", {}).copy(),
              "terminal_colors": look.get("terminal_colors", {}).copy()}
    for role in ("fg", "bg", "selection", "muted"):
        result["colors"][role] = None
        result["terminal_colors"].pop(role, None)
    return result


class Appearance:
    """Saved-config reader; source/fallback_reason describe the latest read.

    On an invalid or temporarily unreadable TOML file, retain the last valid
    saved theme. A missing config means defaults, not stale data. A missing or
    broken theme returns None colors. The caller maps None to curses defaults.
    No environment variable other than CODEX_PANEL_LANG determines language.
    """

    def __init__(self, home, profile="personal"):
        self.home = Path(home).expanduser()
        if profile and (not isinstance(profile, str) or not _NAME.fullmatch(profile)):
            raise ValueError("profile must be a file name, not a path")
        self.profile = profile
        self.source = "terminal-default"
        self.fallback_reason = None
        self._last_theme = None
        self._catalog_error = None
        try:
            self._catalog, self._catalog_version = _load_catalog()
        except (OSError, ValueError, UnicodeError, RecursionError) as exc:
            self._catalog, self._catalog_version = {}, "unknown"
            self._catalog_error = f"Bundled palette catalog unavailable: {exc}"

    def read(self) -> dict:
        language = os.environ.get("CODEX_PANEL_LANG", "").strip() or "en"
        if not re.fullmatch(r"[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*", language):
            language = "en"
        self.fallback_reason = None
        try:
            name = _theme(_toml(self.home / "config.toml"))
            if self.profile:
                overlay = _theme(_toml(self.home / f"{self.profile}.config.toml"))
                if overlay is not None:
                    name = overlay
            self._last_theme = name
        except (OSError, ValueError, UnicodeError) as exc:
            name = self._last_theme
            self.fallback_reason = f"Saved config unreadable; retaining last theme: {exc}"

        colors = dict.fromkeys(ROLES)
        terminal_colors = {}
        self.source = "terminal-default"
        reason = None
        if name is None:
            reason = "No saved theme; CLI automatic palette is unavailable."
        elif not _NAME.fullmatch(name):
            reason = "Theme name is not a safe file stem; using terminal defaults."
        elif _alias_key(name) in self._catalog:
            slug, entry = self._catalog[_alias_key(name)]
            colors = entry["colors"].copy()
            terminal_colors = entry.get("terminal_colors", {}).copy()
            self.source = f"codex-{self._catalog_version}-embedded-{slug}"
            if terminal_colors:
                reason = (f"{slug} uses terminal color references, not fixed RGB; "
                          "using terminal defaults unless the UI resolves terminal_colors.")
            elif any(value is None for value in colors.values()):
                reason = "Built-in theme has unspecified colors; those use terminal defaults."
        else:
            path = self.home / "themes" / f"{name}.tmTheme"
            try:
                colors = _custom_colors(path)
                self.source = str(path)
                if any(value is None for value in colors.values()):
                    reason = "Custom theme has unavailable panel roles; those use terminal defaults."
            except FileNotFoundError:
                reason = f"Palette unavailable for {name!r} (unsupported built-in or missing custom theme)."
                if self._catalog_error:
                    reason = self._catalog_error + "; " + reason
            except (OSError, ValueError, TypeError, OverflowError, ExpatError, plistlib.InvalidFileException) as exc:
                reason = f"Custom theme unreadable; using terminal defaults: {exc}"
        if reason:
            self.fallback_reason = "; ".join(filter(None, (self.fallback_reason, reason)))
        return {"theme": name or "default", "language": language, "colors": colors,
                "source": self.source, "fallback_reason": self.fallback_reason,
                "terminal_colors": terminal_colors}
