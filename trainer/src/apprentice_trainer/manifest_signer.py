"""Ed25519 signing for training_manifest.json (trainer-07).

The Apprentice validator (and any future model-deploy step) needs to know that
a manifest claiming "this LoRA adapter was trained on dataset SHA X with these
hyperparameters" was actually produced by an authorized trainer, not forged
after the fact to bypass the validator.

Ed25519 was chosen for three reasons:
  1. The signature is deterministic — the same private key signing the same
     payload twice produces byte-identical signatures. trainer-07's third
     acceptance bullet ("signature is deterministic for same input") falls out
     of this for free, as long as we also write the manifest deterministically
     (sort_keys=True in manifest_writer).
  2. The keys are tiny (32-byte private / 32-byte public), so we can store
     them as raw bytes in ~/.apprentice/keys/ without any wrapping format.
  3. The `cryptography` library implements it natively without OpenSSL ABI
     concerns.

Usage::

    apprentice-sign sign <manifest.json>      # writes <manifest.json>.sig
    apprentice-sign verify <manifest.json>    # exits 0 if sig valid, 1 if not
    apprentice-sign keygen                    # explicit keygen (auto on first sign)
    apprentice-sign show-pubkey               # print base64 public key
"""

from __future__ import annotations

import argparse
import base64
import logging
import os
import sys
from pathlib import Path

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import (
    Ed25519PrivateKey,
    Ed25519PublicKey,
)

from .train import _setup_logging  # reuse JSON formatter

LOG = logging.getLogger("apprentice_trainer.signer")

DEFAULT_KEY_DIR = Path.home() / ".apprentice" / "keys"
PRIVATE_KEY_FILENAME = "ed25519.priv"
PUBLIC_KEY_FILENAME = "ed25519.pub"
SIGNATURE_SUFFIX = ".sig"


# ---------------------------------------------------------------------------
# Key management
# ---------------------------------------------------------------------------

def ensure_keypair(key_dir: Path | None = None) -> tuple[Path, Path]:
    """Ensure an Ed25519 keypair exists at key_dir; create it on first use.

    Returns (private_key_path, public_key_path). The private key is stored as
    32 raw bytes with mode 0600; the public key as 32 raw bytes with mode 0644.
    """
    key_dir = Path(key_dir) if key_dir is not None else DEFAULT_KEY_DIR
    key_dir.mkdir(parents=True, exist_ok=True)
    priv_path = key_dir / PRIVATE_KEY_FILENAME
    pub_path = key_dir / PUBLIC_KEY_FILENAME

    if priv_path.exists() and pub_path.exists():
        return priv_path, pub_path

    priv = Ed25519PrivateKey.generate()
    priv_bytes = priv.private_bytes_raw()
    pub_bytes = priv.public_key().public_bytes_raw()

    # Atomic + restrictive permissions on the private key. We write to a tmp
    # file with mode 0600 BEFORE renaming so the file never exists at world-
    # readable mode even for an instant.
    tmp_priv = priv_path.with_suffix(priv_path.suffix + ".tmp")
    fd = os.open(str(tmp_priv), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, priv_bytes)
    finally:
        os.close(fd)
    os.replace(tmp_priv, priv_path)

    pub_path.write_bytes(pub_bytes)
    pub_path.chmod(0o644)

    LOG.info("ed25519 keypair generated",
             extra={"key_dir": str(key_dir),
                    "pubkey_b64": base64.b64encode(pub_bytes).decode("ascii")})
    return priv_path, pub_path


def load_private_key(priv_path: Path) -> Ed25519PrivateKey:
    return Ed25519PrivateKey.from_private_bytes(priv_path.read_bytes())


def load_public_key(pub_path: Path) -> Ed25519PublicKey:
    return Ed25519PublicKey.from_public_bytes(pub_path.read_bytes())


# ---------------------------------------------------------------------------
# Sign / verify
# ---------------------------------------------------------------------------

def sign_manifest(manifest_path: Path | str, key_dir: Path | None = None) -> Path:
    """Sign <manifest_path>'s raw bytes with the ed25519 private key.

    Writes the signature to <manifest_path>.sig (raw 64-byte signature).
    Generates the keypair on first call. Returns the signature path.
    """
    manifest_path = Path(manifest_path).resolve()
    if not manifest_path.exists():
        raise FileNotFoundError(manifest_path)
    priv_path, _ = ensure_keypair(key_dir)
    priv = load_private_key(priv_path)
    payload = manifest_path.read_bytes()
    sig = priv.sign(payload)  # Ed25519 — deterministic
    sig_path = manifest_path.with_name(manifest_path.name + SIGNATURE_SUFFIX)
    sig_path.write_bytes(sig)
    LOG.info("manifest signed",
             extra={"manifest": str(manifest_path), "signature": str(sig_path),
                    "signature_len": len(sig)})
    return sig_path


def verify_manifest(manifest_path: Path | str, key_dir: Path | None = None) -> bool:
    """Verify <manifest_path>.sig against <manifest_path>. Returns True/False."""
    manifest_path = Path(manifest_path).resolve()
    sig_path = manifest_path.with_name(manifest_path.name + SIGNATURE_SUFFIX)
    if not sig_path.exists():
        LOG.error("signature file missing", extra={"signature": str(sig_path)})
        return False
    key_dir = Path(key_dir) if key_dir is not None else DEFAULT_KEY_DIR
    pub_path = key_dir / PUBLIC_KEY_FILENAME
    if not pub_path.exists():
        LOG.error("public key missing", extra={"public_key": str(pub_path)})
        return False
    pub = load_public_key(pub_path)
    payload = manifest_path.read_bytes()
    signature = sig_path.read_bytes()
    try:
        pub.verify(signature, payload)
    except InvalidSignature:
        LOG.warning("signature verification FAILED", extra={"manifest": str(manifest_path)})
        return False
    LOG.info("signature verification OK", extra={"manifest": str(manifest_path)})
    return True


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-sign",
        description="Ed25519 sign + verify for training_manifest.json.",
    )
    p.add_argument("--key-dir", default=None,
                   help=f"Where to read/write the keypair. Defaults to {DEFAULT_KEY_DIR}.")
    p.add_argument("-v", "--verbose", action="store_true")
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("sign", help="Sign a manifest. Writes <path>.sig next to it.")
    s.add_argument("manifest", help="Path to training_manifest.json (or any file).")

    v = sub.add_parser("verify", help="Verify <path>.sig against <path>. Exit 0 ok, 1 bad.")
    v.add_argument("manifest", help="Path to training_manifest.json.")

    sub.add_parser("keygen", help="Generate the keypair without signing anything.")
    sub.add_parser("show-pubkey", help="Print the public key (base64).")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    _setup_logging(logging.DEBUG if args.verbose else logging.INFO)
    key_dir = Path(args.key_dir) if args.key_dir else None

    try:
        if args.cmd == "sign":
            sign_manifest(args.manifest, key_dir)
            return 0
        if args.cmd == "verify":
            return 0 if verify_manifest(args.manifest, key_dir) else 1
        if args.cmd == "keygen":
            priv, pub = ensure_keypair(key_dir)
            print(priv, file=sys.stderr)
            print(pub, file=sys.stderr)
            return 0
        if args.cmd == "show-pubkey":
            _, pub_path = ensure_keypair(key_dir)
            print(base64.b64encode(pub_path.read_bytes()).decode("ascii"))
            return 0
    except FileNotFoundError as e:
        LOG.error("file not found", extra={"path": str(e)})
        return 1
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
