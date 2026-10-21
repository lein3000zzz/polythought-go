---
title: Telegram
description: Link Telegram to get messages from Polythought there, and set up the bot.
---

Polythought can write to you on Telegram, through the bot of your instance: notifications,
and messages from your workflows. From its menu you can run your workflows, switch them on
and off, see how the traders you copy do, and check your positions, and your workflows can
start on messages you send it.
Link your Telegram account once to use any of it.

## Linking

1. In Polythought, open **Settings** and, under **Telegram**, press **Link Telegram**.
2. Press **Open Telegram**. Telegram opens a chat with the bot; press **Start**.
3. The bot says which account the chat is linked to, and Settings shows it too.

The link works once, for ten minutes, and links whichever Telegram account opens it, so
keep it to yourself. A Telegram account links one Polythought account at a time: opening a
link of another account's in a chat linked already, the bot names both and asks first,
and only **Link to** the other moves it there.

If **Link Telegram** is missing, the instance has no bot yet; see below.

## The menu

Once linked, a keyboard with five sections sits under the chat's input field. Send `/menu`
if it is gone. Each section answers with a message whose buttons change it in place:

- **📋 Workflows**: your workflows, ● on and ○ off, a page at a time. Pick one to see its last
  runs, run it now, or switch it on or off. Switching off a workflow that
  [trades live](/docs/guides/copy-traders/#trading-live) leaves its resting orders on the
  book, and the bot says how many: cancel them in its Trading view.
- **👥 Traders**: the workflows that [copy traders](/docs/guides/copy-traders/), with how
  many of their traders are on. Pick one to see what its paper account is worth and, for
  each trader, the profit or loss made on what was sold, what is held would make sold now,
  and how many closed copies made money. Pick a trader to see more and **Switch** them
  **on** or **off**, which saves the workflow as a new version, like saving it in the editor.
- **📊 Status**: which workflows are on, how each last run went, and how many runs failed in
  the last 24 hours. With a wallet that can trade, **🛑 Stop live trading** is your
  [kill switch](/docs/guides/copy-traders/#stopping-it): at once, without asking; **▶️
  Resume live trading** asks first.
- **💼 Positions**: pick one of your [wallets](/docs/guides/concepts/#wallets) to see its
  biggest positions, with what they are worth and what they made or lost.
- **🔔 Notifications**: the same switches as in Settings, and **Unlink this chat**.

Times in the menu are relative, like "5 min ago", as the bot does not know your time zone.

## Notifications

Under **Settings → Notifications**, choose what Polythought tells you about:

- **A workflow fails**: a run of one of your workflows failed, with the reason. At most once
  an hour for each workflow; the next message says how many failures came in between. Runs
  you start by hand never notify: you see those yourself.
- **A question waits for your answer**: a [question](#questions) is still unanswered with a
  quarter of its time left. The bot replies to it once, saying how long it stays open.
  Only for questions open 10 minutes or more.
- **A session key is about to expire**: a wallet's [session key](/docs/guides/concepts/#wallets)
  expires in 14 days or less, or has expired. Once per key, with the day it expires.
- **A live trade fails**: a [live order](/docs/guides/copy-traders/#trading-live) matched on
  Polymarket, but its trade failed on chain, so the workflow's books undo it. Once per
  order.
- **Live orders** and **Paper orders**, the same five kinds each, about your workflows'
  orders, with what the order was, its market and the workflow:
  - **fills**: it filled, wholly or in part, as it matched, or later as it rested on the
    book;
  - **is refused**: before it was sent (your kill switch, an admin's cap, too few shares or
    too little cash) or by Polymarket, with why;
  - **goes unfilled**: a FAK or FOK the book could not fill within its price;
  - **rests on the book**: a GTC or GTD came to rest; its fills tell you later;
  - **is cancelled or expires**: what was left of a resting order went.

  Live fills and refusals are on to start with, the rest off, and every paper kind off. At
  most 20 messages of a kind an hour; past that, they are held, and one message at the
  end of the hour says how many more there were, for which workflows.
- **AI agents**, two kinds about what your [agent nodes](/docs/guides/agents/#caps) spend:
  **reaches 80% of a monthly cap**, for the first cap they pass 80% of, and **reaches a
  monthly cap**, when a call is refused for one. Each once a month, and again only if an
  admin raises the cap.

Each has two switches: **Telegram**, to your linked chat (without one, nothing is sent), on
by default unless said above; and **Email**, to your account's address, off until you turn
it on. Email works
once an admin allows notifications by email and mail is set up; until then its switches are
greyed out. A failing workflow's once an hour holds for both together, and a question's
reminder by email points you to Telegram, where you answer it.

## In workflows

Three nodes use your chat with the bot:

- [Telegram message](/docs/reference/nodes/telegram/send/) sends items to you: one message
  listing them all, or one per item. `{{field}}` puts in an item's field. With the **html**
  format, Telegram's tags work, such as `<b>` and `<a href="…">`; item fields are escaped,
  so they cannot change the formatting.
- [Telegram message trigger](/docs/reference/nodes/triggers/telegram/) starts a workflow when
  you send the bot a message. **Starts with** limits it to messages like `/report`; the item
  has the whole `text` and the `rest` after that start. A message that no workflow takes gets
  the bot's usual answer.
- [Telegram question](/docs/reference/nodes/telegram/ask/) asks you something with buttons
  and waits for your answer; see [Questions](#questions).

They reach only your own chat: a shared workflow that uses them writes to, or asks, whoever
uses it. The bot keeps `/start`, `/menu` and the section names for itself; those never start
a workflow.

### Questions

A Telegram question sends you one message, with a button for each answer under it, and the
execution [waits](/docs/guides/concepts/#waiting). Press a button and the items go on
through the port of that button; the line from **Approve** carries on when you press
Approve. If you don't answer within the **Timeout**, one hour unless you change it, they go
through `timeout` instead.

- **Buttons** lists the answers, one per line, up to 10. Each becomes an output port with the
  same name, so the node shows where every answer leads.
- **Question** is the message. `{{field}}` puts in an item's field; with several items,
  each gets a line, and your one answer goes for them all.
- Once answered, the message shows your answer in place of the buttons; when time runs out,
  it says so.
- Only you can answer, from your linked chat. A button of a question that was answered,
  ran out of time or was cancelled says so and goes away.
- A question waits at most as long as any wait may, 7 days unless an admin
  [changed it](/docs/reference/settings/#executions).

## Unlinking

Either side works:

- In Polythought, **Settings → Telegram → Unlink**. The bot tells the chat.
- In Telegram, open **🔔 Notifications** and press **Unlink this chat**. The menu goes away.

The bot then answers only with how to link again. Blocking the bot in Telegram unlinks the
chat too, the next time Polythought writes to it.

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
