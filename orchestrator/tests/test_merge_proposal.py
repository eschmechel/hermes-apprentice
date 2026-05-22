"""Tests for merge proposal MCP tool (`propose_merge`)."""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest


@pytest.fixture
def orch_env(tmp_path, monkeypatch):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path / "root"))
    monkeypatch.setenv("APPRENTICE_VENV_TRAIN", str(tmp_path / "vtrain"))
    monkeypatch.setenv("APPRENTICE_VENV_SERVE", str(tmp_path / "vserve"))
    monkeypatch.delenv("APPRENTICE_TRAIN_PROFILE", raising=False)
    from apprentice_orchestrator.config import Config
    return Config()


@pytest.fixture
def propose_merge_tool(orch_env):
    from apprentice_orchestrator.mcp_server import _build_server
    mcp = _build_server()
    tools = mcp._tool_manager.list_tools()
    for t in tools:
        if t.name == "propose_merge":
            return t.fn
    raise RuntimeError("propose_merge tool not found")


def test_propose_merge_returns_proposal(propose_merge_tool):
    with patch("apprentice_orchestrator.mcp_server.shutil.which") as mock_which:
        mock_which.return_value = "/usr/bin/dataset-builder"
        with patch("apprentice_orchestrator.mcp_server.subprocess.run") as mock_run:
            mock_proc = MagicMock()
            mock_proc.returncode = 0
            mock_proc.stdout = ""
            mock_proc.stderr = ""
            mock_run.return_value = mock_proc

            result = propose_merge_tool(
                parent_a="p1", parent_b="p2", merged_id="merged-p12",
                description="Test merge"
            )

    assert result["merged_id"] == "merged-p12"
    assert result["parents"] == ["p1", "p2"]
    assert result["status"] == "proposed"
    assert "next_step" in result


def test_propose_merge_default_merged_id(propose_merge_tool):
    with patch("apprentice_orchestrator.mcp_server.shutil.which") as mock_which:
        mock_which.return_value = "/usr/bin/dataset-builder"
        with patch("apprentice_orchestrator.mcp_server.subprocess.run") as mock_run:
            mock_proc = MagicMock()
            mock_proc.returncode = 0
            mock_proc.stdout = ""
            mock_proc.stderr = ""
            mock_run.return_value = mock_proc

            result = propose_merge_tool(parent_a="p1", parent_b="p2")

    assert result["merged_id"] == "p1+p2"
    assert result["status"] == "proposed"


def test_propose_merge_handles_dataset_builder_failure(propose_merge_tool):
    with patch("apprentice_orchestrator.mcp_server.shutil.which") as mock_which:
        mock_which.return_value = "/usr/bin/dataset-builder"
        with patch("apprentice_orchestrator.mcp_server.subprocess.run") as mock_run:
            mock_proc = MagicMock()
            mock_proc.returncode = 1
            mock_proc.stdout = ""
            mock_proc.stderr = "error: something went wrong"
            mock_run.return_value = mock_proc

            result = propose_merge_tool(parent_a="p1", parent_b="p2")

    assert "error" in result
    assert "merge failed" in result["error"]


def test_propose_merge_handles_missing_dataset_builder(propose_merge_tool):
    with patch("apprentice_orchestrator.mcp_server.shutil.which") as mock_which:
        mock_which.return_value = None

        result = propose_merge_tool(parent_a="p1", parent_b="p2")

    assert "error" in result
    assert "not found" in result["error"]


def test_propose_merge_writes_candidate(orch_env, propose_merge_tool):
    with patch("apprentice_orchestrator.mcp_server.shutil.which") as mock_which:
        mock_which.side_effect = lambda cmd: "/usr/bin/" + cmd if cmd == "dataset-builder" else None

        with patch("apprentice_orchestrator.mcp_server.subprocess.run") as mock_run:
            mock_proc = MagicMock()
            mock_proc.returncode = 0
            mock_proc.stdout = ""
            mock_proc.stderr = ""
            mock_run.return_value = mock_proc

            result = propose_merge_tool(
                parent_a="p1", parent_b="p2", merged_id="test-candidate"
            )

    assert result["cid"] is not None
    cand_dir = orch_env.candidates_dir
    assert cand_dir.exists()
    cand_files = list(cand_dir.glob("*.json"))
    assert len(cand_files) >= 1
    cand = json.loads(cand_files[0].read_text())
    assert cand["pattern_id"] == "test-candidate"
    assert cand["salt"] == "merge"
