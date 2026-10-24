---
name: custom-nodes
description: Make and change the user's own Polythought node types (custom nodes, a form and TypeScript), write code nodes, and keep the credentials their nodes use. Use when a workflow needs a node Polythought lacks, code, or an API key.
---

# Custom nodes, code and credentials

## Code

Code nodes (`code.transform`, `code.filter`, `code.route`, `code.aggregate`) and custom
nodes run TypeScript written against the declarations `get_code_sdk` gives: what `ctx`
offers, read-only data included. Code runs only for a user who may write code; an admin
decides who. Code runs where the user's own would, within the same limits.

## Custom nodes

A custom node is a node type of the user's own, `custom.<name>`, for their workflows only:
a form of settings, its outputs, the items it sends, and its TypeScript.

- `list_custom_nodes`, `get_custom_node`: the user's, and how one is made.
- `create_custom_node`: a new one. Use `get_code_sdk` first.
- `update_custom_node`: the whole new form and code, on the `version` you read. Its name
  stays. A change that would break workflows using it is refused, saying which and how;
  `confirm` makes it anyway, as the user's Save anyway does. Ask the user first.
- `delete_custom_node`: workflows that use it are named, and it stays unless `confirm`. It
  cannot be undone, and works only where the user lets agents delete.
- Changing or deleting one that a workflow trading live and switched on uses may wait for
  the user, as changing that workflow would: its next run runs the new code.

## Credentials

Credentials keep secrets, such as an API key or a header, for nodes to use. Their secrets are
sealed and never given back, to you or anyone.

- `list_credential_types`: the kinds there are, with their fields. `get_node_type` says which
  kinds a node's credential setting takes.
- `list_credentials`: the user's, by name and kind, to pick in a node by ID.
- `create_credential`: keep one the user gave you. Ask the user for the secret; never make
  one up, and never write it anywhere else.
- `update_credential`: rename one, give it a new secret, or change its other fields.
  Changing where it goes, such as a base URL, sends its secret somewhere new.
- `test_credential`: try it against its service, as Settings' Test does.
- `delete_credential`: its secret is gone, and workflows using it fail. It cannot be undone.

A workflow can send a credential wherever its nodes call. So saving, switching on or running
one that uses a credential, testing one, or changing where one goes works only where the
user lets agents use their credentials.
