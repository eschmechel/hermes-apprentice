"""Residency control endpoint (W1).

A tiny dependency-free HTTP control plane that owns the
:class:`~apprentice_serving.residency.ResidencyManager` and sits next to the
warm vLLM server. The proxy calls ``POST /residency/ensure {adapter_id}`` before
forwarding a matched request (model = adapter_id) to vLLM; pin/unpin/evict/status
are operator surfaces.

The routing logic is factored into :func:`handle` (pure, socket-free) so it's
unit-testable; :func:`serve` wraps it in ``http.server``.
"""

from __future__ import annotations

import json
import logging
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from .config_paths import adapter_path_resolver  # noqa: F401  (re-exported for callers)
from .residency import AllPinnedError, ResidencyManager, VLLMAdminClient

LOG = logging.getLogger("apprentice_serving.control")


def handle(mgr: ResidencyManager, method: str, path: str, body: dict | None) -> tuple[int, dict]:
    """Route a control request. Returns (http_status, json_body). No I/O."""
    if method == "GET" and path == "/residency/status":
        return 200, mgr.status()
    if method == "GET" and path == "/healthz":
        return 200, {"ok": True}
    if method == "POST" and path in ("/residency/ensure", "/residency/pin",
                                     "/residency/unpin", "/residency/evict"):
        adapter_id = (body or {}).get("adapter_id")
        if not adapter_id:
            return 400, {"error": "adapter_id required"}
        try:
            if path == "/residency/ensure":
                mgr.ensure_loaded(adapter_id)
                return 200, {"loaded": adapter_id, **mgr.status()}
            if path == "/residency/pin":
                mgr.pin(adapter_id)
                return 200, mgr.status()
            if path == "/residency/unpin":
                mgr.unpin(adapter_id)
                return 200, mgr.status()
            if path == "/residency/evict":
                return 200, {"evicted": mgr.evict(adapter_id)}
        except AllPinnedError as e:
            return 409, {"error": str(e)}
        except FileNotFoundError as e:
            return 404, {"error": str(e)}
    return 404, {"error": "not found"}


def _make_handler(mgr: ResidencyManager):
    class Handler(BaseHTTPRequestHandler):
        def _dispatch(self, method: str) -> None:
            body = None
            if method == "POST":
                length = int(self.headers.get("Content-Length", 0) or 0)
                raw = self.rfile.read(length) if length else b""
                try:
                    body = json.loads(raw) if raw else {}
                except json.JSONDecodeError:
                    self._send(400, {"error": "invalid JSON"})
                    return
            status, payload = handle(mgr, method, self.path, body)
            self._send(status, payload)

        def _send(self, status: int, payload: dict) -> None:
            data = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            self._dispatch("GET")

        def do_POST(self):
            self._dispatch("POST")

        def log_message(self, *a):  # quiet; we log via slog-style instead
            return

    return Handler


def build_manager(*, vllm_url: str, max_loras: int, registry_root: str | None = None) -> ResidencyManager:
    return ResidencyManager(
        VLLMAdminClient(vllm_url),
        max_loras=max_loras,
        resolve_path=adapter_path_resolver(registry_root),
    )


def serve(mgr: ResidencyManager, *, host: str = "127.0.0.1", port: int = 8071) -> None:
    httpd = ThreadingHTTPServer((host, port), _make_handler(mgr))
    LOG.info("residency control listening", extra={"host": host, "port": port,
                                                    "max_loras": mgr.status()["max_loras"]})
    try:
        httpd.serve_forever()
    finally:
        httpd.server_close()


def main(argv: list[str] | None = None) -> int:
    import argparse

    from .logging import setup_logging

    p = argparse.ArgumentParser(
        prog="apprentice-serve-control",
        description="Residency control plane for the warm multi-LoRA vLLM server.",
    )
    p.add_argument("--vllm-url", default="http://127.0.0.1:8000",
                   help="Base URL of the warm vLLM server (admin load/unload).")
    p.add_argument("--max-loras", type=int, default=4,
                   help="Max resident adapters (match vLLM --max-loras).")
    p.add_argument("--registry-root", default=None,
                   help="Override registry root for adapter path resolution.")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=8071)
    p.add_argument("-v", "--verbose", action="store_true")
    args = p.parse_args(argv)
    setup_logging(logging.DEBUG if args.verbose else logging.INFO)

    mgr = build_manager(vllm_url=args.vllm_url, max_loras=args.max_loras,
                        registry_root=args.registry_root)
    serve(mgr, host=args.host, port=args.port)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
