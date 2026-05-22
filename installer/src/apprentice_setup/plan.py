"""Profile recommendation + install-plan construction (v0.2).

Builds both Python venvs from locks AND Go binaries, then sets up
the Apprentice runtime environment.
"""

from __future__ import annotations

from pathlib import Path

from .detect import Environment

TRAIN_PACKAGES = ["trainer"]
SERVE_PACKAGES = ["validator", "serving", "orchestrator", "telegram"]

GO_BINARIES = [
    ("proxy", "proxy"),
    ("dataset-builder", "dataset-builder"),
    ("registry-service", "registry-service"),
    ("burst", "burst"),
]


def recommend_profile(env: Environment) -> tuple[str, str]:
    if env.has_kvm and not env.in_vm:
        return ("firecracker", "bare-metal KVM detected; Firecracker microVM gives "
                "the strongest Hermes isolation")
    if env.in_vm:
        return ("docker", "running inside a VM; Docker avoids nested virtualization "
                "(no /dev/kvm passthrough needed)")
    return ("none", "no KVM available; install Apprentice binaries directly "
            "alongside your existing Hermes — no isolation layer")


def build_install_commands(
    env: Environment, *, repo_root: Path, home: Path,
    build_go: bool = True, monitoring: bool = False,
) -> list[list[str]]:
    locks = repo_root / "deploy" / "locks"
    venv_train = home / ".apprentice" / "venv-train"
    venv_serve = home / ".apprentice" / "venv-serve"
    install_bin = home / ".apprentice" / "bin"
    cmds: list[list[str]] = []

    if build_go:
        cmds.append(["mkdir", "-p", str(install_bin)])
        for mod, binary in GO_BINARIES:
            mod_dir = repo_root / mod
            if (mod_dir / "go.mod").exists():
                cmds.append(["go", "build", "-o", str(install_bin / binary), str(mod_dir)])

    cmds.append(["uv", "venv", str(venv_train)])
    cmds.append(["uv", "pip", "install", "--python", str(venv_train / "bin" / "python"),
                 "-r", str(locks / "train.lock")])
    for pkg in TRAIN_PACKAGES:
        cmds.append(["uv", "pip", "install", "--python", str(venv_train / "bin" / "python"),
                     "-e", str(repo_root / pkg)])

    cmds.append(["uv", "venv", str(venv_serve)])
    cmds.append(["uv", "pip", "install", "--python", str(venv_serve / "bin" / "python"),
                 "-r", str(locks / "serve.lock")])
    for pkg in SERVE_PACKAGES:
        cmds.append(["uv", "pip", "install", "--python", str(venv_serve / "bin" / "python"),
                     "-e", str(repo_root / pkg)])

    if monitoring:
        cmds.append(["docker", "compose", "-f",
                     str(repo_root / "deploy" / "docker" / "compose.monitoring.yml"),
                     "up", "-d"])

    return cmds


def preflight(env: Environment) -> list[str]:
    problems = []
    if not env.has_uv:
        problems.append("uv is not installed (https://docs.astral.sh/uv/); required to build the venvs")
    if not env.has_gpu:
        problems.append("no NVIDIA GPU detected (nvidia-smi) — training/serving need CUDA")
    if not env.has_go:
        problems.append("go is not installed; Go binaries (proxy, dataset-builder, "
                        "registry-service, burst) will be skipped unless --enable-go-build is set")
    return problems
