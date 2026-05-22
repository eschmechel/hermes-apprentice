from pathlib import Path

from apprentice_setup import config as cfg_mod
from apprentice_setup.cli import build_parser, cron_lines, main
from apprentice_setup.detect import Environment, detect_environment
from apprentice_setup.plan import build_install_commands, preflight, recommend_profile


# ── detection ────────────────────────────────────────────────────────────────

def test_detect_gpu_and_kvm(monkeypatch):
    class P:
        returncode = 0
        stdout = "NVIDIA GeForce RTX 4060, 8188\n"

    env = detect_environment(
        which=lambda n: "/usr/bin/" + n,            # everything present
        path_exists=lambda p: p == "/dev/kvm",
        run=lambda *a, **k: P(),
        read_cpuinfo=lambda: "flags: fpu vme",      # no 'hypervisor' -> bare metal
    )
    assert env.has_gpu and env.gpu_name == "NVIDIA GeForce RTX 4060"
    assert env.gpu_vram_mb == 8188
    assert env.has_kvm and env.has_docker and env.has_uv and not env.in_vm


def test_detect_in_vm_no_gpu(monkeypatch):
    env = detect_environment(
        which=lambda n: None,                       # nothing installed
        path_exists=lambda p: False,
        run=lambda *a, **k: (_ for _ in ()).throw(FileNotFoundError()),
        read_cpuinfo=lambda: "flags: fpu hypervisor lm",
    )
    assert not env.has_gpu and not env.has_kvm and env.in_vm


# ── profile recommendation ───────────────────────────────────────────────────

def _env(**kw) -> Environment:
    base = dict(has_gpu=True, gpu_name="x", gpu_vram_mb=8000, has_kvm=True,
                has_docker=True, has_uv=True, has_go=True, in_vm=False)
    base.update(kw)
    return Environment(**base)


def test_recommend_firecracker_on_baremetal_kvm():
    p, _ = recommend_profile(_env(has_kvm=True, in_vm=False))
    assert p == "firecracker"


def test_recommend_docker_in_vm():
    p, _ = recommend_profile(_env(has_kvm=True, in_vm=True))
    assert p == "docker"


def test_recommend_docker_without_kvm():
    """No KVM and not in VM — recommend 'none' (raw install)."""
    p, _ = recommend_profile(_env(has_kvm=False, in_vm=False))
    assert p == "none"


def test_recommend_none_returned_when_no_kvm_and_no_docker():
    """Even without docker binary, 'none' is the right fallback."""
    p, _ = recommend_profile(_env(has_kvm=False, in_vm=False, has_docker=False))
    assert p == "none"


def test_cli_dry_run_with_none_profile(tmp_path, monkeypatch, capsys):
    import apprentice_setup.cli as cli
    monkeypatch.setattr(cli, "detect_environment", lambda: _env(has_kvm=False, in_vm=False))
    rc = main([
        "--non-interactive", "--home", str(tmp_path), "--profile", "none",
    ])
    out = capsys.readouterr().out
    assert rc == 0
    assert "Dry run" in out
    assert "none" in out


def test_preflight_flags_missing_uv_and_gpu():
    problems = preflight(_env(has_uv=False, has_gpu=False))
    assert any("uv" in p for p in problems) and any("GPU" in p for p in problems)
    assert preflight(_env()) == []


# ── install plan ─────────────────────────────────────────────────────────────

def test_build_install_commands_uses_locks_and_editables(tmp_path):
    cmds = build_install_commands(_env(), repo_root=Path("/repo"), home=tmp_path)
    flat = [" ".join(c) for c in cmds]
    assert any("uv venv" in c and "venv-train" in c for c in flat)
    assert any("train.lock" in c for c in flat)
    assert any("serve.lock" in c for c in flat)
    assert any("-e /repo/trainer" in c for c in flat)
    assert any("-e /repo/validator" in c for c in flat)
    assert any("-e /repo/serving" in c for c in flat)
    assert any("-e /repo/orchestrator" in c for c in flat)
    assert any("-e /repo/telegram" in c for c in flat)


# ── config merge (idempotent) ────────────────────────────────────────────────

def test_merge_env_preserves_existing_on_blank_update():
    existing = {"TELEGRAM_BOT_TOKEN": "keep", "OTHER": "x"}
    merged = cfg_mod.merge_env(existing, {"TELEGRAM_BOT_TOKEN": "", "OPENROUTER_API_KEY": "new"})
    assert merged["TELEGRAM_BOT_TOKEN"] == "keep"      # blank didn't clobber
    assert merged["OPENROUTER_API_KEY"] == "new"
    assert merged["OTHER"] == "x"                       # untouched key preserved


def test_parse_and_render_roundtrip():
    text = 'A=1\nB="two"\n# comment\nC=three\n'
    parsed = cfg_mod.parse_env(text)
    assert parsed == {"A": "1", "B": "two", "C": "three"}
    again = cfg_mod.parse_env(cfg_mod.render_env(parsed))
    assert again == parsed


# ── CLI dry run (no side effects) ────────────────────────────────────────────

def test_cli_dry_run_writes_nothing(tmp_path, monkeypatch, capsys):
    import apprentice_setup.cli as cli
    monkeypatch.setattr(cli, "detect_environment", lambda: _env())
    rc = main([
        "--non-interactive", "--home", str(tmp_path), "--profile", "docker",
    ])
    out = capsys.readouterr().out
    assert rc == 0
    assert "Dry run" in out and "train.lock" in out
    assert not (tmp_path / ".apprentice" / ".env").exists()  # nothing written


def test_cli_apply_writes_env(tmp_path, monkeypatch, capsys):
    import apprentice_setup.cli as cli
    monkeypatch.setattr(cli, "detect_environment", lambda: _env())
    # Stop before running uv: preflight is clean here, so stub subprocess.run.
    monkeypatch.setattr(cli.subprocess, "run", lambda *a, **k: type("R", (), {"returncode": 0})())
    rc = main([
        "--non-interactive", "--apply", "--home", str(tmp_path), "--profile", "docker",
        "--telegram-token", "tok", "--telegram-chat-id", "chat",
    ])
    assert rc == 0
    env_file = tmp_path / ".apprentice" / ".env"
    assert env_file.exists()
    vals = cfg_mod.parse_env(env_file.read_text())
    assert vals["APPRENTICE_GUEST_BACKEND"] == "docker"
    assert vals["TELEGRAM_BOT_TOKEN"] == "tok"


def test_parser_defaults():
    args = build_parser().parse_args([])
    assert not args.apply and not args.non_interactive


def test_cron_lines_mention_core_jobs(tmp_path):
    lines = "\n".join(cron_lines(tmp_path, "firecracker"))
    assert "apprentice-orchestrator tick" in lines
    assert "safety advance" in lines
    assert "firecracker" in lines


# ── banner ───────────────────────────────────────────────────────────────────

def test_banner_plain_has_no_ansi():
    from apprentice_setup import banner
    out = banner.render(color=False)
    assert "Apprentice" in out and "\033[" not in out


def test_banner_color_has_ansi():
    from apprentice_setup import banner
    out = banner.render(color=True)
    assert "\033[" in out and out.endswith("\033[0m")
