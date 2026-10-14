---
title: Telegram
description: Link Telegram to get messages from Polythought there, and set up the bot.
---

Polythought can write to you on Telegram, through the bot of your instance. Link your
Telegram account once; messages from Polythought then arrive in your chat with the bot.

## Linking

1. In Polythought, open **Settings** and, under **Telegram**, press **Link Telegram**.
2. Press **Open Telegram**. Telegram opens a chat with the bot; press **Start**.
3. The bot says which account the chat is linked to, and Settings shows it too.

The link works once, for ten minutes, and links whichever Telegram account opens it, so
keep it to yourself. A Telegram account links one Polythought account at a time: linking it
to another moves it there.

If **Link Telegram** is missing, the instance has no bot yet; see below.

## Unlinking

Either side works:

- In Polythought, **Settings → Telegram → Unlink**. The bot tells the chat.
- In Telegram, send the bot `/start` and press **Unlink this chat** under its answer.

The bot then answers only with how to link again.

## Setting up the bot

The bot belongs to the instance; an admin sets it up once.

1. In Telegram, message [@BotFather](https://t.me/BotFather), send `/newbot`, and pick a
   name and a username for the bot. BotFather answers with its **token**.
2. In Polythought, open **Admin → Config**. Under **Telegram**, switch on **Telegram bot**,
   paste the token into **Bot token**, and save.

The bot starts within seconds; the server does not restart. Changing any of these settings
later restarts only the bot. If it cannot start, say because Telegram refused the token,
admins see why under **Settings → Telegram**.

### Polling or webhook

**How the bot gets messages** has two choices:

- **polling** (the default): the server asks Telegram for new messages. It works anywhere,
  behind a firewall or on a laptop.
- **webhook**: Telegram sends each message to the server as it comes, at
  `/api/v1/telegram/webhook` under the public URL. That URL must be `https`, on port 443, 80,
  88 or 8443, and reachable from the internet.

Run one server per bot: two servers polling with the same token get in each other's way,
and the bot reports it.

The three settings can also come from the environment (`POLYTHOUGHT_TELEGRAM_ENABLED`,
`POLYTHOUGHT_TELEGRAM_TOKEN`, `POLYTHOUGHT_TELEGRAM_MODE`), which locks them on the Config
page; see [Configuration](/docs/self-hosting/configuration/).
