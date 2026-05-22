"""Environment detection for the Apprentice installer (W5).

All probes take injectable callables so the detection logic is unit-testable
without a real GPU/KVM/Docker.
"""

from __future__ import annotations

import os
import shutil
import subprocess
from dataclasses import dataclass, asdict


@dataclass
class Environment:
    has_gpu: bool
    gpu_name: str | None
    gpu_vram_mb: int | None
    has_kvm: bool
    has_docker: bool
    has_uv: bool
    has_go: bool
    in_vm: bool

    def as_dict(self) -> dict:
        return asdict(self)


def detect_environment(
    *,
    which=shutil.which,
    path_exists=os.path.exists,
    run=subprocess.run,
    read_cpuinfo=None,
) -> Environment:
    gpu_name: str | None = None
    gpu_vram_mb: int | None = None
    has_gpu = False
    if which("nvidia-smi"):
        try:
            out = run(
                ["nvidia-smi", "--query-gpu=name,memory.total",
                 "--format=csv,noheader,nounits"],
                capture_output=True, text=True, timeout=10,
            )
            if out.returncode == 0 and out.stdout.strip():
                first = out.stdout.strip().splitlines()[0]
                parts = [p.strip() for p in first.split(",")]
                has_gpu = True
                gpu_name = parts[0] if parts else None
                if len(parts) > 1 and parts[1].isdigit():
                    gpu_vram_mb = int(parts[1])
        except (OSError, subprocess.SubprocessError):
            pass

    return Environment(
        has_gpu=has_gpu,
        gpu_name=gpu_name,
        gpu_vram_mb=gpu_vram_mb,
        has_kvm=path_exists("/dev/kvm"),
        has_docker=which("docker") is not None,
        has_uv=which("uv") is not None,
        has_go=which("go") is not None,
        in_vm=_detect_vm(read_cpuinfo),
    )


def _detect_vm(read_cpuinfo) -> bool:
    """Heuristic: the CPU 'hypervisor' flag is present inside a guest VM."""
    if read_cpuinfo is None:
        def read_cpuinfo() -> str:
            try:
                with open("/proc/cpuinfo", encoding="utf-8") as fh:
                    return fh.read()
            except OSError:
                return ""
    return "hypervisor" in read_cpuinfo()
