"""Idempotent ~/.apprentice/.env read/merge/write (W5 → v0.2).

The installer collects secrets/settings (Telegram token + chat-id,
OpenRouter key, RunPod key, base model, monthly budget, admin API key,
isolation profile) and persists them to ``~/.apprentice/.env``.
Re-running preserves existing values for keys not provided this run.
"""

from __future__ import annotations

from pathlib import Path

MANAGED_KEYS = (
    "APPRENTICE_GUEST_BACKEND",
    "APPRENTICE_BASE_MODEL",
    "TELEGRAM_BOT_TOKEN",
    "TELEGRAM_HOME_CHANNEL",
    "OPENROUTER_API_KEY",
    "RUNPOD_API_KEY",
    "APPRENTICE_MONTHLY_BUDGET_USD",
    "APPRENTICE_PROXY_URL",
    "APPRENTICE_TENANTS_ROOT",
    "APPRENTICE_GLOBAL_API_KEY",
    "APPRENTICE_CANARY_STATE_DIR",
)


def parse_env(text: str) -> dict[str, str]:
    out: dict[str, str] = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, val = line.partition("=")
        key = key.strip()
        val = val.strip().strip('"').strip("'")
        if key:
            out[key] = val
    return out


def merge_env(existing: dict[str, str], updates: dict[str, str]) -> dict[str, str]:
    merged = dict(existing)
    for k, v in updates.items():
        if v:
            merged[k] = v
    return merged


def render_env(values: dict[str, str]) -> str:
    lines = ["# Apprentice environment — written by apprentice-setup (v0.2).",
             "# Re-running the installer preserves keys you set by hand.", ""]
    for k in sorted(values):
        lines.append(f"{k}={values[k]}")
    return "\n".join(lines) + "\n"


def load_env_file(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    return parse_env(path.read_text(encoding="utf-8"))


def write_env_file(path: Path, values: dict[str, str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(render_env(values), encoding="utf-8")
