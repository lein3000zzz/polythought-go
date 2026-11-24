# SDKs

Polythought's SDKs, beside each other:

- `typescript/`: `@polythought/sdk`, which code nodes import, and `@polythought/workflow`,
  which builds workflows as code. The server embeds them through the Go package here,
  bundles the SDK into the runtime code runs in, and serves its declarations to editors.
  `make sdk-types` regenerates `typescript/types/sdk.d.ts` after a change to `sdk.ts`.
- `python/`: `polythought`, for notebooks: Polythought's data and market data as tables,
  datasets, and serving models. An image's build makes its wheel, for the version the
  server is, and the server serves it at `/api/v1/sdk/python/`. `make sdk-python-types`
  regenerates its types from the API's spec. `make sdk-python-check` runs its checks, and
  `make sdk-python-wheel` builds a wheel for a server built by hand to serve.
