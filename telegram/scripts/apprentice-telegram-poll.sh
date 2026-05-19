#!/bin/bash
# Hermes cron --no-agent --script: one Telegram getUpdates poll cycle.
#
# Install once inside the microVM (alongside the dispatch script):
#   scp telegram/scripts/apprentice-telegram-poll.sh \
#       root@10.0.2.2:/root/.hermes/scripts/apprentice-telegram-poll.sh
#   ssh root@10.0.2.2 'chmod +x /root/.hermes/scripts/apprentice-telegram-poll.sh'
#
# Register:
#   ssh root@10.0.2.2 'hermes cron create \
#       --name apprentice-poll-replies \
#       --no-agent \
#       --script apprentice-telegram-poll.sh \
#       --deliver telegram "every 1m"'
#
# Behaviour:
#   - Calls ``apprentice-telegram poll-replies`` once.
#   - Writes one JSON marker per recognised reply under
#     ~/.apprentice/decisions/.
#   - Emits NO stdout (decision markers are not user-facing); Hermes treats
#     the silent run as "nothing to send this tick".
#
# Env: requires TELEGRAM_BOT_TOKEN. Hermes auto-loads ~/.hermes/.env, so put
# the token there.
set -euo pipefail

: "${APPRENTICE_TELEGRAM_BIN:=apprentice-telegram}"
: "${APPRENTICE_OUTBOX_ROOT:=$HOME/.apprentice/outbox}"
: "${APPRENTICE_DECISIONS_ROOT:=$HOME/.apprentice/decisions}"
: "${APPRENTICE_OFFSET_PATH:=$HOME/.apprentice/telegram/updates_offset}"

# Suppress stdout — the marker JSON is for the orchestrator, not Telegram.
"${APPRENTICE_TELEGRAM_BIN}" poll-replies \
    --outbox-root "${APPRENTICE_OUTBOX_ROOT}" \
    --decisions-root "${APPRENTICE_DECISIONS_ROOT}" \
    --offset-path "${APPRENTICE_OFFSET_PATH}" \
    > /dev/null
