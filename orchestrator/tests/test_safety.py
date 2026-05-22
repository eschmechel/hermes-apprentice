"""Tests for apprentice_orchestrator.safety — the canary ramp management module."""

from __future__ import annotations

import json
from http.server import HTTPServer, BaseHTTPRequestHandler
from threading import Thread

import pytest

from apprentice_orchestrator import safety


class _CanaryHandler(BaseHTTPRequestHandler):
    """Tiny HTTP server that simulates the proxy canary API."""

    _states: list[dict] | None = None
    _advance_result: dict | None = None
    _compare_result: dict | None = None

    def do_GET(self):
        if self.path == "/canary/state":
            data = self._states or []
            self._send_json(data)
        elif self.path.startswith("/canary/state/"):
            pid = self.path.split("/")[-1]
            states = {s["pattern_id"]: s for s in (self._states or [])}
            match = states.get(pid)
            if match:
                self._send_json(match)
            else:
                self.send_error(404)
        else:
            self.send_error(404)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length)) if length else {}

        if self.path == "/canary/advance":
            if self._advance_result:
                self._send_json(self._advance_result)
            else:
                self.send_error(500)
        elif self.path == "/canary/set-state":
            self._send_json({"status": "ok"})
        elif self.path == "/canary/compare":
            self._send_json(self._compare_result or {"agreement": 0.85})
        else:
            self.send_error(404)

    def _send_json(self, data):
        encoded = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


@pytest.fixture
def canary_server():
    """Spin up a local HTTP server that mocks the proxy canary API."""
    server = HTTPServer(("127.0.0.1", 0), _CanaryHandler)
    port = server.server_address[1]
    proxy_url = f"http://127.0.0.1:{port}"
    _CanaryHandler._states = [
        {"pattern_id": "p1", "state": "warming", "pct": 15, "request_count": 10, "agreement_sum": 8.5},
        {"pattern_id": "p2", "state": "live", "pct": 100, "request_count": 0, "agreement_sum": 0},
        {"pattern_id": "p3", "state": "broken", "pct": 0, "request_count": 0, "agreement_sum": 0},
    ]
    _CanaryHandler._advance_result = {
        "pattern_id": "p1", "state": "warming", "transitioned": True, "pct": 25,
    }
    _CanaryHandler._compare_result = {"agreement": 0.92}

    t = Thread(target=server.serve_forever, daemon=True)
    t.start()
    yield proxy_url
    server.shutdown()
    t.join(timeout=2)


# ── list_states ─────────────────────────────────────────────────────────────

def test_list_states(canary_server):
    states = safety.list_states(canary_server)
    assert len(states) == 3
    ids = {s["pattern_id"] for s in states}
    assert ids == {"p1", "p2", "p3"}


def test_list_states_connection_error():
    states = safety.list_states("http://127.0.0.1:19999")
    assert states == []


# ── get_state ───────────────────────────────────────────────────────────────

def test_get_state_found(canary_server):
    state = safety.get_state(canary_server, "p1")
    assert state is not None
    assert state["pattern_id"] == "p1"
    assert state["state"] == "warming"
    assert state["pct"] == 15


def test_get_state_not_found(canary_server):
    state = safety.get_state(canary_server, "nonexistent")
    assert state is None


# ── advance ─────────────────────────────────────────────────────────────────

def test_advance_with_score(canary_server):
    result = safety.advance(canary_server, "p1", score=0.85)
    assert result is not None
    assert result["pattern_id"] == "p1"
    assert result["transitioned"] is True


def test_advance_without_score(canary_server):
    result = safety.advance(canary_server, "p1")
    assert result is not None
    assert result["pattern_id"] == "p1"


def test_advance_failure(canary_server):
    _CanaryHandler._advance_result = None
    result = safety.advance(canary_server, "p1")
    assert result is None


# ── set_state ───────────────────────────────────────────────────────────────

def test_set_state(canary_server):
    ok = safety.set_state(canary_server, "p1", "broken", pct=0)
    assert ok is True


# ── compare ─────────────────────────────────────────────────────────────────

def test_compare(canary_server):
    result = safety.compare(canary_server, '{"choices":[{"message":{"content":"hi"}}]}',
                            '{"choices":[{"message":{"content":"hello"}}]}')
    assert result is not None
    assert result["agreement"] == 0.92


# ── alert ───────────────────────────────────────────────────────────────────

def test_alert_for_broken_pattern(canary_server):
    msg = safety.alert(canary_server, "p3")
    assert msg is not None
    assert "broken" in msg
    assert "p3" in msg


def test_alert_for_non_broken(canary_server):
    msg = safety.alert(canary_server, "p1")
    assert msg is None


def test_alert_for_nonexistent(canary_server):
    msg = safety.alert(canary_server, "nonexistent")
    assert msg is None


# ── CLI integration ─────────────────────────────────────────────────────────

def test_cli_safety_list(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "list", "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert len(data) == 3


def test_cli_safety_status(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "status", "--pattern-id", "p1", "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert data["pattern_id"] == "p1"
    assert data["state"] == "warming"


def test_cli_safety_status_not_found(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "status", "--pattern-id", "nope", "--proxy-url", canary_server])
    assert rc == 1
    data = json.loads(capsys.readouterr().out)
    assert "error" in data


def test_cli_safety_advance(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "advance", "--pattern-id", "p1", "--score", "0.9",
                   "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert data["pattern_id"] == "p1"


def test_cli_safety_set_state(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "set-state", "--pattern-id", "p1", "--state", "broken",
                   "--pct", "0", "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert data["status"] == "ok"


def test_cli_safety_compare(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "compare",
                   "--specialist-body", '{"a":1}',
                   "--upstream-body", '{"b":2}',
                   "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert "agreement" in data


def test_cli_safety_alert_broken(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "alert", "--pattern-id", "p3", "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert data["level"] == "alert"
    assert "broken" in data["msg"]


def test_cli_safety_alert_not_broken(canary_server, capsys):
    from apprentice_orchestrator import cli
    rc = cli.main(["safety", "alert", "--pattern-id", "p2", "--proxy-url", canary_server])
    assert rc == 0
    data = json.loads(capsys.readouterr().out)
    assert data["level"] == "info"
    assert data["msg"] == "pattern 'p2' is not broken"
