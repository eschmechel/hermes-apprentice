import pytest

from apprentice_serving.config_paths import adapter_path_resolver
from apprentice_serving.control import handle
from apprentice_serving.residency import ResidencyManager


class FakeAdmin:
    def __init__(self):
        self.loaded = []

    def load(self, name, path):
        self.loaded.append(name)

    def unload(self, name):
        self.loaded.remove(name)


def _mgr(max_loras=2):
    return ResidencyManager(FakeAdmin(), max_loras=max_loras, resolve_path=lambda a: f"/reg/{a}")


def test_ensure_loads():
    mgr = _mgr()
    status, body = handle(mgr, "POST", "/residency/ensure", {"adapter_id": "a"})
    assert status == 200 and body["loaded"] == "a" and "a" in body["resident"]


def test_ensure_requires_adapter_id():
    status, body = handle(_mgr(), "POST", "/residency/ensure", {})
    assert status == 400 and "adapter_id" in body["error"]


def test_status_and_health():
    mgr = _mgr()
    mgr.ensure_loaded("a")
    s, b = handle(mgr, "GET", "/residency/status", None)
    assert s == 200 and "a" in b["resident"]
    s, b = handle(mgr, "GET", "/healthz", None)
    assert s == 200 and b["ok"] is True


def test_pin_then_full_returns_409():
    mgr = _mgr(max_loras=1)
    handle(mgr, "POST", "/residency/pin", {"adapter_id": "p"})
    status, body = handle(mgr, "POST", "/residency/ensure", {"adapter_id": "q"})
    assert status == 409 and "pinned" in body["error"]


def test_evict_reports_bool():
    mgr = _mgr()
    mgr.ensure_loaded("a")
    s, b = handle(mgr, "POST", "/residency/evict", {"adapter_id": "a"})
    assert s == 200 and b["evicted"] is True
    s, b = handle(mgr, "POST", "/residency/evict", {"adapter_id": "a"})
    assert b["evicted"] is False


def test_unknown_route_404():
    s, _ = handle(_mgr(), "GET", "/nope", None)
    assert s == 404


def test_resolver_prefers_latest_then_vdir(tmp_path):
    root = tmp_path / "registry"
    skill = root / "demo"
    (skill / "v1" / "lora-adapter").mkdir(parents=True)
    resolve = adapter_path_resolver(str(root))
    # only v1 exists -> falls back to it
    assert resolve("demo").endswith("v1/lora-adapter")
    # add a 'latest' -> preferred
    (skill / "latest" / "lora-adapter").mkdir(parents=True)
    assert resolve("demo").endswith("latest/lora-adapter")


def test_resolver_missing_raises(tmp_path):
    resolve = adapter_path_resolver(str(tmp_path))
    with pytest.raises(FileNotFoundError):
        resolve("ghost")
