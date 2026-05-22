"""``apprentice-setup`` — interactive (or scripted) installer (W5 → v0.2).

Detects the host (GPU / KVM / Docker / uv), recommends an isolation profile,
collects Telegram + OpenRouter + RunPod + budget + base model settings,
builds Go binaries, reproduces both venvs from the locks, and prints cron
lines to register.

Safe by default: prints the install *plan* and only executes with ``--apply``.
Non-interactive flags make it scriptable (and testable) end-to-end.
"""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

from . import banner
from . import config as cfg_mod
from .detect import detect_environment
from .plan import build_install_commands, preflight, recommend_profile


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[3]


def _prompt(label: str, default: str = "", *, interactive: bool) -> str:
    if not interactive:
        return default
    suffix = f" [{default}]" if default else ""
    try:
        ans = input(f"{label}{suffix}: ").strip()
    except EOFError:
        return default
    return ans or default


def cron_lines(home: Path, profile: str) -> list[str]:
    return [
        "# Apprentice crons (register with your scheduler, e.g. crontab -e):",
        "* * * * * apprentice-telegram dispatch-one                         # deliver queued messages",
        "* * * * * apprentice-telegram poll-replies                          # ingest operator replies",
        "*/2 * * * * apprentice-orchestrator tick                            # drain approvals -> train",
        "*/10 * * * * apprentice-orchestrator safety advance --all            # shadow-diff canary guard",
        f"# isolation profile: {profile}",
    ]


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-setup",
        description="Detect the host, configure, and reproduce the Apprentice venvs + Go binaries.",
    )
    p.add_argument("--non-interactive", action="store_true",
                   help="Never prompt; use flags/defaults (for CI/scripts).")
    p.add_argument("--apply", action="store_true",
                   help="Execute the install plan (default: print it as a dry run).")
    p.add_argument("--profile", choices=["firecracker", "docker", "none"], default=None,
                   help="Force an isolation profile (default: recommend from detection).")
    p.add_argument("--home", default=str(Path.home()), help="Home dir for ~/.apprentice (testing).")

    p.add_argument("--telegram-token", default="")
    p.add_argument("--telegram-chat-id", default="")
    p.add_argument("--openrouter-key", default="")
    p.add_argument("--runpod-key", default="",
                   help="RunPod API key for cloud GPU burst (optional).")
    p.add_argument("--base-model", default="qwen2.5-1.5b",
                   help="Default base model alias (see trainer/supported_models.yaml).")
    p.add_argument("--monthly-budget", type=float, default=20.0,
                   help="Monthly cloud budget in USD (0 = local-only, default: 20).")
    p.add_argument("--global-api-key", default="",
                   help="Admin API key for global tenant patterns (generated if blank).")
    p.add_argument("--enable-monitoring", action="store_true",
                   help="Include Prometheus + Grafana in install plan.")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    interactive = not args.non_interactive
    home = Path(args.home).expanduser()
    repo_root = _repo_root()

    banner.print_banner()
    env = detect_environment()
    print("Detected environment:")
    for k, v in env.as_dict().items():
        print(f"  {k:14} {v}")

    rec_profile, reason = recommend_profile(env)
    profile = args.profile or _prompt(
        f"Isolation profile (recommended: {rec_profile} — {reason})",
        rec_profile, interactive=interactive,
    )
    if profile not in ("firecracker", "docker", "none"):
        print(f"error: invalid profile {profile!r}", file=sys.stderr)
        return 2

    problems = preflight(env)
    if problems:
        print("\nPreflight problems:")
        for pr in problems:
            print(f"  - {pr}")
        if not args.apply:
            print("(fix these before --apply)")

    go_done = env.has_go or _prompt(
        "Go toolchain not found. Build Go binaries anyway? (y/n)", "n",
        interactive=interactive,
    ).lower().startswith("y")

    env_path = home / ".apprentice" / ".env"
    existing = cfg_mod.load_env_file(env_path)

    global_api_key = args.global_api_key or existing.get("APPRENTICE_GLOBAL_API_KEY", "")
    if not global_api_key and interactive:
        import secrets
        global_api_key = secrets.token_hex(16)
        print(f"  Generated admin API key: {global_api_key}")

    updates = {
        "APPRENTICE_GUEST_BACKEND": profile,
        "APPRENTICE_BASE_MODEL": args.base_model or _prompt(
            "Default base model alias (see trainer/supported_models.yaml)",
            existing.get("APPRENTICE_BASE_MODEL", "qwen2.5-1.5b"), interactive=interactive,
        ),
        "TELEGRAM_BOT_TOKEN": args.telegram_token or _prompt(
            "Telegram bot token", existing.get("TELEGRAM_BOT_TOKEN", ""), interactive=interactive,
        ),
        "TELEGRAM_HOME_CHANNEL": args.telegram_chat_id or _prompt(
            "Telegram home chat id", existing.get("TELEGRAM_HOME_CHANNEL", ""), interactive=interactive,
        ),
        "OPENROUTER_API_KEY": args.openrouter_key or _prompt(
            "OpenRouter API key (optional)", existing.get("OPENROUTER_API_KEY", ""), interactive=interactive,
        ),
        "RUNPOD_API_KEY": args.runpod_key or _prompt(
            "RunPod API key (optional, for cloud GPU burst)",
            existing.get("RUNPOD_API_KEY", ""), interactive=interactive,
        ),
        "APPRENTICE_MONTHLY_BUDGET_USD": str(args.monthly_budget) if args.monthly_budget != 20.0
            else _prompt("Monthly cloud budget in USD (0 = local-only)",
                         existing.get("APPRENTICE_MONTHLY_BUDGET_USD", "20"), interactive=interactive,
        ),
        "APPRENTICE_PROXY_URL": "http://localhost:8083",
        "APPRENTICE_TENANTS_ROOT": str(home / ".apprentice" / "tenants"),
        "APPRENTICE_GLOBAL_API_KEY": global_api_key,
        "APPRENTICE_CANARY_STATE_DIR": str(home / ".apprentice" / "proxy" / "canary"),
    }
    merged = cfg_mod.merge_env(existing, updates)

    cmds = build_install_commands(env, repo_root=repo_root, home=home,
                                  build_go=go_done, monitoring=args.enable_monitoring)
    print("\nInstall plan (reproduce venvs from locks + build Go binaries):")
    for c in cmds:
        print("  $ " + " ".join(c))

    print("\nCrons to register:")
    for line in cron_lines(home, profile):
        print("  " + line)

    if not args.apply:
        print(f"\nDry run. Re-run with --apply to write {env_path} and build the venvs.")
        return 0

    cfg_mod.write_env_file(env_path, merged)
    print(f"\nWrote {env_path}")
    if problems:
        print("Refusing to build venvs while preflight problems remain.", file=sys.stderr)
        return 1
    for c in cmds:
        print("  $ " + " ".join(c))
        rc = subprocess.run(c).returncode
        if rc != 0:
            print(f"command failed (rc={rc}); stopping.", file=sys.stderr)
            return 1
    print("\nDone. Specialists will train + serve from the two venvs.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
