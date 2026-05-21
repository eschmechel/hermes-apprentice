"""LoRA adapter residency over a warm vLLM base (W1).

One base model stays resident in `vllm serve --enable-lora`; specialist LoRA
adapters (~18 MB) are loaded on demand via vLLM's runtime admin API and kept
warm with an LRU policy bounded by ``--max-loras``. The user (or the safety
layer) can ``pin`` an adapter so it's never evicted. This is what makes a real
specialist *zoo* fit on one small GPU: the base is loaded once, specialists are
cheap deltas swapped in and out.

The proxy routes a matched request by setting the request ``model`` to the
adapter name; before forwarding, the serving control layer calls
``ResidencyManager.ensure_loaded(adapter_id)``.
"""

from __future__ import annotations

import logging
from collections import OrderedDict
from typing import Callable, Protocol

LOG = logging.getLogger("apprentice_serving.residency")


class AdminClient(Protocol):
    """vLLM runtime LoRA admin surface (POST /v1/{load,unload}_lora_adapter)."""

    def load(self, name: str, path: str) -> None: ...
    def unload(self, name: str) -> None: ...


class AllPinnedError(RuntimeError):
    """Can't make room: every resident adapter is pinned and the cap is reached."""


class VLLMAdminClient:
    """Concrete :class:`AdminClient` over vLLM's runtime LoRA admin endpoints.

    Requires the server started with ``--enable-lora`` and the env
    ``VLLM_ALLOW_RUNTIME_LORA_UPDATING=True``.
    """

    def __init__(self, base_url: str, *, timeout: float = 30.0, client=None) -> None:
        self._base = base_url.rstrip("/")
        self._timeout = timeout
        self._client = client  # inject for tests; else lazy httpx

    def _post(self, path: str, payload: dict) -> None:
        if self._client is not None:
            resp = self._client.post(self._base + path, json=payload, timeout=self._timeout)
        else:
            import httpx
            resp = httpx.post(self._base + path, json=payload, timeout=self._timeout)
        resp.raise_for_status()

    def load(self, name: str, path: str) -> None:
        self._post("/v1/load_lora_adapter", {"lora_name": name, "lora_path": path})

    def unload(self, name: str) -> None:
        self._post("/v1/unload_lora_adapter", {"lora_name": name})


class ResidencyManager:
    """LRU residency for LoRA adapters on a warm base, bounded by ``max_loras``.

    ``resolve_path(adapter_id) -> str`` maps an adapter id to its on-disk path
    (e.g. ``registry/<id>/latest/lora-adapter``).
    """

    def __init__(
        self,
        admin: AdminClient,
        *,
        max_loras: int,
        resolve_path: Callable[[str], str],
    ) -> None:
        if max_loras < 1:
            raise ValueError("max_loras must be >= 1")
        self._admin = admin
        self._max = max_loras
        self._resolve = resolve_path
        self._lru: "OrderedDict[str, str]" = OrderedDict()  # id -> path, MRU last
        self._pinned: set[str] = set()

    # ---- core ---------------------------------------------------------------
    def ensure_loaded(self, adapter_id: str) -> None:
        """Guarantee ``adapter_id`` is resident; mark it most-recently-used.
        Evicts the LRU *unpinned* adapter(s) to make room when at capacity."""
        if adapter_id in self._lru:
            self._lru.move_to_end(adapter_id)
            return
        while len(self._lru) >= self._max:
            self._evict_one()
        path = self._resolve(adapter_id)
        self._admin.load(adapter_id, path)
        self._lru[adapter_id] = path
        LOG.info("adapter loaded", extra={"adapter": adapter_id, "resident": len(self._lru)})

    def _evict_one(self) -> None:
        for aid in list(self._lru.keys()):  # oldest first
            if aid not in self._pinned:
                self._admin.unload(aid)
                del self._lru[aid]
                LOG.info("adapter evicted (LRU)", extra={"adapter": aid})
                return
        raise AllPinnedError(
            f"all {len(self._lru)} resident adapters are pinned; raise --max-loras or unpin one"
        )

    # ---- pin / unpin --------------------------------------------------------
    def pin(self, adapter_id: str) -> None:
        self.ensure_loaded(adapter_id)
        self._pinned.add(adapter_id)
        LOG.info("adapter pinned", extra={"adapter": adapter_id})

    def unpin(self, adapter_id: str) -> None:
        self._pinned.discard(adapter_id)
        LOG.info("adapter unpinned", extra={"adapter": adapter_id})

    def evict(self, adapter_id: str) -> bool:
        """Explicit unload ('we don't need this anymore'). Returns True if it was resident."""
        if adapter_id not in self._lru:
            return False
        self._admin.unload(adapter_id)
        del self._lru[adapter_id]
        self._pinned.discard(adapter_id)
        return True

    # ---- introspection ------------------------------------------------------
    def status(self) -> dict:
        return {
            "max_loras": self._max,
            "resident": list(self._lru.keys()),   # LRU-ordered, MRU last
            "pinned": sorted(self._pinned),
        }

    def is_resident(self, adapter_id: str) -> bool:
        return adapter_id in self._lru
