---
title: Credentials
description: Keys and tokens your workflows use, kept encrypted.
---

Some services want a key or a token with each request. Rather than typing it into a
workflow, keep it as a **credential** on the **Credentials** page and pick it in the node
that needs it.

- Secret values are stored encrypted and never shown again, not even to you. To change one,
  type a new value; leave it empty to keep the saved one.
- Only your own workflows can use your credentials. Sharing a workflow leaves them out, and
  whoever uses the shared copy picks their own.
- Deleting a credential stops the workflows that use it until you pick another.

## Kinds

Each credential has a kind, which says what it holds. Nodes ask for one kind. The kinds
there are, with their fields, are listed in [Credential types](/docs/reference/credential-types/).

### HTTP header

One header sent with each request, for services that want an `Authorization` header or an
API key. The [Webhook](/docs/reference/nodes/output/webhook/) node uses it:

1. On **Credentials**, press **New credential**, pick the kind **HTTP header**, and give it
   a name. Enter the header's name, such as `Authorization`, and its value, such as
   `Bearer` followed by your token.
2. In the editor, select the Webhook node and choose the credential under **Credential**.

The header goes only to the address in the node. If that address redirects elsewhere, to
another host, the header is not sent there.

### Language model keys

Keys for language models: **Anthropic API key** for Claude, **OpenAI-compatible API key**
for OpenAI or any API that works like it (OpenRouter, or a model served on your own
network), and **Google Gemini API key**. The [AI agent](/docs/guides/agents/) node uses
them. Each is your own key: calls made with it are billed to your account with the
provider, and nothing calls a provider unless one of your workflows does.

1. Get a key from the provider: the Claude Console, OpenAI's or OpenRouter's dashboard, or
   Google AI Studio.
2. On **Credentials**, press **New credential**, pick the kind, give it a name and paste the
   key.
3. Press **Test**. Polythought asks the provider for the models the key can use, which costs
   nothing; if the key is wrong, the provider's own answer is shown, such as
   `Anthropic answered 401: invalid x-api-key`.

For OpenAI's own API, leave **Base URL** empty. For OpenRouter, set it to
`https://openrouter.ai/api/v1`; OpenRouter also reports what each call cost.

A model served on your own network, such as Ollama's at `http://localhost:11434/v1`, needs
the administrator's **Private addresses** setting. Without it, the base URL must be a
public `https` address. With it, plain `http` still reaches only addresses inside the
network, so a key never crosses the internet in clear.
