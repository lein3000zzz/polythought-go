---
title: Key decisions
description: The choices that shape Polythought, what else was weighed, and why.
---

Polythought was built from a plan, decided as it went. These are the choices that shape it
most. Where a choice was made for a reason, the reason is given; where it was a starting
point, what it gives.

## Running user code

**User code runs in WebAssembly.** Code nodes, custom nodes, expressions and workflow code
run in QuickJS, compiled to WebAssembly and run by wazero, a fresh instance each run. JavaScript
engines in Go had no cap on their memory; V8 needed cgo and its Go binding's builds broke for a
year; QuickJS translated to Go put an engine bug inside the server's memory; Node and its
sandboxes do not stand up to hostile code. WebAssembly gives a hard memory boundary, a page cap
and a deadline that stops it mid-instruction, in pure Go. The engine's wrapper is Polythought's
own, a few hundred lines of C with a single way back to the server, as the existing Go bindings
had open safety bugs or reached Go through I/O only.
[More](/docs/architecture/user-code/).

**TypeScript is compiled by esbuild, inside the server.** It takes about a millisecond, so no
build service is needed, and the server refuses only what does not compile. Type errors are the
editor's to show, in the browser, not a reason to refuse a save.

**Code reaches only the SDK.** No npm, no network: data comes from SDK calls the server answers,
read-only and for the code's owner. What code can do is then what the SDK offers, which keeps
it reviewable.

**On a public instance, code runs in processes of its own.** A pool of runner processes, each the
server's own binary in a Linux sandbox, each kept to one owner, one run at a time. A runner that
crashes fails its own run. If runners cannot start, code fails: it never falls back into the
server.

**Admins decide who writes code.** Everyone, chosen users, or no one: a shared instance may
not want strangers' code at all.

## Workflows

**The graph is the workflow.** A workflow is a JSON graph of nodes and connections; the canvas
edits it, and the TypeScript of the Code view is a second way to write the same graph, not a
second kind of workflow.

**Runs that wait are kept in the database.** A run waiting for an answer is parked as a
snapshot in Postgres, not held in memory, so it survives restarts and costs nothing while it
waits; taking a wait deletes it, so it resumes once.

**Nodes say what they send.** Each node type describes its items' fields, which drives the
editor's field pickers, the checks before saving, and the types code is written against.

## Trading

**Paper first, live behind several switches.** New workflows trade on paper, against the live
book, paying the fees a live order would, so their results mean something. Going live needs the
instance's Live trading switch, a wallet that can sign, the owner's confirmation, and then, at
every order, the caps and the owner's kill switch. Each live order is recorded before it is
sent. [More](/docs/architecture/trading/).

**Orders are signed in Go, and checked against Polymarket's own client.** No Go client signed
Polymarket's current orders. Rather than trust a port, its signatures, amounts and request
bodies are checked byte for byte against vectors generated from the official TypeScript client,
pinned to a commit. Amounts are exact; a price off the tick is refused, never rounded. Tests
never trade.

**Caps hold when runs race.** A user's day cap counts what is being sent as well as what was,
under one lock per user, so two runs at once cannot both pass it.

## Agents

**Users bring their own model keys.** The instance holds none, and nothing calls a provider but
a user's own workflow. Subscription logins (`claude`, `codex`) are not used even when an
instance is self-hosted: their terms forbid serving other people from one subscription.
Dollars are what providers report; there is no price table to go stale.

**Agents only read.** The agent node's tools search markets and read books and trades; an
answer reaches an order only through the workflow's own trading nodes.

**Agents from outside sign in with OAuth, as MCP has it.** Claude Code, Codex and the like
expect an OAuth 2.1 server, so Polythought is one, rather than asking users to paste API
tokens into them. A user allows each agent scopes on a consent page, live trading unticked to
start with, and only at a browser. Tokens are random and kept as hashes, not signed JWTs: each
use is looked up, so revoking one stops it at once. Agents that describe themselves in a
metadata document at an https address are shown as verified; those that register themselves
still work, marked as unverified.

## Plugins

**Plugins are processes, not libraries.** A Go plugin loaded into the server could do anything
the server can, and could crash it. Plugins run as their own processes, over gRPC with
go-plugin, their only way out the host API, which checks each call against what an admin
approved. [More](/docs/architecture/plugins/).

**Permissions are named and exact.** An admin approves the permissions a version asks for by
name; an unknown permission is refused, so a typo is never a permission quietly missing. Orders
from a plugin go through the same trading service, caps and kill switch as a node's.

**Signatures are optional, and binding once given.** Archives may be signed with minisign; an
unsigned one installs after a warning. Once a plugin is signed by a trusted key, its later
versions must be signed by the same key, and its files are held to the digest approved.

**The sandbox is the kernel's, in the server's own container.** Landlock and seccomp, no
sidecar container, no privileges to grant: where the kernel cannot sandbox, plugins do not
start unless an admin says they may.

## The platform

**Go for the server, TypeScript for users.** The server is Go; what users write is TypeScript.
Go lets the server embed its compiler and its WebAssembly runtime without cgo, so it ships as
one static binary, which is also its own code runner and sandbox launcher.

**Hexagonal layout.** The core, its domain, the engine, the nodes and the services, declares what
it needs as ports; adapters, the HTTP API, Postgres, Polymarket, Telegram, the sandbox, fill
them. The core is tested with stand-ins for the outside world, and an adapter can change without
the core noticing.

**One Postgres, one server.** Everything lasting is in Postgres: workflows, waits, what nodes
remember, settings, plugins' approvals. What only needs to last a moment, rate limits, the
holds on a cap, is in the server's memory, which suits one server; several would need it shared.

**Settings live, bootstrap apart.** What the server needs before its database, its address and
keys, comes from the environment or a file; everything else is a setting admins change on the
Config page, applied at once or after a restart the page offers. The environment overrides a
saved value, so an operator can pin one.

**Secrets sealed with envelope encryption.** Each secret has its own key, wrapped by a master key
from the environment, and is bound to its row; keys rotate by adding a new one first.
[More](/docs/architecture/data/#sealed).

**One origin.** The app, the docs and the API share one, through Caddy: no CORS, and a cookie and
origin check with one origin to know. [More](/docs/architecture/web/#one-origin).

**The API is code first.** The Go handlers are the source; the OpenAPI spec and the app's types
are generated from them, and a test keeps the spec and the routes in step.

## Accounts

**Invite only, to start with.** Registration is by invite unless an admin opens it. Invites
may admit many sign-ups, with codes admins can type and copy again, sealed at rest. People
without one may ask, if an admin allows: they confirm their address first, so nobody is mailed
an invite they did not ask for, and the form answers the same whatever comes of it.

**Telegram is a second way in.** The instance's bot carries notifications, questions a run waits
on, and a menu, with buttons rather than commands, so much of Polythought works from a phone.

## How it was built

**Docs in the repository, the reference generated.** The reference pages come from the code and
a test fails when they are stale, so the docs describe the version that runs.

**Security reviews by many reviewers, each finding checked.** Each phase that touched money,
keys or isolation ended in a review: reviewers in parallel, each finding then given to another
whose task was to disprove it, the report first and the fixes after.
