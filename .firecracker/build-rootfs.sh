#!/bin/bash
# Build rootfs.ext4 from .firecracker/Dockerfile.
# Runs entirely through docker — no host sudo, no loop devices required.
# Uses `mkfs.ext4 -d <stage_dir>` to populate the image directly.
set -euo pipefail

FC_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE_TAG="hermes-fc-rootfs:v2026.5.16"
EXT4_SIZE_MB=4096
EXT4_PATH="${FC_DIR}/rootfs.ext4"
HOST_UID="$(id -u)"
HOST_GID="$(id -g)"

echo "=== 1/3: Building Docker image ${IMAGE_TAG} ==="
docker build -t "$IMAGE_TAG" -f "${FC_DIR}/Dockerfile" "${FC_DIR}"

echo "=== 2/3: Pre-allocating ${EXT4_SIZE_MB} MiB ext4 file ==="
rm -f "$EXT4_PATH"
truncate -s "${EXT4_SIZE_MB}M" "$EXT4_PATH"

echo "=== 3/3: Populating rootfs.ext4 inside container (no loop, no privileged) ==="
docker run --rm \
    -v "${EXT4_PATH}:/rootfs.ext4" \
    -e "HOST_UID=${HOST_UID}" \
    -e "HOST_GID=${HOST_GID}" \
    "$IMAGE_TAG" \
    bash -eu -c '
        if ! command -v mkfs.ext4 >/dev/null 2>&1; then
            apt-get update >/dev/null
            apt-get install -y --no-install-recommends e2fsprogs >/dev/null
        fi
        mkdir -p /tmp/stage
        for d in bin sbin etc home lib lib64 opt root srv usr var; do
            if [ -e "/$d" ]; then
                cp -a "/$d" /tmp/stage/
            fi
        done
        mkdir -p /tmp/stage/proc /tmp/stage/sys /tmp/stage/dev \
                 /tmp/stage/tmp /tmp/stage/run /tmp/stage/mnt /tmp/stage/media
        ls -la /tmp/stage/sbin/init /tmp/stage/usr/sbin/init /tmp/stage/etc/init.sh 2>&1 \
            | sed "s/^/[verify] /"
        mkfs.ext4 -F -L hermes-rootfs -d /tmp/stage /rootfs.ext4 >/dev/null
        chown "${HOST_UID}:${HOST_GID}" /rootfs.ext4
    '

echo
echo "Done. rootfs.ext4 built:"
ls -lh "$EXT4_PATH"
