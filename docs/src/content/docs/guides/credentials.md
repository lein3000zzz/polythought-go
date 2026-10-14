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

1. On **Credentials**, press **New credential**, keep the kind **HTTP header**, and give it a
   name. Enter the header's name, such as `Authorization`, and its value, such as
   `Bearer` followed by your token.
2. In the editor, select the Webhook node and choose the credential under **Credential**.

The header goes only to the address in the node. If that address redirects elsewhere, to
another host, the header is not sent there.
