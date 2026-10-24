# Polythought for Claude Code

This plugin connects Claude Code to your Polythought instance over MCP and teaches it
Polythought's tools: building and running workflows, researching Polymarket, trading on
paper and live, working the tasks of your workflows' Local agent nodes, and an admin's work.

Polythought is self-hosted, so the plugin asks for your instance's address. Nothing runs
until you sign in, and Claude does only what you allow it on Polythought's consent page.

## Install

In Claude Code:

```
/plugin marketplace add lein3000zzz/polythought-go
/plugin install polythought@polythought
```

As you enable it, Claude Code asks for **Polythought's MCP address**: the one on your
instance's **Settings → Connected agents**, like `https://polythought.example.com/mcp`. To
change it later, open `/config`, or the plugin under `/plugin` and **Configure options**.

## Sign in

Run `/mcp`, pick the plugin's `polythought` server and authenticate. Your browser opens
Polythought, which asks what Claude may do: tick the scopes you want, such as **Take tasks**
for the `work-tasks` skill. Revoke it any time on Connected agents.

If signing in through the plugin's server does not work, add the server yourself and keep
the plugin for its skills; disable the plugin's own server under `/mcp`:

```
claude mcp add --transport http --scope user polythought https://polythought.example.com/mcp
```

## Skills

Claude loads these when your request fits; `/polythought:<skill>` runs one yourself.

| Skill | What for |
|---|---|
| `polythought` | Start here: scopes, untrusted text, what waits for you, what you let agents do. |
| `workflows` | Reading, building, changing, running and switching on workflows; executions; the library. |
| `custom-nodes` | Your own node types, code, and credentials. |
| `markets` | Researching Polymarket: markets, books, prices, trades, positions. |
| `trading` | Paper and live trading, wallets, orders, the kill switch. |
| `work-tasks` | Working your Local agent nodes' tasks until you stop it. Only you start it: `/polythought:work-tasks`. |
| `admin` | Users, invites, requests to join, Config and plugins, for admins. |

## Permissions

Tasks and market data hold text others wrote, and Claude Code can run commands on your
machine. Read [Agents over MCP](https://github.com/lein3000zzz/polythought-go/blob/main/docs/src/content/docs/guides/agents-over-mcp.md)
on the permission setups for an agent left to work on its own. Rules for this plugin's tools
name them `mcp__plugin_polythought_polythought__<tool>`; for a server you added yourself as
`polythought`, `mcp__polythought__<tool>`.
