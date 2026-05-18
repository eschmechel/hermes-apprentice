# Firecracker microVM for Hermes

This directory boots [Hermes Agent](https://github.com/NousResearch/hermes-agent) inside a [Firecracker](https://firecracker-microvm.github.io/) microVM so the agent runs isolated from the host.

## Prerequisites

- Linux host with KVM (`/dev/kvm` accessible)
- Docker (used for the rootfs build and to pull the kernel image)
- `sudo` (required for TAP device + iptables NAT setup at boot)
- `iproute2`, `iptables`, `ssh-keygen`
- An SSH key at `~/.ssh/id_ed25519.pub` (or set `SSH_PUBKEY_PATH` to point elsewhere)

## Setup

Run once per fresh clone:

```bash
bash .firecracker/bootstrap.sh    # download firecracker binary + Linux kernel
bash .firecracker/build-rootfs.sh # build rootfs.ext4 from Dockerfile (~5 min first run, cached after)
```

Then to boot:

```bash
bash .firecracker/vm.sh start-ssh    # boots + drops you into an ssh session
```

or step by step:

```bash
bash .firecracker/vm.sh start        # boots in the background
bash .firecracker/vm.sh status       # verify VM + API + sshd
bash .firecracker/vm.sh ssh          # connect to the guest
```

Inside the VM, `hermes --version` should return `Hermes Agent v0.14.0 (2026.5.16)`.

## Lifecycle (`vm.sh`)

| Command | What it does |
|---|---|
| `vm.sh start` | Boot the VM in the background (`sudo bash start-vm.sh`) |
| `vm.sh start-ssh` | Boot, wait for sshd, then `ssh root@10.0.2.2` |
| `vm.sh status` | Print firecracker PID, API sock state, TAP device state, ssh reachability |
| `vm.sh stop` | SIGTERM firecracker, escalate to SIGKILL if needed, tear down `fc-tap0` |
| `vm.sh restart` | `stop` then `start` |
| `vm.sh logs` | `tail -F /tmp/firecracker.log` |
| `vm.sh ssh [cmd]` | `ssh root@10.0.2.2 [cmd]` with `StrictHostKeyChecking=accept-new` |
| `vm.sh api <METHOD> <PATH> [body]` | Raw Firecracker API call over `/tmp/firecracker.sock`. Example: `vm.sh api PUT /actions '{"action_type":"SendCtrlAltDel"}'` |

## Customization

| Variable | Default | Effect |
|---|---|---|
| `SSH_PUBKEY_PATH` | `~/.ssh/id_ed25519.pub` | Public key baked into `/root/.ssh/authorized_keys` during rootfs build. Re-run `build-rootfs.sh` after changing. |

## Files

| Path | Purpose |
|---|---|
| `Dockerfile` | Builds the Debian-based rootfs image with Hermes installed |
| `.dockerignore` | Keeps the build context tiny (only `Dockerfile` is sent to the daemon) |
| `bootstrap.sh` | Downloads firecracker v1.15.1 and the iximiuz labs Linux kernel v6.18.21 |
| `build-rootfs.sh` | Builds `rootfs.ext4` from the Dockerfile via `mkfs.ext4 -d` inside a container — no host sudo, no loop devices |
| `start-vm.sh` | Renders `vm-config.json` from `.tmpl`, sets up TAP/NAT, launches Firecracker |
| `vm.sh` | Lifecycle wrapper: status / start / start-ssh / stop / restart / logs / ssh / api |
| `vm-config.json.tmpl` | Firecracker config template with `@REPO_DIR@` placeholder |

`rootfs.ext4`, `firecracker`, and `boot/vmlinux*` are gitignored — recreate them with `bootstrap.sh` and `build-rootfs.sh`.

## Network

| Endpoint | Address |
|---|---|
| Host TAP `fc-tap0` | `10.0.2.1/24` |
| Guest `eth0` | `10.0.2.2/24` |
| Guest default gateway | `10.0.2.1` |
| Outbound NAT | iptables MASQUERADE on `10.0.2.0/24` |

`start-vm.sh` tears down and recreates `fc-tap0` on each run and kills any stale Firecracker processes that might still hold a TAP file descriptor.

## Troubleshooting

- **`ioctl(TUNSETIFF): Device or resource busy`** — a previous Firecracker process is still holding the TAP fd. `start-vm.sh` now handles this; if you still see it, run `pgrep -af firecracker` and kill manually.
- **`hermes --version` reports "Update available: 282 commits behind"** — expected. The Dockerfile pins to release tag `v2026.5.16`; main HEAD has moved on. Bump the pin in `Dockerfile` and re-run `build-rootfs.sh` if you want a newer Hermes.
- **`sudo: a password is required`** — `build-rootfs.sh` runs everything inside Docker and needs no host sudo. `start-vm.sh` does need sudo (TAP/iptables); run it in a terminal that has TTY access.
- **`Connection refused` on ssh** — give the VM a couple seconds to finish booting. The init script prints `[init] sshd started` when sshd is up.
