"""Tests for ``apprentice_trainer.models`` — supported-models registry."""

from __future__ import annotations

from pathlib import Path

import pytest

from apprentice_trainer import models

SAMPLE_YAML = """\
base_models:
  - id: unsloth/Qwen2.5-1.5B-Instruct
    quantized_id: unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit
    default: true
    min_vram_gb: 6
    license: Apache 2.0

  - id: unsloth/Llama-3.2-3B-Instruct
    quantized_id: unsloth/Llama-3.2-3B-Instruct-bnb-4bit
    default: false
    min_vram_gb: 10
    license: Llama 3.2 Community
"""


@pytest.fixture
def yaml_path(tmp_path: Path) -> Path:
    p = tmp_path / "supported_models.yaml"
    p.write_text(SAMPLE_YAML)
    return p


# ── load_supported_models ───────────────────────────────────────────────────

def test_load_supported_models_returns_list(yaml_path: Path):
    models_list = models.load_supported_models(yaml_path)
    assert len(models_list) == 2
    assert models_list[0]["id"] == "unsloth/Qwen2.5-1.5B-Instruct"
    assert models_list[0]["default"] is True


def test_load_supported_models_file_not_found():
    with pytest.raises(FileNotFoundError):
        models.load_supported_models("/nonexistent/path.yaml")


def test_load_supported_models_empty_yaml(tmp_path: Path):
    p = tmp_path / "empty.yaml"
    p.write_text("")
    assert models.load_supported_models(p) == []


# ── get_default_model ───────────────────────────────────────────────────────

def test_get_default_model(yaml_path: Path):
    default = models.get_default_model(yaml_path)
    assert default == "unsloth/Qwen2.5-1.5B-Instruct"


def test_get_default_model_no_default(tmp_path: Path):
    p = tmp_path / "no_default.yaml"
    p.write_text("""\
base_models:
  - id: unsloth/Foo
    default: false
""")
    with pytest.raises(ValueError, match="no entry with default: true"):
        models.get_default_model(p)


# ── get_model_config ────────────────────────────────────────────────────────

def test_get_model_config_by_id(yaml_path: Path):
    cfg = models.get_model_config("unsloth/Qwen2.5-1.5B-Instruct", yaml_path)
    assert cfg is not None
    assert cfg["min_vram_gb"] == 6


def test_get_model_config_by_quantized_id(yaml_path: Path):
    cfg = models.get_model_config("unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit", yaml_path)
    assert cfg is not None
    assert cfg["id"] == "unsloth/Qwen2.5-1.5B-Instruct"


def test_get_model_config_not_found(yaml_path: Path):
    assert models.get_model_config("nonexistent/model", yaml_path) is None


# ── resolve_model ───────────────────────────────────────────────────────────

def test_resolve_default_returns_default_model(yaml_path: Path):
    resolved = models.resolve_model(None, load_in_4bit=False, path=yaml_path)
    assert resolved == "unsloth/Qwen2.5-1.5B-Instruct"


def test_resolve_default_with_4bit_returns_quantized(yaml_path: Path):
    resolved = models.resolve_model(None, load_in_4bit=True, path=yaml_path)
    assert resolved == "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"


def test_resolve_exact_full_id(yaml_path: Path):
    resolved = models.resolve_model("unsloth/Qwen2.5-1.5B-Instruct",
                                    load_in_4bit=False, path=yaml_path)
    assert resolved == "unsloth/Qwen2.5-1.5B-Instruct"


def test_resolve_quantized_id_directly(yaml_path: Path):
    qid = "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"
    resolved = models.resolve_model(qid, load_in_4bit=False, path=yaml_path)
    assert resolved == qid  # already quantized, return as-is


def test_resolve_by_repo_name(yaml_path: Path):
    resolved = models.resolve_model("Qwen2.5-1.5B-Instruct", load_in_4bit=False,
                                    path=yaml_path)
    assert resolved == "unsloth/Qwen2.5-1.5B-Instruct"


def test_resolve_by_alias_case_insensitive(yaml_path: Path):
    resolved = models.resolve_model("qwen2.5-1.5b", load_in_4bit=False,
                                    path=yaml_path)
    assert resolved == "unsloth/Qwen2.5-1.5B-Instruct"


def test_resolve_alias_with_4bit_returns_quantized(yaml_path: Path):
    resolved = models.resolve_model("qwen2.5-1.5b", load_in_4bit=True,
                                    path=yaml_path)
    assert resolved == "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"


def test_resolve_llama_alias(yaml_path: Path):
    resolved = models.resolve_model("llama-3.2-3b", load_in_4bit=False,
                                    path=yaml_path)
    assert resolved == "unsloth/Llama-3.2-3B-Instruct"


def test_resolve_model_no_quantized_id_returns_id_directly(yaml_path: Path):
    p = yaml_path
    p.write_text("""\
base_models:
  - id: Qwen/Qwen2.5-1.5B-Instruct
    default: true
    min_vram_gb: 6
    license: Apache 2.0
""")
    resolved = models.resolve_model(None, load_in_4bit=True, path=p)
    assert resolved == "Qwen/Qwen2.5-1.5B-Instruct"  # no quantized variant


def test_resolve_unknown_model_raises(yaml_path: Path):
    with pytest.raises(ValueError, match="Unknown model"):
        models.resolve_model("completely/unknown", path=yaml_path)


# ── list_models ─────────────────────────────────────────────────────────────

def test_list_models(yaml_path: Path):
    models_list = models.list_models(yaml_path)
    assert len(models_list) == 2
    assert models_list[0]["id"].startswith("unsloth/")
