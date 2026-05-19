#!/bin/bash
# Throwaway watchdog script for foundation-07 acceptance.
# Emits a recognizable marker + timestamp so the apprentice can confirm
# the Hermes cron tick delivered our stdout to Telegram verbatim.
set -euo pipefail

ts="$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
host="$(hostname || echo unknown)"

echo "HERMES-APPRENTICE-FOUNDATION-07-MARKER"
echo "ping at ${ts}"
echo "host: ${host}"
