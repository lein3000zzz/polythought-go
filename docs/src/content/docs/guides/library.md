---
title: The library
description: Ready-made scenarios to start from, and workflows members share.
---

The **Library** holds workflows to start from: scenarios that come with Polythought, and
workflows other members of your instance shared.

## Using a template

Open one to read what it does and see its nodes; select a node to see how it is set. Press
**Use this** to make a copy of your own. Some templates ask you to fill in what only you can
know, such as a wallet or a webhook URL; optional ones, like a credential, can be left empty.
The copy starts switched off, so you can change anything before you turn it on.

A shared workflow may bring [custom nodes](/docs/guides/custom-nodes/) its author made. Its
page lists them, code included: that code runs for you, so read it first. Using it copies
them into your **Nodes**. One of yours with the same name and the same definition is used
as it is; one that differs stays as it is, and the copy takes the next free name, like
`spread-split-2`, which the new workflow uses. Making custom nodes takes code access.

The scenarios that come with Polythought:

- **Your first workflow**: search live markets and watch items pass from node to node.
- **Markets with tight spreads**: read live order books and find outcomes where buying and selling cost almost the same.
- **Big price moves**: spot popular markets whose odds moved 5% or more in the last hour.
- **Large trades**: list trades of $1,000 or more in the markets you follow.
- **Alerts in Discord or Slack**: get a chat message when an outcome becomes 80% likely.
- **Your positions** and **Low balance warning**: read your own wallet's positions and balance.
- **Copy traders** and **Copy traders with approval**: follow Polymarket traders and copy
  their trades on paper; the second asks you on Telegram before each copy.

## Sharing your own

In the editor, choose **⋮ → Share to library**, with a one-line summary and, if you like, a
longer description. Everyone on the instance can then find it and use a copy.

Personal settings stay with you. Sharing leaves out your wallets, credentials and values
like webhook URLs, which often contain a secret token. Whoever uses the copy is asked for
their own.

The custom nodes the workflow uses go with it, as they are when you share: their settings,
outputs and code, which everyone can read.

The library keeps a copy as it was when you shared it: later changes to your workflow, or
to its custom nodes, do not reach it until you share again, which replaces the copy. You can share up to 20
workflows, and remove one from the library on its page.
