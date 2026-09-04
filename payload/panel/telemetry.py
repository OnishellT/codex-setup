"""Independent, standard-library-only telemetry for one agent's rollout.

Feed decoded JSON objects in log order. The caller owns file tailing, history
ownership/fork filtering, and resetting this instance for a new counter epoch.
In particular, session_id/root_turn_id are shared by children, NOT agent IDs.
If a fork inherits a cumulative baseline, the caller must rebase it as well as
filter inherited records. This module does no filesystem or account access.

thread_token_usage and token_count.info.total_token_usage are overlapping
cumulative snapshots, not additional requests. turn_token_usage is not a
lifetime counter. Unique response usage is a fallback for snapshot-free logs.
An uncorrelated delta after a snapshot makes totals unknown until a subsequent
snapshot resolves it; guessing whether that request was already included would
double-count. last_token_usage alone cannot establish a lifetime total.

Cached input is included in input; reasoning output is included in output.
Neither subset is added to tokens. Missing/invalid counters remain None.

Inspected 2026-09-03 17-09-26 / 17-09-36 / 17-09-42 rollouts expose raw token
counts and account-window rate_limits, but no per-request weighted quota cost.
Tokens therefore cannot establish an agent's quota-cost percentage. No quota
estimation or account request is performed here.
"""

from collections import deque
from collections.abc import Mapping


_FIELDS = (
    "input_tokens", "cached_input_tokens", "output_tokens",
    "reasoning_output_tokens", "total_tokens",
)


def _counter(value):
    return value if type(value) is int and value >= 0 else None


def _string(value):
    return value if isinstance(value, str) and value else None


def _usage(value):
    if not isinstance(value, Mapping):
        return {}
    result = {key: _counter(value.get(key)) for key in _FIELDS}
    # Compute the inclusive total when possible, never adding either subset.
    if result["input_tokens"] is not None and result["output_tokens"] is not None:
        result["total_tokens"] = result["input_tokens"] + result["output_tokens"]
    for subset, whole in (("cached_input_tokens", "input_tokens"),
                          ("reasoning_output_tokens", "output_tokens")):
        if (result[subset] is not None and result[whole] is not None
                and result[subset] > result[whole]):
            result[subset] = None
    return {key: count for key, count in result.items() if count is not None}


class Telemetry:
    """Collect one owned stream; identity is a string ID or agent path.

    summary() returns detached dictionaries with inclusive token counters and
    up to EVENT_LIMIT communication_events (oldest first). Events contain only
    id, from, to and timestamp, with None for unexposed optional metadata.
    Message bodies, encrypted passthroughs and tool arguments are never read.
    Dedup keys are retained for the instance lifetime so replay cannot inflate
    counts or repopulate evicted events. Create a new instance on log replacement.
    """

    EVENT_LIMIT = 64

    def __init__(self, identity):
        if not _string(identity):
            raise ValueError("identity must be a nonempty string ID or agent path")
        self.identity = identity
        self._values = dict.fromkeys(_FIELDS)
        self._has_snapshot = False
        self._has_delta = False
        self._seen_requests = set()
        self._seen_events = set()
        self._events = deque(maxlen=self.EVENT_LIMIT)
        self._pending = []

    @staticmethod
    def _position(record):
        ordinal = _counter(record.get("ordinal"))
        if ordinal is not None:
            return ("ordinal", ordinal)
        timestamp = _string(record.get("timestamp"))
        return ("timestamp", timestamp) if timestamp else None

    @staticmethod
    def _later(position, previous):
        return (position is not None and previous is not None
                and position[0] == previous[0] and position[1] > previous[1])

    def _snapshot(self, counts, record):
        position = self._position(record)
        # A later snapshot is a replacement, never a delta. A lower snapshot
        # is stale (or an epoch reset the caller must handle), not new usage.
        old_total = self._values["total_tokens"]
        total = counts.get("total_tokens")
        if old_total is not None and total is not None and total < old_total:
            return
        if (self._has_snapshot and total is not None and total == old_total):
            # Repeated snapshots can fill previously absent detail, but must
            # not combine counters from different cumulative checkpoints.
            for key, value in counts.items():
                if self._values[key] is None:
                    self._values[key] = value
        else:
            self._values = {key: counts.get(key) for key in _FIELDS}
        self._has_snapshot = True
        unresolved = []
        for pending_position, minimum in self._pending:
            covered = self._later(position, pending_position)
            if not covered or any(counts.get(key, -1) < value
                                  for key, value in minimum.items()):
                unresolved.append((pending_position, minimum))
        self._pending = unresolved

    def _request_key(self, payload, record):
        response_id = _string(payload.get("response_id"))
        if response_id:
            return ("response", _string(payload.get("thread_id")), response_id)
        position = self._position(record)
        # Equal numeric usage is NOT a request identity: two real requests may
        # have identical token counts. Without an ID/position, don't guess.
        return ("record", position) if position else None

    def consume(self, record):
        """Consume a decoded record; malformed/unrelated records are harmless."""
        if not isinstance(record, Mapping):
            return
        payload = record.get("payload")
        if not isinstance(payload, Mapping):
            return
        kind = record.get("type")
        if kind == "token_usage_record":
            counts = _usage(payload.get("thread_token_usage"))
            delta = _usage(payload.get("usage"))
            if not counts and not delta:
                return
            key = self._request_key(payload, record)
            duplicate = key is not None and key in self._seen_requests
            if key is not None:
                self._seen_requests.add(key)
            if counts:
                self._snapshot(counts, record)
            elif not duplicate:
                if self._has_snapshot:
                    # No shared response ID on token_count: this delta may
                    # overlap an earlier snapshot. Wait for an ordered one
                    # that covers at least the current values plus this delta.
                    baseline = {field: self._values[field] or 0 for field in _FIELDS}
                    for _, previous in self._pending:
                        for field, value in previous.items():
                            baseline[field] = max(baseline[field], value)
                    minimum = {field: baseline[field] + value
                               for field, value in delta.items()}
                    self._pending.append((self._position(record), minimum))
                elif key is not None:
                    for field in _FIELDS:
                        value = delta.get(field)
                        old = self._values[field]
                        self._values[field] = (
                            (old or 0) + value
                            if value is not None and (not self._has_delta or old is not None)
                            else None
                        )
                    self._has_delta = True
                else:
                    # We cannot distinguish this request from a replay. Nor
                    # can we silently omit it and present a partial sum.
                    self._values = dict.fromkeys(_FIELDS)
                    self._has_delta = True
        elif kind == "event_msg" and payload.get("type") == "token_count":
            info = payload.get("info")
            if isinstance(info, Mapping):
                counts = _usage(info.get("total_token_usage"))
                if counts:
                    self._snapshot(counts, record)
        elif kind == "response_item" and payload.get("type") == "agent_message":
            self._communication(payload, record)

    def _communication(self, payload, record):
        sender = _string(payload.get("author"))
        recipient = _string(payload.get("recipient"))
        if sender is None or recipient is None:
            return
        message_id = _string(payload.get("id"))
        timestamp = _string(record.get("timestamp")) or _string(payload.get("timestamp"))
        key = (("message", message_id) if message_id else
               ("record", self._position(record), sender, recipient))
        if key in self._seen_events:
            return
        self._seen_events.add(key)
        self._events.append({"id": message_id, "from": sender, "to": recipient,
                             "timestamp": timestamp})

    def summary(self):
        """Return inclusive counts (int or None) and detached event metadata."""
        values = self._values if not self._pending else dict.fromkeys(_FIELDS)
        return {
            "identity": self.identity,
            "tokens": values["total_tokens"],
            **{key: values[key] for key in _FIELDS if key != "total_tokens"},
            "communication_events": [dict(event) for event in self._events],
        }
