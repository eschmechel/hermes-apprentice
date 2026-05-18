#!/bin/bash
# Fetch the Firecracker binary and Linux kernel needed to boot the microVM.
# Idempotent: skips downloads whose SHA256 already matches.
set -euo pipefail

FC_DIR="$(cd "$(dirname "$0")" && pwd)"

# --- Firecracker binary ----------------------------------------------------
FC_VERSION="v1.15.1"
FC_TARBALL_URL="https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-x86_64.tgz"
FC_TARBALL_SHA256="d4a32ab2322d887ca1bc4a4e7afa9cc35393e6362dfc2b3becb389d362e4275a"
FC_BIN_SHA256="7e8b57e88c459396d4680d83dcdd8c7f72305447cb55b11f4ac98ad70a3f7825"
FC_BIN_PATH="${FC_DIR}/firecracker"

# --- Linux kernel (iximiuz labs, virtio-net built in) ----------------------
KERNEL_VERSION="6.18.21"
KERNEL_IMAGE="ghcr.io/iximiuz/labs/kernelfs:6.18-fc-amd64"
KERNEL_SHA256="8c69cbb6fd9615e3bd375ae3840350bf017bf4b7596a53093c59fedbba446d7b"
KERNEL_PATH="${FC_DIR}/boot/vmlinux-${KERNEL_VERSION}"
KERNEL_SYMLINK="${FC_DIR}/boot/vmlinux"

sha_matches() {
    local file="$1" expected="$2"
    [ -f "$file" ] || return 1
    local actual
    actual="$(sha256sum "$file" | awk '{print $1}')"
    [ "$actual" = "$expected" ]
}

mkdir -p "${FC_DIR}/boot"

# --- Firecracker ----------------------------------------------------------
if sha_matches "$FC_BIN_PATH" "$FC_BIN_SHA256"; then
    echo "[firecracker] already present at $FC_BIN_PATH (sha matches)"
else
    echo "[firecracker] downloading ${FC_VERSION} from GitHub releases..."
    tmp_tgz="$(mktemp --suffix=.tgz)"
    trap 'rm -f "$tmp_tgz"' EXIT
    curl -fsSL --retry 3 -o "$tmp_tgz" "$FC_TARBALL_URL"

    actual_sha="$(sha256sum "$tmp_tgz" | awk '{print $1}')"
    if [ "$actual_sha" != "$FC_TARBALL_SHA256" ]; then
        echo "ERROR: tarball SHA256 mismatch"
        echo "       expected: $FC_TARBALL_SHA256"
        echo "       got:      $actual_sha"
        exit 1
    fi

    tmp_extract="$(mktemp -d)"
    trap 'rm -f "$tmp_tgz"; rm -rf "$tmp_extract"' EXIT
    tar -xzf "$tmp_tgz" -C "$tmp_extract"
    extracted_bin="$(find "$tmp_extract" -type f -name "firecracker-${FC_VERSION}-x86_64" | head -1)"
    if [ -z "$extracted_bin" ]; then
        echo "ERROR: could not find firecracker binary inside tarball"
        exit 1
    fi
    install -m 0755 "$extracted_bin" "$FC_BIN_PATH"
    rm -f "$tmp_tgz"
    rm -rf "$tmp_extract"
    trap - EXIT

    if ! sha_matches "$FC_BIN_PATH" "$FC_BIN_SHA256"; then
        echo "WARNING: installed binary SHA256 does not match recorded value ($FC_BIN_SHA256)."
        echo "         This is OK if Firecracker re-packaged the release; verify by running:"
        echo "         $FC_BIN_PATH --version"
    fi
    echo "[firecracker] installed at $FC_BIN_PATH"
fi

# --- Kernel ---------------------------------------------------------------
if sha_matches "$KERNEL_PATH" "$KERNEL_SHA256"; then
    echo "[kernel] already present at $KERNEL_PATH (sha matches)"
else
    echo "[kernel] pulling ${KERNEL_IMAGE}..."
    docker pull "$KERNEL_IMAGE" >/dev/null

    echo "[kernel] extracting /boot/vmlinux-${KERNEL_VERSION}..."
    container_id="$(docker create --entrypoint /dontexist "$KERNEL_IMAGE" placeholder 2>/dev/null || true)"
    if [ -z "$container_id" ]; then
        # Some image configs reject the placeholder; fall back to plain `docker create`.
        container_id="$(docker create "$KERNEL_IMAGE")"
    fi
    docker cp "${container_id}:/boot/vmlinux-${KERNEL_VERSION}" "$KERNEL_PATH"
    docker rm "$container_id" >/dev/null

    if ! sha_matches "$KERNEL_PATH" "$KERNEL_SHA256"; then
        actual="$(sha256sum "$KERNEL_PATH" | awk '{print $1}')"
        echo "WARNING: extracted kernel SHA256 does not match recorded value."
        echo "         expected: $KERNEL_SHA256"
        echo "         got:      $actual"
        echo "         The upstream image may have been re-tagged. Verify the boot still works."
    fi
    echo "[kernel] installed at $KERNEL_PATH"
fi

# Ensure the unversioned symlink points at the current kernel
ln -sf "$KERNEL_PATH" "$KERNEL_SYMLINK"
echo "[kernel] symlink: $KERNEL_SYMLINK -> $KERNEL_PATH"

echo
echo "Bootstrap complete. Next steps:"
echo "  1) bash ${FC_DIR}/build-rootfs.sh     # build the Hermes rootfs.ext4"
echo "  2) sudo bash ${FC_DIR}/start-vm.sh    # boot the microVM"
echo "  3) ssh -o StrictHostKeyChecking=accept-new root@10.0.2.2"
