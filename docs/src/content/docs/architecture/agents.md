---
title: Agents
description: How the AI agent node calls language models with each user's own key, what it may look at, and how its spend is held to caps.
---

The **AI agent** node asks a language model about items and sends the answers on. It calls
Anthropic, Gemini, or any OpenAI-compatible API (OpenRouter, a local server), with a key its
owner keeps in their credentials. The instance holds no key of its own, and nothing calls a
provider but a user's own workflow.

```mermaid
sequenceDiagram
  participant N as AI agent node
  participant M as Spend meter
  participant P as Provider (the user's key)
  participant T as Read-only tools
  N->>M: room under the caps?
  M-->>N: yes, held
  N->>P: prompt, item, answer shape, tools
  P-->>N: asks for a tool
  N->>T: search markets, order book, trades
  T-->>N: results, bounded
  N->>P: the results
  P-->>N: answer, usage
  N->>M: record what it used
```

## The node

It calls the model once per item, four at a time, or once for all items together. Its answer
is text, fields from a table the owner fills in, or JSON matching a schema, checked, with up to
three retries; a refusal fails the item at once. Each answer carries the model, why it
stopped, its usage and its cost where the provider reports one.

The model may be given five tools, none on to start with, all read-only: search markets, an
order book, price history, recent trades, a public address's positions. Each call and each
result is bounded, ten calls by default and twenty results each; at the limit, the model is
told to answer with what it has. There is no tool that trades: an agent's answer reaches an
order only through the workflow's own trading nodes and their checks.

## Keys

Each user brings their own key, kept sealed in their credentials, and picks it in the node.
Agents use API keys only, never a subscription's login such as `claude` or `codex`, even on a
self-hosted instance: a subscription's terms forbid serving other people from it, and a key
keeps each user's calls on their own account.

An OpenAI-compatible key may name its own address. The server reaches it only at a public
address over https, through the guard webhooks use; a model on the same machine or network,
over plain http, needs an admin to allow private addresses.

## Spend

Each call is recorded against its owner's month as soon as it returns, even when its run was
cancelled. An admin caps a month's spend for the instance and per user, in tokens and in
dollars ($20 and 20 million tokens unless changed), and the node may cap its own run.
Dollars are only what a provider reports, as OpenRouter does; there is no price table to go
stale.

Before each call the meter checks the user's kill switch (if they chose that it stops their
agents too), the run's caps, then the month's: what is recorded, plus what calls in flight
hold, plus this call's estimate. Holding what calls in flight may spend keeps a cap from being
passed by much when many run at once. Users are told at 80% of a cap and at the cap, once a
month each.

## Agents over MCP

A coming part of Polythought lets agents such as Claude Code and Codex work with it as their
user does, over an MCP server in the app; and lets a workflow hand a task to its owner's own
agent. The sign-in is built: an OAuth 2.1 server in the app, where the user allows an agent
scopes on a consent page and revokes it on Settings, as
[the security model](/docs/architecture/security/#accounts) describes.

So is the endpoint, `/mcp`: the official Go SDK's MCP server, over Streamable HTTP, inside the
API server. An agent's OAuth token works there, and so does an API token, each with its
scopes; a request without one is told where to sign in. Each tool asks for a scope, and each
agent or token may call 120 a minute unless an admin changes it; an answer is kept under
about 8,000 tokens, and a list comes a page at a time.

The tools so far read what the user builds: their workflows, as JSON or as the TypeScript the
Code view shows (the server prints it as the app does, held to it by shared tests), their
problems, their executions and the first items a run by hand kept, the node types with their
settings, the code SDK's declarations, and the library. Text others wrote, a plugin's words,
another user's library entry, what flowed through a workflow from the markets, comes in fields
named `untrusted`, which the server tells agents are data, never instructions. A workflow's
personal settings, such as a webhook's URL, show only as set unless the user lets agents see
them.

Agents read the markets with the AI agent node's own five tools, the same code answering
both, and one market's details beside them; and the user's money: their wallets and the pUSD
in each, how each workflow trades and has done, its orders, and the live order log. Tools that
trade and build are coming; this page will describe them as they do.
