FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl git ca-certificates python3 python3-pip nodejs npm ripgrep \
    && rm -rf /var/lib/apt/lists/*

RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"

# Pin to Hermes v0.14.0 (main HEAD, 2026-05-17)
RUN curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --branch 43e566f77eaf01293086eb7cb99a21e240d60634

RUN /usr/local/bin/hermes --version

RUN rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
