"""Tests for serving-01: vLLM server launcher. CPU-only."""

from __future__ import annotations

import json
from pathlib import Path
from unittest import mock

import pytest

from apprentice_serving.server import (
    build_vllm_cmd,
    check_only,
    resolve_model_path,
)


# --- resolve_model_path --------------------------------------------------

def test_resolve_model_direct_path(tmp_path: Path):
    model = tmp_path / "model"
    model.mkdir()
    (model / "config.json").write_text('{"model_type": "qwen2"}')
    result = resolve_model_path(model_dir=str(model))
    assert result == model.resolve()


def test_resolve_model_dir_missing():
    with pytest.raises(FileNotFoundError, match="model dir not found"):
        resolve_model_path(model_dir="/nonexistent/path")


def test_resolve_model_dir_no_config_json(tmp_path: Path):
    model = tmp_path / "model"
    model.mkdir()
    with pytest.raises(FileNotFoundError, match="no config.json"):
        resolve_model_path(model_dir=str(model))


def test_resolve_both_args_raises():
    with pytest.raises(ValueError, match="not both"):
        resolve_model_path(pattern_id="x", model_dir="/y")


def test_resolve_neither_arg_raises():
    with pytest.raises(ValueError, match="specify --pattern-id"):
        resolve_model_path()


def test_resolve_from_registry_success(tmp_path: Path):
    model = tmp_path / "model"
    model.mkdir()
    (model / "config.json").write_text('{}')

    registry_response = {
        "found": True,
        "version": 1,
        "manifest": {
            "model_dir": str(model.resolve()),
            "pattern_id": "test-skill",
            "scores": {"exact_match": 0.85},
        },
    }

    with mock.patch("apprentice_serving.server.httpx") as mock_httpx:
        mock_httpx.get.return_value.raise_for_status.return_value = None
        mock_httpx.get.return_value.json.return_value = registry_response
        result = resolve_model_path(pattern_id="test-skill")
        assert result == model.resolve()


def test_resolve_from_registry_not_found():
    with mock.patch("apprentice_serving.server.httpx") as mock_httpx:
        mock_httpx.get.return_value.raise_for_status.return_value = None
        mock_httpx.get.return_value.json.return_value = {"found": False}
        with pytest.raises(RuntimeError, match="not found in registry"):
            resolve_model_path(pattern_id="nonexistent")


def test_resolve_from_registry_manifest_is_string(tmp_path: Path):
    model = tmp_path / "model"
    model.mkdir()
    (model / "config.json").write_text('{}')
    manifest = {"model_dir": str(model.resolve())}

    with mock.patch("apprentice_serving.server.httpx") as mock_httpx:
        mock_httpx.get.return_value.raise_for_status.return_value = None
        mock_httpx.get.return_value.json.return_value = {
            "found": True,
            "manifest": json.dumps(manifest),
        }
        result = resolve_model_path(pattern_id="test-skill")
        assert result == model.resolve()


# --- build_vllm_cmd ------------------------------------------------------

def test_build_vllm_cmd_defaults(tmp_path: Path):
    cmd = build_vllm_cmd(tmp_path)
    assert cmd[0] == "vllm"
    assert cmd[1] == "serve"
    assert str(tmp_path) in cmd
    assert "--host" in cmd
    assert "0.0.0.0" in cmd
    assert "--port" in cmd
    assert "8000" in cmd
    assert "--gpu-memory-utilization" in cmd
    assert "--max-model-len" in cmd


def test_build_vllm_cmd_custom_port(tmp_path: Path):
    cmd = build_vllm_cmd(tmp_path, port=9999)
    idx = cmd.index("--port")
    assert cmd[idx + 1] == "9999"


def test_build_vllm_cmd_custom_gpu_frac(tmp_path: Path):
    cmd = build_vllm_cmd(tmp_path, gpu_memory_utilization=0.75)
    idx = cmd.index("--gpu-memory-utilization")
    assert cmd[idx + 1] == "0.75"


# --- check_only ----------------------------------------------------------

def test_check_only_model_dir_valid(tmp_path: Path):
    model = tmp_path / "model"
    model.mkdir()
    (model / "config.json").write_text("{}")
    rc = check_only(model_dir=str(model))
    assert rc == 0


def test_check_only_model_dir_missing():
    rc = check_only(model_dir="/nonexistent")
    assert rc == 1


def test_check_only_missing_both():
    rc = check_only()
    assert rc == 1
