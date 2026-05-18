#!/bin/bash
# Lifecycle wrapper for the Firecracker microVM.
# Subcommands: status | start | stop | restart | logs | ssh | api
set -euo pipefail

FC_DIR="$(cd "$(dirname "$0")" && pwd)"
TAP_DEV="fc-tap0"
GUEST_IP="10.0.2.2"
SOCK="/tmp/firecracker.sock"
LOG="/tmp/firecracker.log"
# Match the firecracker process by its API socket argument, not by binary path.
# Paths differ between bind mounts (/mnt/GOONDRIVE vs /home/Eragon) while the
# --api-sock argument is the same regardless of how start-vm.sh was invoked.
FC_PATTERN="firecracker --api-sock ${SOCK}"

usage() {
    cat <<EOF
Usage: $(basename "$0") <command>

  status     Show whether the VM, API socket, and guest are reachable
  start      Boot the VM (delegates to sudo bash start-vm.sh)
  start-ssh  Boot the VM, wait for sshd, then drop into an ssh session
  stop       Graceful SIGTERM to firecracker + tear down TAP device
  restart    stop then start
  logs       Tail /tmp/firecracker.log (Ctrl-C to exit)
  ssh        ssh root@${GUEST_IP} with sane defaults
  api        Raw Firecracker API helper: e.g. \`vm.sh api GET /\` or \`vm.sh api PUT /actions '{"action_type":"SendCtrlAltDel"}'\`
EOF
}

cmd_status() {
    local pids
    pids="$(pgrep -af "$FC_PATTERN" 2>/dev/null || true)"
    if [ -n "$pids" ]; then
        echo "firecracker: RUNNING"
        printf '%s\n' "$pids" | sed 's/^/  /'
    else
        echo "firecracker: not running"
    fi

    if [ -S "$SOCK" ]; then
        echo "api socket:  $SOCK (present)"
    else
        echo "api socket:  absent"
    fi

    if ip link show "$TAP_DEV" &>/dev/null; then
        echo "tap device:  $TAP_DEV up"
    else
        echo "tap device:  absent"
    fi

    if ssh -o ConnectTimeout=2 -o StrictHostKeyChecking=accept-new \
           -o LogLevel=ERROR -o BatchMode=yes \
           "root@${GUEST_IP}" 'true' 2>/dev/null; then
        echo "guest ssh:   reachable at root@${GUEST_IP}"
    else
        echo "guest ssh:   unreachable (VM may still be booting or sshd not up)"
    fi
}

cmd_start() {
    exec sudo bash "${FC_DIR}/start-vm.sh"
}

cmd_start_ssh() {
    sudo bash "${FC_DIR}/start-vm.sh"
    echo
    echo "Waiting for sshd on root@${GUEST_IP}..."
    local attempts=20
    for i in $(seq 1 $attempts); do
        if ssh -o ConnectTimeout=1 -o StrictHostKeyChecking=accept-new \
               -o LogLevel=ERROR -o BatchMode=yes \
               "root@${GUEST_IP}" 'true' 2>/dev/null; then
            echo "sshd up (attempt $i/$attempts). Connecting..."
            exec ssh -o StrictHostKeyChecking=accept-new "root@${GUEST_IP}"
        fi
        sleep 1
    done
    echo "sshd did not come up within ${attempts}s — investigate with:"
    echo "  $(basename "$0") status"
    echo "  $(basename "$0") logs"
    exit 1
}

cmd_stop() {
    if ! pgrep -f "$FC_PATTERN" >/dev/null 2>&1 && ! ip link show "$TAP_DEV" &>/dev/null; then
        echo "VM not running and no TAP device — nothing to stop."
        return 0
    fi

    echo "Sending SIGTERM to firecracker..."
    sudo pkill -TERM -f "$FC_PATTERN" 2>/dev/null || true

    # Wait up to 5s for graceful exit
    for i in 1 2 3 4 5; do
        if ! pgrep -f "$FC_PATTERN" >/dev/null 2>&1; then break; fi
        sleep 1
    done
    if pgrep -f "$FC_PATTERN" >/dev/null 2>&1; then
        echo "firecracker didn't exit on SIGTERM; sending SIGKILL..."
        sudo pkill -KILL -f "$FC_PATTERN" 2>/dev/null || true
    fi

    rm -f "$SOCK"

    if ip link show "$TAP_DEV" &>/dev/null; then
        echo "Tearing down TAP device $TAP_DEV..."
        sudo ip link set "$TAP_DEV" down 2>/dev/null || true
        sudo ip tuntap del dev "$TAP_DEV" mode tap 2>/dev/null || true
    fi
    echo "Stopped."
}

cmd_restart() {
    cmd_stop
    cmd_start
}

cmd_logs() {
    if [ ! -f "$LOG" ]; then
        echo "No log at $LOG — has the VM ever been started?"
        exit 1
    fi
    exec tail -F "$LOG"
}

cmd_ssh() {
    shift || true
    exec ssh -o StrictHostKeyChecking=accept-new "root@${GUEST_IP}" "$@"
}

cmd_api() {
    shift || true
    if [ $# -lt 2 ]; then
        echo "Usage: vm.sh api <METHOD> <PATH> [json-body]"
        echo "  vm.sh api GET /"
        echo "  vm.sh api PUT /actions '{\"action_type\":\"SendCtrlAltDel\"}'"
        exit 2
    fi
    local method="$1" path="$2" body="${3-}"
    if [ ! -S "$SOCK" ]; then
        echo "API socket $SOCK absent — VM not running?"
        exit 1
    fi
    if [ -n "$body" ]; then
        curl -fsS --unix-socket "$SOCK" -X "$method" "http://localhost${path}" \
             -H 'Content-Type: application/json' -d "$body"
    else
        curl -fsS --unix-socket "$SOCK" -X "$method" "http://localhost${path}"
    fi
    echo
}

main() {
    if [ $# -lt 1 ]; then
        usage
        exit 2
    fi
    case "$1" in
        status)    shift; cmd_status "$@" ;;
        start)     shift; cmd_start  "$@" ;;
        start-ssh) shift; cmd_start_ssh "$@" ;;
        stop)      shift; cmd_stop   "$@" ;;
        restart)   shift; cmd_restart "$@" ;;
        logs)    shift; cmd_logs   "$@" ;;
        ssh)     cmd_ssh "$@" ;;
        api)     cmd_api "$@" ;;
        -h|--help|help) usage; exit 0 ;;
        *)
            echo "Unknown command: $1"
            usage
            exit 2
            ;;
    esac
}

main "$@"
