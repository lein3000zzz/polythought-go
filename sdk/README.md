# SDKs

Polythought's SDKs, beside each other (D442):

- `typescript/`: `@polythought/sdk`, which code nodes import, and `@polythought/workflow`,
  which builds workflows as code. The server embeds them through the Go package here,
  bundles the SDK into the runtime code runs in, and serves its declarations to editors.
  `make sdk-types` regenerates `typescript/types/sdk.d.ts` after a change to `sdk.ts`.
- `python/`: for notebooks, to come (D445).
