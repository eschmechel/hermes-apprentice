"""``apprentice-registry`` — manage promoted specialist versions (W10).

Subcommands::

    apprentice-registry status  <pattern-id>
    apprentice-registry demote  <pattern-id> [--to-version N]
    apprentice-registry gc      <pattern-id> [--keep K]

``demote`` is an *instant* rollback: it only repoints the ``latest`` symlink, so
the warm multi-LoRA server and the proxy pick up the previous version on the
next ``ensure``/lookup without recopying any weights.
"""

from __future__ import annotations

import argparse
import json
import logging
import sys
from pathlib import Path

from . import registry
from .logging import setup_logging

LOG = logging.getLogger("apprentice_validator.registry_cli")


def _root(args: argparse.Namespace) -> Path | None:
    return Path(args.registry_root).expanduser() if args.registry_root else None


def _emit(obj: dict) -> None:
    sys.stdout.write(json.dumps(obj, indent=2, sort_keys=True) + "\n")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="apprentice-registry",
                                description="Manage promoted specialist versions.")
    p.add_argument("--registry-root", default=None,
                   help="Override registry root (default: ~/.apprentice/registry).")
    p.add_argument("-v", "--verbose", action="store_true")
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("status", help="Show versions + current latest.")
    s.add_argument("pattern_id")

    d = sub.add_parser("demote", help="Repoint latest to a previous version.")
    d.add_argument("pattern_id")
    d.add_argument("--to-version", type=int, default=None,
                   help="Specific version to roll back to (default: next-lower).")

    g = sub.add_parser("gc", help="Prune old versions, keeping the newest K + latest.")
    g.add_argument("pattern_id")
    g.add_argument("--keep", type=int, default=3)
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    setup_logging(logging.DEBUG if args.verbose else logging.INFO)
    root = _root(args)
    skill_dir = (root or registry.DEFAULT_REGISTRY_ROOT) / args.pattern_id

    try:
        if args.cmd == "status":
            _emit({
                "pattern_id": args.pattern_id,
                "versions": registry.list_versions(skill_dir),
                "latest": registry.current_version(skill_dir),
            })
            return 0
        if args.cmd == "demote":
            now = registry.demote(pattern_id=args.pattern_id,
                                  to_version=args.to_version, registry_root=root)
            _emit({"pattern_id": args.pattern_id, "latest": now, "action": "demoted"})
            return 0
        if args.cmd == "gc":
            pruned = registry.garbage_collect(pattern_id=args.pattern_id,
                                              keep=args.keep, registry_root=root)
            _emit({"pattern_id": args.pattern_id, "pruned": pruned,
                   "latest": registry.current_version(skill_dir)})
            return 0
    except (FileNotFoundError, ValueError) as e:
        LOG.error("%s", e)
        return 1
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
