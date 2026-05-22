"""Canary safety management — manage specialist ramp states via the proxy API.

Provides a Python interface to the proxy's canary HTTP handlers:
``GET /canary/state``, ``GET /canary/state/{id}``,
``POST /canary/advance``, ``POST /canary/set-state``,
``POST /canary/compare``, ``GET /canary/alert/{id}``.
"""

from __future__ import annotations

import json
import logging
import urllib.error
import urllib.request
from typing import Any

LOG = logging.getLogger("apprentice_orchestrator.safety")

STATES = ("warming", "live", "broken")


def _req(method: str, url: str, body: dict | None = None) -> dict | list | None:
    data = json.dumps(body).encode() if body else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            raw = resp.read()
            if not raw:
                return None
            return json.loads(raw)
    except urllib.error.HTTPError as exc:
        LOG.error("HTTP %d from %s %s", exc.code, method, url,
                  extra={"body": exc.read().decode(errors="replace")[:500]})
        return None
    except urllib.error.URLError as exc:
        LOG.error("Connection error %s %s: %s", method, url, exc.reason)
        return None


def list_states(proxy_url: str) -> list[dict[str, Any]]:
    """List all registered canary states from the proxy."""
    result = _req("GET", f"{proxy_url}/canary/state")
    return result if isinstance(result, list) else []


def get_state(proxy_url: str, pattern_id: str) -> dict[str, Any] | None:
    """Get the canary state for a single pattern."""
    result = _req("GET", f"{proxy_url}/canary/state/{pattern_id}")
    return result if isinstance(result, dict) else None


def advance(proxy_url: str, pattern_id: str, score: float | None = None) -> dict[str, Any] | None:
    """Trigger a canary evaluation step.

    When *score* is provided it is used directly; otherwise the proxy
    uses its accumulated shadow agreement data.  Returns the new state
    and whether a transition occurred.
    """
    body: dict[str, Any] = {"pattern_id": pattern_id}
    if score is not None:
        body["score"] = score
    result = _req("POST", f"{proxy_url}/canary/advance", body)
    return result if isinstance(result, dict) else None


def set_state(proxy_url: str, pattern_id: str, state: str, pct: int = 0) -> bool:
    """Manually override a pattern's canary state."""
    result = _req("POST", f"{proxy_url}/canary/set-state",
                  {"pattern_id": pattern_id, "state": state, "pct": pct})
    return isinstance(result, dict) and result.get("status") == "ok"


def compare(proxy_url: str, specialist_body: str, upstream_body: str) -> dict[str, float] | None:
    """Compare two response bodies and return the agreement score."""
    result = _req("POST", f"{proxy_url}/canary/compare", {
        "specialist": specialist_body,
        "upstream": upstream_body,
    })
    return result if isinstance(result, dict) else None


def alert(proxy_url: str, pattern_id: str) -> str | None:
    """Get the alert message for a broken pattern (or None if not broken)."""
    state = get_state(proxy_url, pattern_id)
    if not state or state.get("state") != "broken":
        return None
    return (
        f"⚠️ Canary: pattern {pattern_id!r} demoted to broken. "
        "Agreement fell below threshold. No specialist responses served until resolved."
    )
