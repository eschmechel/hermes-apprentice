FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl git ca-certificates python3 python3-pip nodejs npm ripgrep \
    iproute2 iputils-ping procps openssh-server \
    && rm -rf /var/lib/apt/lists/*

RUN ssh-keygen -A

RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"

# Pin to Hermes v0.14.0 release tag (v2026.5.16, commit a91a57fa)
RUN curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --branch v2026.5.16

RUN /usr/local/bin/hermes --version

# Install optional Hermes extras into the Hermes venv via uv.
# python-telegram-bot is required for the messaging tool / cron --deliver telegram;
# Hermes treats it as an optional dep, so installs only fail noisily at first use.
RUN VIRTUAL_ENV=/usr/local/lib/hermes-agent/venv \
    /root/.local/bin/uv pip install python-telegram-bot

RUN mkdir -p /root/.hermes/skills

ARG SSH_AUTHORIZED_KEY=""
RUN mkdir -p /root/.ssh \
    && printf '%s\n' "$SSH_AUTHORIZED_KEY" > /root/.ssh/authorized_keys \
    && chmod 0700 /root/.ssh \
    && chmod 0600 /root/.ssh/authorized_keys

RUN printf '%s\n' \
    '#!/bin/sh' \
    'mkdir -p /proc /sys /dev/pts /tmp /run /run/sshd' \
    'mount -t proc proc /proc' \
    'mount -t devpts devpts /dev/pts' \
    'mount -t sysfs sysfs /sys' \
    'ip link set lo up' \
    'ip link set eth0 up' \
    'ip addr add 10.0.2.2/24 dev eth0' \
    'ip route add default via 10.0.2.1' \
    'echo "[init] network up: eth0 10.0.2.2/24 via 10.0.2.1"' \
    'echo "[init] hermes version:"' \
    '/usr/local/bin/hermes --version || echo "[init] hermes --version FAILED"' \
    '/usr/sbin/sshd && echo "[init] sshd started; ssh root@10.0.2.2 from host"' \
    'exec /bin/bash -l' \
    > /etc/init.sh \
    && chmod 0755 /etc/init.sh \
    && ln -sf /etc/init.sh /usr/sbin/init

RUN rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
