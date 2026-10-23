---
title: Telegram, mail and notifications
description: How the instance's Telegram bot and mail server reach users, and how notifications choose between them.
---

Polythought reaches people two ways: the instance's Telegram bot, and its mail server. Both
are set up by an admin on the Config page and change without a restart.

## The bot

The bot polls Telegram for updates, or, with an https public address, takes them at a
webhook, whose secret is new each start and checked in constant time. A change to its
settings restarts the bot alone; a bot that cannot start retries, from five seconds up to five
minutes, unless Telegram refused its token.

A user links a chat from Settings with a one-time link: a code of 192 random bits, kept as its
hash, working once for ten minutes. Moving a chat already linked to another account asks
first. The bot serves private chats only, and a chat that blocks it is unlinked. Every button
it shows names what it acts on, and each press is checked against the account pressing it.

Sends are held to Telegram's limits: one a second per chat, with a small burst, and 30 a second
in all, a refusal for going too fast waited out and retried. The bot's token never reaches the
log.

Workflows use the bot three ways: Send message, at most 20 a run; Ask, a question with up to
ten buttons, one output port each, which parks the run until an answer or its timeout and sends
a reminder when a quarter of the time is left; and the Telegram trigger, which starts a run for
a message to the bot.

## Mail

Mail goes through the SMTP server an admin sets up, its settings read for each message, so a
change applies to the next. STARTTLS is required unless an admin chooses TLS from the start,
or no encryption for a server on the same host; certificates are always checked, and a
password is never sent in clear except to the same machine. Each mail has a text and an HTML
part, every value in it escaped, and says it was sent by a machine.

What is mailed: a test mail, a link to verify an address, a password reset and a notice after
the password changed, invites, a request's confirmation link and, if an admin wants, its
denial, and notifications.

## Notifications

Each kind of notification has a switch for Telegram and one for mail, per user: a workflow
failing, a question waiting, a session key about to expire, live and paper orders filling,
refused, unfilled, resting or ended, agents' spend nearing or at its cap, a workflow paused;
and for admins, a plugin that keeps crashing and someone asking to join. Mail needs an admin
to allow notifications by mail.

Notifications never hold up a run: they are queued, and a full queue drops one rather than
wait. A workflow's failures are told at most once an hour, and never for a run started by
hand; order notifications at most 20 of a kind an hour, the rest summed up when the hour ends.
