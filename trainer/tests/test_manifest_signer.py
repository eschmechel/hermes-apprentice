"""Tests for trainer-07's Ed25519 signer. CPU-only."""

from __future__ import annotations

import os
import stat
from pathlib import Path

import pytest

from apprentice_trainer import manifest_signer as ms


@pytest.fixture
def key_dir(tmp_path: Path) -> Path:
    return tmp_path / "keys"


@pytest.fixture
def manifest(tmp_path: Path) -> Path:
    p = tmp_path / "training_manifest.json"
    p.write_text('{"hello": "world", "exit_code": 0}\n', encoding="utf-8")
    return p


def test_ensure_keypair_generates_on_first_run(key_dir: Path):
    priv, pub = ms.ensure_keypair(key_dir)
    assert priv.exists() and priv.read_bytes()
    assert pub.exists() and pub.read_bytes()
    # 32-byte raw Ed25519 keys
    assert len(priv.read_bytes()) == 32
    assert len(pub.read_bytes()) == 32


def test_ensure_keypair_private_key_is_0600(key_dir: Path):
    priv, _ = ms.ensure_keypair(key_dir)
    mode = stat.S_IMODE(os.stat(priv).st_mode)
    assert mode == 0o600, f"private key has mode {oct(mode)}, want 0o600"


def test_ensure_keypair_is_idempotent(key_dir: Path):
    priv1, pub1 = ms.ensure_keypair(key_dir)
    b1 = (priv1.read_bytes(), pub1.read_bytes())
    priv2, pub2 = ms.ensure_keypair(key_dir)
    b2 = (priv2.read_bytes(), pub2.read_bytes())
    assert b1 == b2, "second ensure_keypair must NOT regenerate"


def test_sign_then_verify_roundtrip(key_dir: Path, manifest: Path):
    sig = ms.sign_manifest(manifest, key_dir)
    assert sig.exists()
    assert sig.name == manifest.name + ".sig"
    assert ms.verify_manifest(manifest, key_dir) is True


def test_verify_detects_tampering(key_dir: Path, manifest: Path):
    ms.sign_manifest(manifest, key_dir)
    # Tamper with the manifest after signing.
    manifest.write_text(manifest.read_text() + " // gotcha\n")
    assert ms.verify_manifest(manifest, key_dir) is False


def test_signature_is_deterministic(key_dir: Path, manifest: Path):
    """Ed25519 signatures are deterministic by spec — same key + same payload
    must produce byte-identical signatures. This is the trainer-07 acceptance
    bullet that requires no extra machinery; we just verify it holds."""
    sig1 = ms.sign_manifest(manifest, key_dir).read_bytes()
    sig2 = ms.sign_manifest(manifest, key_dir).read_bytes()
    assert sig1 == sig2


def test_verify_returns_false_when_signature_missing(key_dir: Path, manifest: Path):
    # Generate the keypair but never sign.
    ms.ensure_keypair(key_dir)
    assert ms.verify_manifest(manifest, key_dir) is False


def test_verify_returns_false_when_pubkey_missing(tmp_path: Path, manifest: Path):
    # Sign with one keydir, verify against a different one with no pubkey.
    kd1 = tmp_path / "k1"
    kd2 = tmp_path / "k2"
    ms.sign_manifest(manifest, kd1)
    assert ms.verify_manifest(manifest, kd2) is False


def test_cli_sign_then_verify(tmp_path: Path, manifest: Path):
    kd = tmp_path / "kd"
    rc_sign = ms.main(["--key-dir", str(kd), "sign", str(manifest)])
    assert rc_sign == 0
    rc_ok = ms.main(["--key-dir", str(kd), "verify", str(manifest)])
    assert rc_ok == 0


def test_cli_verify_fails_for_bad_sig(tmp_path: Path, manifest: Path):
    kd = tmp_path / "kd"
    ms.main(["--key-dir", str(kd), "sign", str(manifest)])
    # Tamper.
    manifest.write_text(manifest.read_text() + "garbage")
    rc_bad = ms.main(["--key-dir", str(kd), "verify", str(manifest)])
    assert rc_bad == 1
