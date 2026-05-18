#!/bin/bash
set -e

FC_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "${FC_DIR}/.." && pwd)"
FIRECRACKER="${FC_DIR}/firecracker"
CONFIG_TMPL="${FC_DIR}/vm-config.json.tmpl"
CONFIG="${FC_DIR}/vm-config.json"
SOCK="/tmp/firecracker.sock"
TAP_DEV="fc-tap0"
TAP_IP="10.0.2.1"
GUEST_IP="10.0.2.2"
GUEST_MAC="06:00:00:00:00:01"
NAT_SUBNET="10.0.2.0/24"
EXPECTED_FC_VERSION="v1.15.1"
# Match by --api-sock so bind-mount path differences don't fool pkill.
FC_PATTERN="firecracker --api-sock ${SOCK}"

echo "=== Firecracker VM Startup ==="

if [ ! -f "$FIRECRACKER" ]; then
    echo "ERROR: Firecracker binary not found at $FIRECRACKER"
    echo "       Run: bash ${FC_DIR}/bootstrap.sh"
    exit 1
fi

ACTUAL_FC_VERSION="$("$FIRECRACKER" --version 2>/dev/null | head -1 | awk '{print $2}')"
if [ "$ACTUAL_FC_VERSION" != "$EXPECTED_FC_VERSION" ]; then
    echo "ERROR: Firecracker version mismatch."
    echo "       Expected: $EXPECTED_FC_VERSION"
    echo "       Found:    $ACTUAL_FC_VERSION"
    echo "       Re-run bootstrap.sh, or update EXPECTED_FC_VERSION in this script if the upgrade is intentional."
    exit 1
fi

if [ ! -f "${FC_DIR}/boot/vmlinux" ] && [ ! -L "${FC_DIR}/boot/vmlinux" ]; then
    echo "ERROR: Kernel not found at ${FC_DIR}/boot/vmlinux"
    echo "       Run: bash ${FC_DIR}/bootstrap.sh"
    exit 1
fi

if [ ! -f "${FC_DIR}/rootfs.ext4" ]; then
    echo "ERROR: rootfs.ext4 not found at ${FC_DIR}/rootfs.ext4"
    echo "       Run: bash ${FC_DIR}/build-rootfs.sh"
    exit 1
fi

if [ ! -f "$CONFIG_TMPL" ]; then
    echo "ERROR: VM config template not found at $CONFIG_TMPL"
    exit 1
fi

echo "Rendering vm-config.json from template with REPO_DIR=${REPO_DIR}..."
sed "s|@REPO_DIR@|${REPO_DIR}|g" "$CONFIG_TMPL" > "$CONFIG"

echo "Killing any stale Firecracker processes (they hold TAP fds)..."
sudo pkill -KILL -f "$FC_PATTERN" 2>/dev/null || true
pkill -KILL -f "$FC_PATTERN" 2>/dev/null || true
rm -f "$SOCK"

echo "Setting up TAP device..."
if ip link show "$TAP_DEV" &>/dev/null; then
    echo "Cleaning up existing TAP device $TAP_DEV..."
    sudo ip link set "$TAP_DEV" down 2>/dev/null || true
    sudo ip tuntap del dev "$TAP_DEV" mode tap 2>/dev/null || true
fi
for i in 1 2 3 4 5; do
    if ! ip link show "$TAP_DEV" &>/dev/null; then break; fi
    echo "  device still present, waiting ($i/5)..."
    sleep 1
done
if ip link show "$TAP_DEV" &>/dev/null; then
    echo "ERROR: $TAP_DEV still present after delete. Manually check:"
    echo "  lsof /dev/net/tun"
    echo "  pgrep -af firecracker"
    exit 1
fi
echo "Creating TAP device $TAP_DEV..."
sudo ip tuntap add dev "$TAP_DEV" mode tap
sudo ip addr add "${TAP_IP}/24" dev "$TAP_DEV"
sudo ip link set "$TAP_DEV" up

echo "Configuring NAT..."
sudo sysctl -w net.ipv4.ip_forward=1
sudo iptables -t nat -C POSTROUTING -s "$NAT_SUBNET" ! -o "$TAP_DEV" -j MASQUERADE 2>/dev/null || \
    sudo iptables -t nat -A POSTROUTING -s "$NAT_SUBNET" ! -o "$TAP_DEV" -j MASQUERADE

echo "Starting Firecracker VM..."
rm -f /tmp/firecracker.log
"$FIRECRACKER" \
    --api-sock "$SOCK" \
    --config-file "$CONFIG" \
    > /tmp/firecracker.log 2>&1 &

FC_PID=$!
echo "Firecracker PID: $FC_PID"

for i in {1..30}; do
    if [ -S "$SOCK" ]; then break; fi
    sleep 0.2
done

# Hand the API socket back to the invoking user so vm.sh api works without sudo.
if [ -S "$SOCK" ] && [ -n "${SUDO_USER:-}" ]; then
    chown "${SUDO_USER}:${SUDO_USER}" "$SOCK" 2>/dev/null || true
fi

sleep 10

if kill -0 $FC_PID 2>/dev/null; then
    echo "=== VM IS RUNNING ==="
    echo "Guest reachable at: ssh root@${GUEST_IP}"
    echo "=== Console output: ==="
    cat /tmp/firecracker.log
    echo "=== End ==="
else
    echo "ERROR: VM failed to start"
    cat /tmp/firecracker.log
    exit 1
fi
