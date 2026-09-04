"""Structured public rollout transcript (standard library only).

Contract: call Transcript.consume(decoded_record) in log order ONLY AFTER Tail's
ownership/fork check. This module does not infer ownership from session_id, open
files, tail partial JSONL lines, or inspect session metadata. Replace the instance
on log replacement/reset. events is a detached, oldest-first list of dictionaries
with timestamp, kind, title, body, call_id, and event_types. Timestamp is the
record's epoch number or ISO string, unchanged; missing timestamps are None.
Titles/event_types retain raw English schema labels. Bodies retain original
language and whitespace, except terminal controls, narrow secret assignments,
and explicitly marked memory omissions. No prompts/private item fields are read.

Calls have separate tool_input/tool_output events. Command snapshots and deltas
update one command event; public message mirrors update one assistant event.
Only explicit call/item/event IDs establish identity, never content similarity.
Unidentified events remain distinct. Dedup history, entries and bodies are bounded;
an old mirror can reappear after eviction. An omission event makes this explicit.
An 8 MiB default budget bounds retained text/index accounting, not Python allocator
overhead or the caller-owned input record. events allocates a detached snapshot.
"""

from collections import OrderedDict
from collections.abc import Mapping
from copy import deepcopy
import hashlib
import json
import re
import shlex


_PRIVATE = frozenset({
    "analysis", "reasoning", "summary", "summary_text", "raw_content",
    "encrypted_content", "encrypted_body", "encrypted_payload", "instructions",
    "base_instructions", "system_prompt", "developer_instructions",
    "internal_chat_message_metadata_passthrough",
})
_PHASES = {None, "commentary", "final", "final_answer"}
_CHANNELS = {None, "commentary", "final", "final_answer"}
_LIFECYCLE = {
    "task_started", "task_complete", "task_completed", "task_aborted",
    "turn_started", "turn_complete", "turn_completed", "turn_aborted",
    "turn_failed", "error", "warning", "stream_error", "shutdown_complete",
}
_TEXT_TYPES = {None, "text", "input_text", "output_text"}
_TRIM = "[OMITTED: earlier text exceeded transcript memory limit]\n"

# OSC/DCS/SOS/PM/APC strings (including incomplete strings) and CSI/ESC codes.
# Keep tabs and newlines; strip CR, C0/C1 controls and bidi display overrides.
_STRINGS = re.compile(r"(?:\x1b[\]PX^_]|[\x90\x98\x9d\x9e\x9f])"
                      r".*?(?:\x07|\x1b\\|\x9c|$)", re.DOTALL)
_CSI = re.compile(r"(?:\x1b\[|\x9b)[0-?]*[ -/]*(?:[@-~]|$)")
_ESC = re.compile(r"\x1b[ -/]*[0-~]?|\x1b$")
_CONTROLS = re.compile(r"[\x00-\x08\x0b-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]")
_SECRET_NAME = (r"(?:[A-Za-z][A-Za-z0-9_]*_)?(?:api[_-]?key|access[_-]?token|"
                r"refresh[_-]?token|auth[_-]?token|password|passwd|secret|token|"
                r"client[_-]?secret|secret[_-]?key)")
_ASSIGNMENT = re.compile(
    r"(?i)(?<![\w-])([\"']?" + _SECRET_NAME + r"[\"']?[ \t]*[=:][ \t]*)"
    r"(\"(?:\\.|[^\"\\])*\"|'(?:\\.|[^'\\])*'|[^\s,;\}\]\"']+)"
)


def safe_text(value):
    """Preserve layout; remove terminal sequences and known secret assignments.

    Deliberately does not redact generic 'token' prose (e.g. 'Token share') or
    perform heuristic token detection. This is not a general-purpose DLP filter.
    """
    if value is None:
        return ""
    if not isinstance(value, str):
        value = str(value)
    value = _CONTROLS.sub("", _ESC.sub("", _CSI.sub("", _STRINGS.sub("", value))))

    def redact(match):
        literal = match[2]
        quote = literal[0] if literal[0] in "\"'" else ""
        return match[1] + quote + "[REDACTED]" + quote

    return _ASSIGNMENT.sub(redact, value)


# Kept as a convenience for callers that previously named their sanitizer clean.
clean = safe_text


def _public(value):
    return (value.get("channel") in _CHANNELS
            and value.get("phase") in _PHASES
            and value.get("role") not in {"system", "developer", "user"})


def _safe_object(value):
    """Remove private envelope fields from structured tool data, not prose."""
    if isinstance(value, Mapping):
        if not _public(value) or value.get("type") in _PRIVATE:
            return None
        return {str(k): _safe_object(v) for k, v in value.items()
                if k not in _PRIVATE and not str(k).startswith("encrypted_")}
    if isinstance(value, list):
        return [_safe_object(v) for v in value]
    return value


def _text(value):
    if isinstance(value, str):
        return value
    if value is None:
        return ""
    if isinstance(value, list):
        # Text content blocks use concatenation, never whitespace normalization.
        if all(isinstance(v, Mapping) and "type" in v for v in value):
            return "".join(_text(v) for v in value)
        return json.dumps(_safe_object(value), ensure_ascii=False, indent=2)
    if isinstance(value, Mapping):
        if not _public(value):
            return ""
        if "type" in value:
            if value.get("type") in _TEXT_TYPES:
                return _text(value.get("text"))
            return ""  # Never stringify encrypted/image/reasoning blocks.
        return json.dumps(_safe_object(value), ensure_ascii=False, indent=2)
    if isinstance(value, (int, float, bool)):
        return json.dumps(value)
    return ""


def _ident(value):
    return value if isinstance(value, (str, int)) and not isinstance(value, bool) else None


def _key(category, value):
    # Fixed-size identity keys avoid retaining arbitrarily long untrusted IDs.
    return category, hashlib.sha256(str(value).encode("utf-8", "replace")).digest()


class Transcript:
    def __init__(self, max_bytes=8 * 1024 * 1024, max_events=4096):
        if not isinstance(max_bytes, int) or max_bytes < 2048:
            raise ValueError("max_bytes must be at least 2048")
        if not isinstance(max_events, int) or max_events < 1:
            raise ValueError("max_events must be positive")
        self.max_bytes = max_bytes
        self.max_events = max_events
        self._entries = OrderedDict()
        self._aliases = {}
        self._seen = OrderedDict()
        self._next = 0
        self._bytes = 0
        self._omitted = False
        self._turn = None
        self._last_final = None

    @property
    def events(self):
        """Detached public snapshot; existing entries can grow after consume()."""
        result = []
        if self._omitted:
            result.append({"timestamp": None, "kind": "omission", "title": "omission",
                           "body": "[OMITTED: older transcript text or dedup history "
                                   "exceeded memory limits; old mirrors may reappear]",
                           "call_id": None, "event_types": ["omission"]})
        for entry in self._entries.values():
            event = deepcopy(entry["event"])
            parts = entry["parts"]
            if event["kind"] == "command":
                labels = {"command": "Command", "cwd": "Working directory",
                          "output": "Output", "stdout": "Stdout", "stderr": "Stderr",
                          "formatted_output": "Formatted output", "status": "Status",
                          "exit_code": "Exit code", "duration": "Duration",
                          "duration_ms": "Duration (ms)"}
                body = []
                for field, label in labels.items():
                    if field not in parts:
                        continue
                    # Aggregation and formatted output are mirrors, not more bytes.
                    if field in {"stdout", "stderr"} and parts.get("output"):
                        if parts[field] in parts["output"]:
                            continue
                    if field == "formatted_output" and (
                            parts.get("output") or parts.get("stdout") or parts.get("stderr")):
                        continue
                    body.append(label + ":\n" + parts[field])
                event["body"] = clean("\n\n".join(body))
            else:
                event["body"] = clean(parts.get("body", ""))
            result.append(event)
        return result

    def _entry(self, category, record, payload, label, extra=None):
        sources = [payload, extra or {}, record]
        aliases = []
        for source in sources:
            for field in ("call_id", "item_id", "event_id", "id"):
                value = _ident(source.get(field))
                if value is not None:
                    aliases.append(_key(category, value))
        seq = next((self._aliases[a] for a in aliases if a in self._aliases), None)
        if seq is None:
            seq = self._next
            self._next += 1
            stamp = record.get("timestamp", payload.get("timestamp"))
            if not isinstance(stamp, (str, int, float)) or isinstance(stamp, bool):
                stamp = None
            entry = {"serial": seq, "event": {"timestamp": stamp, "kind": category,
                               "title": label, "call_id": None, "event_types": []},
                     "parts": {}, "aliases": set(), "size": 0}
            self._entries[seq] = entry
        entry = self._entries[seq]
        # A bridge between known item/call IDs joins both identity namespaces.
        for alias in aliases:
            old_seq = self._aliases.get(alias)
            if old_seq is not None and old_seq != seq:
                other = self._entries[old_seq]
                for field, value in other["parts"].items():
                    self._snapshot(entry, field, value)
                aliases.extend(a for a in other["aliases"] if a not in aliases)
                self._drop(old_seq)
            self._aliases[alias] = seq
            entry["aliases"].add(alias)
        call_id = next((_ident(s.get("call_id")) for s in sources
                        if _ident(s.get("call_id")) is not None), None)
        if call_id is not None:
            entry["event"]["call_id"] = clean(str(call_id))[:256]
        if label not in entry["event"]["event_types"]:
            entry["event"]["event_types"].append(label)
        entry["event"]["title"] = label
        return entry

    @staticmethod
    def _snapshot(entry, field, value):
        old = entry["parts"].get(field, "")
        # A delayed shorter mirror must not discard an already longer snapshot.
        if value != old and (not old.startswith(value) or not old):
            entry["parts"][field] = value
        elif field not in entry["parts"]:
            entry["parts"][field] = value

    def _delta(self, entry, field, value, record, payload):
        # call_id alone identifies a stream, NOT a chunk. Identical chunks with
        # distinct IDs (or no chunk ID) must both survive.
        chunk_id = next((_ident(s.get(k)) for s in (payload, record)
                         for k in ("event_id", "id", "sequence", "sequence_number", "offset")
                         if _ident(s.get(k)) is not None), None)
        if chunk_id is not None:
            key = hashlib.sha256(json.dumps([entry["serial"], field, chunk_id, value],
                                           ensure_ascii=False).encode()).digest()
            if key in self._seen:
                return
            self._seen[key] = None
        entry["parts"][field] = entry["parts"].get(field, "") + value

    def _command(self, record, payload, label, extra=None):
        entry = self._entry("command", record, payload, label, extra)
        parts = entry["parts"]
        if "command" in payload:
            command = payload["command"]
            if isinstance(command, list):
                command = shlex.join(str(arg) for arg in command)
            if isinstance(command, str):
                parts["command"] = command
        if label == "exec_command_output_delta":
            stream = payload.get("stream", "stdout")
            field = "stderr" if stream == "stderr" else "stdout"
            self._delta(entry, field, _text(payload.get("delta", payload.get("output"))),
                        record, payload)
        else:
            for field in ("stdout", "stderr", "formatted_output"):
                if payload.get(field) is not None:
                    self._snapshot(entry, field, _text(payload[field]))
            for field in ("aggregated_output", "output"):
                if payload.get(field) is not None:
                    self._snapshot(entry, "output", _text(payload[field]))
                    break
        for field in ("cwd", "status", "exit_code", "duration", "duration_ms"):
            if payload.get(field) is not None:
                parts[field] = _text(payload[field])
        self._bound(entry)

    def _message(self, record, payload, label, extra=None):
        if not _public(payload):
            return
        body = _text(payload.get("content", payload.get("text", payload.get("message"))))
        if not body:
            return
        entry = self._entry("assistant", record, payload, label, extra)
        self._snapshot(entry, "body", body)
        if payload.get("phase") in {"final", "final_answer"}:
            turn = payload.get("turn_id", (extra or {}).get("turn_id", self._turn))
            if turn is None and self._last_final and self._last_final[0] == entry["serial"]:
                turn = self._last_final[1]
            self._last_final = (entry["serial"], turn)
        self._bound(entry)

    def consume(self, record):
        """Accept one complete, already-owned decoded JSONL record; return None."""
        if not isinstance(record, Mapping):
            return
        payload = record.get("payload")
        if not isinstance(payload, Mapping) or not _public(payload):
            return
        outer, label = record.get("type"), payload.get("type")
        if not isinstance(label, str):
            return
        if outer == "response_item":
            if label == "message" and payload.get("role") == "assistant":
                self._message(record, payload, label)
            elif label in {"agent_message", "AgentMessage"}:
                # Some agent_message envelopes carry an author/channel object.
                author = payload.get("author")
                if author not in (None, "assistant") and not (
                        isinstance(author, Mapping) and author.get("role") == "assistant"
                        and _public(author)):
                    return
                self._message(record, payload, label)
            elif label in {"function_call", "custom_tool_call",
                           "function_call_output", "custom_tool_call_output"}:
                output = label.endswith("_output")
                category = "tool_output" if output else "tool_input"
                field = "output" if output else ("arguments" if label == "function_call" else "input")
                entry = self._entry(category, record, payload, label)
                name = payload.get("name")
                if isinstance(name, str):
                    entry["event"]["title"] = label + " · " + clean(name)[:256]
                self._snapshot(entry, "body", _text(payload.get(field)))
                self._bound(entry)
        elif outer == "event_msg":
            if label in {"item_started", "item_completed", "item_updated"}:
                item = payload.get("item")
                if not isinstance(item, Mapping) or not _public(item):
                    return
                item_type = item.get("type")
                if item_type in {"CommandExecution", "command_execution", "commandExecution"}:
                    self._command(record, item, item_type, payload)
                elif item_type in {"AgentMessage", "agent_message", "agentMessage"}:
                    self._message(record, item, item_type, payload)
            elif label == "agent_message":
                self._message(record, payload, label)
            elif label in {"exec_command_begin", "exec_command_output_delta", "exec_command_end"}:
                self._command(record, payload, label)
            elif label in _LIFECYCLE:
                if label in {"task_started", "turn_started"}:
                    turn = payload.get("turn_id")
                    if turn is None or turn != self._turn:
                        self._last_final = None
                    self._turn = turn
                entry = self._entry("lifecycle", record, payload, label)
                # No wholesale payload dump: only public error/warning text.
                entry["parts"]["body"] = _text(payload.get("message"))
                self._bound(entry)
                # Some versions only expose the final public message here.
                if label in {"task_complete", "task_completed", "turn_complete", "turn_completed"}:
                    final = payload.get("last_agent_message")
                    if isinstance(final, str) and final:
                        # This field explicitly mirrors the turn's final message.
                        # Never apply text dedup to ordinary assistant messages.
                        if self._last_final:
                            seq, turn = self._last_final
                            previous = self._entries.get(seq)
                            if (previous and turn == payload.get("turn_id", self._turn)
                                    and previous["parts"].get("body") == final):
                                return
                        self._message(record, {"message": final,
                                      "id": payload.get("last_agent_message_id"),
                                      "turn_id": payload.get("turn_id", self._turn),
                                      "phase": "final_answer"}, label + ".last_agent_message")

    def _drop(self, seq):
        entry = self._entries.pop(seq)
        self._bytes -= entry["size"]
        for alias in entry["aliases"]:
            if self._aliases.get(alias) == seq:
                del self._aliases[alias]

    @staticmethod
    def _size(entry):
        return (len(json.dumps(entry["event"], ensure_ascii=False).encode("utf-8", "replace"))
                + sum(len(v.encode("utf-8", "replace")) + 64 for v in entry["parts"].values())
                + len(entry["aliases"]) * 128 + 256)

    def _bound(self, entry):
        # Bound a single event as well as the rolling collection. Sanitize BEFORE
        # tail clipping so a clipped secret assignment cannot expose its value.
        overhead = self._size(entry) - sum(len(v.encode("utf-8", "replace"))
                                          for v in entry["parts"].values())
        allowance = max(len(_TRIM), (self.max_bytes - 640 - overhead
                                    - len(self._seen) * 128) // max(1, len(entry["parts"])))
        for field, value in entry["parts"].items():
            encoded = value.encode("utf-8", "replace")
            if len(encoded) > allowance:
                safe = clean(value).encode("utf-8", "replace")
                keep = max(0, allowance - len(_TRIM))
                entry["parts"][field] = _TRIM + (safe[-keep:] if keep else b"").decode("utf-8", "ignore")
                self._omitted = True
        # Metadata strings are also untrusted and budgeted.
        if isinstance(entry["event"]["timestamp"], str) and len(entry["event"]["timestamp"]) > 128:
            entry["event"]["timestamp"] = None
        self._bytes -= entry["size"]
        entry["size"] = self._size(entry)
        self._bytes += entry["size"]
        while len(self._seen) > min(8192, self.max_bytes // 1024):
            self._seen.popitem(last=False)
            self._omitted = True
        while self._entries and (len(self._entries) > self.max_events or
                                self._bytes + len(self._seen) * 128 + 512 > self.max_bytes):
            if len(self._entries) == 1:
                # Fixed envelope/index overhead can dominate very small budgets.
                self._drop(next(iter(self._entries)))
                self._omitted = True
                break
            self._drop(next(iter(self._entries)))
            self._omitted = True
