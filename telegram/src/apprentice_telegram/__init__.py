"""Apprentice → Hermes Telegram glue.

This package owns three pieces:

1. Message templates (``templates.py``) — graduation, failure, weekly.
2. An outbox (``outbox.py``) — host- or guest-local file queue consumed by a
   Hermes ``no_agent`` cron job whose stdout becomes a Telegram message via
   ``hermes cron … --deliver telegram``.
3. A reply poller (``reply_handler.py``) — calls Telegram ``getUpdates`` and
   converts ``train`` / ``details`` / ``skip`` replies into decision markers
   under ``~/.apprentice/decisions/``.

Outgoing delivery does not call the Telegram API directly. Hermes is already
configured with ``TELEGRAM_HOME_CHANNEL`` and python-telegram-bot
(see ``notes/foundation-07/proof.md``); riding its delivery adapter means we
inherit retry/rate-limit handling for free.
"""

__all__ = ["templates", "outbox", "reply_handler"]
