from pathlib import Path

import pytest

from apprentice_serving.residency import AllPinnedError, ResidencyManager
from apprentice_serving.server import build_vllm_cmd


class FakeAdmin:
    def __init__(self):
        self.loaded: list[str] = []
        self.calls: list[tuple[str, str]] = []  # (op, name)

    def load(self, name, path):
        self.loaded.append(name)
        self.calls.append(("load", name))

    def unload(self, name):
        self.loaded.remove(name)
        self.calls.append(("unload", name))


def _mgr(max_loras=2):
    admin = FakeAdmin()
    mgr = ResidencyManager(admin, max_loras=max_loras, resolve_path=lambda a: f"/reg/{a}/lora-adapter")
    return mgr, admin


def test_ensure_loads_and_dedupes():
    mgr, admin = _mgr()
    mgr.ensure_loaded("a")
    mgr.ensure_loaded("a")  # idempotent, no second load
    assert admin.calls == [("load", "a")]
    assert mgr.is_resident("a")


def test_lru_eviction_at_capacity():
    mgr, admin = _mgr(max_loras=2)
    mgr.ensure_loaded("a")
    mgr.ensure_loaded("b")
    mgr.ensure_loaded("a")          # 'a' now MRU, 'b' is LRU
    mgr.ensure_loaded("c")          # must evict the LRU -> 'b'
    assert ("unload", "b") in admin.calls
    assert set(admin.loaded) == {"a", "c"}
    assert mgr.status()["resident"][-1] == "c"  # MRU last


def test_pin_protects_from_eviction():
    mgr, admin = _mgr(max_loras=2)
    mgr.pin("keep")
    mgr.ensure_loaded("x")
    mgr.ensure_loaded("y")          # capacity hit -> evicts the unpinned 'x', not 'keep'
    assert ("unload", "x") in admin.calls
    assert "keep" in admin.loaded


def test_all_pinned_and_full_raises():
    mgr, admin = _mgr(max_loras=2)
    mgr.pin("p1")
    mgr.pin("p2")
    with pytest.raises(AllPinnedError):
        mgr.ensure_loaded("q")


def test_unpin_then_evictable():
    mgr, admin = _mgr(max_loras=1)
    mgr.pin("p")
    mgr.unpin("p")
    mgr.ensure_loaded("other")      # now 'p' can be evicted
    assert ("unload", "p") in admin.calls
    assert mgr.is_resident("other")


def test_explicit_evict():
    mgr, admin = _mgr()
    mgr.ensure_loaded("a")
    assert mgr.evict("a") is True
    assert mgr.evict("a") is False  # already gone
    assert not mgr.is_resident("a")


def test_status_shape():
    mgr, _ = _mgr(max_loras=3)
    mgr.ensure_loaded("a")
    mgr.pin("b")
    s = mgr.status()
    assert s["max_loras"] == 3 and set(s["resident"]) == {"a", "b"} and s["pinned"] == ["b"]


def test_build_vllm_cmd_lora_flags():
    base = build_vllm_cmd(Path("/m"), enable_lora=False)
    assert "--enable-lora" not in base
    lora = build_vllm_cmd(Path("/base"), enable_lora=True, max_loras=8, max_lora_rank=16)
    assert "--enable-lora" in lora
    assert "--max-loras" in lora and "8" in lora
    assert "--max-lora-rank" in lora and "16" in lora
