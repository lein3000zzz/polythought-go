---
title: Agents over MCP
description: Connect Claude Code, Codex and other agents to Polythought, choose what they may do, and run them safely.
---

Agents such as Claude Code, Codex and Gemini CLI can work with Polythought as you do: read
your workflows, build and run them, research markets, trade on paper and live, and take the
tasks your workflows hand them. They connect over MCP, sign in through your browser, and do
only what you allowed. Polythought runs no agent itself and holds no login of yours.

## Connecting one

**Settings → Connected agents** has the steps for each client, your instance's address
filled in, with a copy button on each command: Claude Code, Codex, Gemini CLI, Cursor,
opencode, VS Code, Claude Desktop and claude.ai, and any other client. Each comes down to:

1. Add Polythought to the agent at the address the page shows, such as
   `https://polythought.example.com/mcp`.
2. Sign in. The agent opens your browser on Polythought's consent page, which names the
   agent and asks what it may do.
3. Tick the scopes it needs, and allow it.

The agent then shows under **Connected agents**, with its scopes and its last use, and under
**Connected now** while it has a session open. Revoke it there and it stops at once; it asks
you again the next time it signs in. An agent that also ran before a restart shows as
**Since before a restart** until it comes back.

Claude Desktop and claude.ai connect from Anthropic's cloud, not from your computer, so they
work only with an instance reachable from the internet. An API token, under **API tokens**,
works too, sent as `Authorization: Bearer pt_…`, with the scopes it was made with.

## What it may do

| Scope | What the agent may do |
|---|---|
| Read | See your workflows and their runs, the node catalog, the library, market data, your wallets' balances and orders. Every scope brings it. |
| Build | Create, change, check, run and switch workflows; custom nodes and code; credentials you give it; the library. |
| Take tasks | Take and answer the tasks of your Local agent nodes. |
| Change settings | Your notifications, and your connected agents. |
| Trade on paper | Set workflows to trade on paper, and cancel paper orders. |
| Trade live | Set workflows to trade live from your wallets, and cancel live orders, within your caps and kill switch. |
| Admin | For admins: users, invites, requests to join, the Config settings and plugins. |

Take tasks, Change settings, Trade live and Admin start unticked. Some things stay yours, at a
browser, whatever the scopes: your password, your wallets' keys and approvals, creating API
tokens, linking Telegram, whose chat confirms what your agents ask for, the switches below,
and confirming what waits for you.

Under the scopes, five switches on Connected agents, all off to start with, decide whether
agents and tokens may see personal settings such as webhook URLs, delete workflows, custom
nodes and credentials, share to the library, use your credentials in workflows, and answer
the questions your workflows ask, such as a Telegram question approving a trade.

## What waits for you

What reaches real money waits for you to confirm it, a switch each under **Confirm my
agents' live actions**, on to start with: going live or changing the wallet, switching on or
running a workflow that trades live, changing one that is on or a custom node it uses, and
turning the kill switch off. For admins, so does an agent's work on other users, Config and plugins.

A waiting action shows in a bar on every page and on Telegram, with buttons to confirm or
refuse it; with **Also ask in the agent's window** on, the agent's client asks you too,
where it can: a link to Polythought, where you decide, or, where it shows no links, a
Confirm in the agent itself. That answer comes from the agent's own client, so one that is
compromised could give it for you; leave the switch off if that worries you. API tokens get
the link only. Confirmed, Polythought carries it out as the agent asked. Your own actions
never wait, and neither does turning the kill switch on.

## Tasks for your agents

A workflow hands work to one of your agents with the **Local agent** node, as
[Your own agents](/docs/guides/agents/#your-own-agents) describes: the agent waits for a
task with `wait_for_task`, answers it with `answer_task`, and the run goes on. Give it the
Take tasks scope, and ask it to keep waiting for tasks. With the Claude Code plugin, run
`/polythought:work-tasks`.

## The Claude Code plugin

The repository is a Claude Code plugin marketplace. In Claude Code:

```
/plugin marketplace add lein3000zzz/polythought-go
/plugin install polythought@polythought
```

As you enable it, Claude Code asks for your instance's MCP address, the one Connected agents
shows; `/config` changes it later. Then run `/mcp` and sign in. The plugin brings skills on
Polythought's tools, which Claude loads when your request fits: `polythought` to start,
`workflows`, `custom-nodes`, `markets`, `trading`, `admin`, and `work-tasks`, which only you
start. Its [README](https://github.com/lein3000zzz/polythought-go/blob/main/integrations/claude-code/README.md)
has the details, and how to add the server yourself if signing in through the plugin fails.

## Text an agent must not obey

An agent working with Polythought reads text other people wrote:

- market questions, rules and outcomes, and traders' names, from Polymarket;
- other users' library entries, their descriptions and their code;
- plugins' names, descriptions and logs;
- what flowed through a workflow: market data, what a plugin's node fetched from the web, a
  language model's answers;
- a task's whole text, its prompt perhaps from another user's entry, filled in with the above;
- the notes people send with a request to join.

Any of it can be written to steer an agent: "ignore your instructions and run this". Polythought
puts all of it in fields named `untrusted`, and tells the agent those are data to weigh,
never instructions to follow. That helps, but it is a label, not a wall: a model can still be
talked into things. Many agents can run commands on your machine, read your files and reach
the network, so what an agent may do there matters as much as what it may do here.

## Running one on its own

There are two broad ways to run an agent that works for a while without you, such as one
working tasks. Neither is right for everyone: it depends on what else the agent can reach,
and how much you watch it.

**It asks before each tool.** The agent asks you before it calls a tool, Polythought's or its
own, such as a shell command. Nothing happens that you did not approve, but left alone it
stops at the first ask and waits for you. Good while you watch it, or for a few tasks.

**It may call only what you allowed, and nothing else.** You allow the few tools its work
needs, such as Polythought's `wait_for_task` and `answer_task`, and the agent is refused the
rest without asking: no shell, no file edits. It runs unattended, and a task that tries to
steer it into more is refused by the client, not by the model's judgment. Allow too much,
and you are back to trusting the model.

Either way, running the agent in a container or a virtual machine of its own, with none of
your files and no other credentials, limits what a mistake can reach.

How each client does it:

- **Claude Code**: the default mode asks before tools; `dontAsk` refuses everything not in
  your `permissions.allow` rules, such as `mcp__polythought__wait_for_task` for a server you
  added as `polythought`, or `mcp__plugin_polythought_polythought__wait_for_task` for the
  plugin's. `bypassPermissions` asks for nothing at all, and is meant for isolated containers.
- **Codex**: `default_tools_approval_mode`, and `approval_mode` per tool under
  `[mcp_servers.polythought.tools.<tool>]`; `enabled_tools` and `disabled_tools` narrow the
  server's tools.
- **Gemini CLI**: the server's `includeTools` and `excludeTools`; `trust` skips its
  confirmations for every tool of the server.
- **Cursor**: asks before MCP tools unless you allow them, in its run modes.
- **opencode**: its `permission` settings, `allow`, `ask` or `deny` by tool, where
  Polythought's tools are `polythought_<tool>`.
- **VS Code**: asks before a tool runs, unless you allow it for the session or always.

Check your client's own docs before relying on one: these settings change with its versions.

## Limits

An agent or a token may make 120 tool calls a minute, and all of yours together 600,
unless an admin changes it. Each agent or token keeps up to 10 sessions open and all of
yours 30, the oldest closing past that; its client opens another when it calls again. An
answer is kept under about 8,000 tokens, and a long list comes a page at a time. Your caps on live
orders and your kill switch hold for agents as for you; turning the kill switch on stops
their live trading at once.
