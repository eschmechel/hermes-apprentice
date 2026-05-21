from apprentice_orchestrator import placement
from apprentice_orchestrator.config import Config


def test_enough_vram_no_evict(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setattr(placement, "free_vram_mb", lambda: 8000)
    called = []
    monkeypatch.setattr(placement, "_stop_warm_serve", lambda: called.append(True))
    r = placement.prepare_local_gpu(Config())
    assert r["evicted"] is False and not called


def test_low_vram_evicts(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setenv("APPRENTICE_TRAIN_VRAM_MB", "4000")
    monkeypatch.setattr(placement, "free_vram_mb", lambda: 600)
    called = []
    monkeypatch.setattr(placement, "_stop_warm_serve", lambda: called.append(True))
    r = placement.prepare_local_gpu(Config())
    assert r["evicted"] is True and called == [True]


def test_no_nvidia_smi_is_noop(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setattr(placement, "free_vram_mb", lambda: None)
    called = []
    monkeypatch.setattr(placement, "_stop_warm_serve", lambda: called.append(True))
    r = placement.prepare_local_gpu(Config())
    assert r["vram_checked"] is False and not called


def test_conflict_skip_policy_does_not_evict(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setenv("APPRENTICE_ON_VRAM_CONFLICT", "skip")
    monkeypatch.setattr(placement, "free_vram_mb", lambda: 100)
    called = []
    monkeypatch.setattr(placement, "_stop_warm_serve", lambda: called.append(True))
    r = placement.prepare_local_gpu(Config())
    assert r["evicted"] is False and not called


def test_decide_cloud_falls_back_to_local(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setenv("APPRENTICE_TRAINING_PLACEMENT", "cloud")
    assert placement.decide(Config()) == "local"
