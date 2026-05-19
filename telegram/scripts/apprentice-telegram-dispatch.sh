#!/bin/bash
# Hermes cron --no-agent --script consumer.
#
# Install once inside the microVM:
#   ssh root@10.0.2.2 'mkdir -p /root/.hermes/scripts'
#   scp telegram/scripts/apprentice-telegram-dispatch.sh \
#       root@10.0.2.2:/root/.hermes/scripts/apprentice-telegram-dispatch.sh
#   ssh root@10.0.2.2 'chmod +x /root/.hermes/scripts/apprentice-telegram-dispatch.sh'
#
# Register the cron (5-minute cadence — Hermes ticks the dispatcher; an empty
# outbox is a silent no-op so this is cheap):
#   ssh root@10.0.2.2 'hermes cron create \
#       --name apprentice-telegram \
#       --no-agent \
#       --script apprentice-telegram-dispatch.sh \
#       --deliver telegram "every 5m"'
#
# On each tick the script invokes ``apprentice-telegram dispatch-one``. Empty
# outbox → empty stdout → Hermes' --deliver telegram treats it as a silent
# run (see notes/cron-tick-implementation.md). Otherwise the rendered body
# becomes the Telegram message.
set -euo pipefail

# Allow override via env so the same script works in CI / dev sandboxes.
: "${APPRENTICE_TELEGRAM_BIN:=apprentice-telegram}"
: "${APPRENTICE_OUTBOX_ROOT:=$HOME/.apprentice/outbox}"

exec "${APPRENTICE_TELEGRAM_BIN}" dispatch-one --outbox-root "${APPRENTICE_OUTBOX_ROOT}"
