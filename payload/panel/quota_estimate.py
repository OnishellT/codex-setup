"""Session-relative credit-rate proxy, NOT measured account quota allocation.

Official rate snapshot checked 2026-09-03:
https://learn.chatgpt.com/docs/pricing
https://learn.chatgpt.com/docs/agent-configuration/speed
Rates are credits per million uncached input, cached input, output tokens.
Included subscription quota is not guaranteed proportional to credit billing.
No multiplication by account usedPercent: it includes other sessions/devices.
"""
import datetime as dt

RATES = {
    "gpt-5.6-sol": (100, 10, 500),
    "gpt-5.6-terra": (50, 5, 300),
    "gpt-5.6-luna": (5, 0.5, 30),
    "gpt-5.5": (125, 12.5, 750),
    "gpt-5.4": (62.5, 6.25, 375),
    "gpt-5.4-mini": (18.75, 1.875, 113),
}
VALID_THROUGH = dt.date(2026, 11, 21)


def weight(member, today=None):
    if (today or dt.date.today()) > VALID_THROUGH:
        return None, "Rate snapshot needs revalidation"
    model = member.get("model")
    if model == "gpt-5.6":
        model = "gpt-5.6-sol"
    if member.get("mixed_pricing_context"):
        return None, "Model/tier changed; no per-model token breakdown"
    if model not in RATES:
        return None, "No published rate in the local snapshot"
    counters = [member.get(k) for k in ("input_tokens", "cached_input_tokens", "output_tokens")]
    if any(type(v) is not int or v < 0 for v in counters):
        return None, "Incomplete token breakdown"
    incoming, cached, outgoing = counters
    if cached > incoming:
        return None, "Invalid cached token counter"
    tier = member.get("service_tier")
    if tier not in (None, "default", "standard", "auto", "fast"):
        return None, "Unknown credit multiplier for this service tier"
    multiplier = 1
    if tier == "fast":
        if model.startswith("gpt-5.6") or model == "gpt-5.5":
            multiplier = 2.5
        elif model == "gpt-5.4":
            multiplier = 2
        else:
            return None, "Unknown Fast multiplier"
    i, c, o = RATES[model]
    weighted = ((incoming - cached) * i + cached * c + outgoing * o) * multiplier
    return weighted, ("Credit-rate proxy; Standard assumed" if tier in (None, "auto")
                      else "Credit-rate proxy; " + tier)


def assign_estimates(members, has_main=True, today=None):
    """Set explicitly estimated fields; denominator includes Main and ALL children."""
    weighted = [weight(m, today) for m in members]
    complete = has_main and bool(members) and all(w is not None for w, _ in weighted)
    total = sum(w for w, _ in weighted) if complete else None
    for member, (value, reason) in zip(members, weighted):
        member["estimated_quota_share"] = 100 * value / total if total else None
        member["quota_estimate_basis"] = reason if complete else (
            reason if value is None else "Session denominator incomplete")
    return total
