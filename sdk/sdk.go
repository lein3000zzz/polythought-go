// Package sdk holds Polythought's SDKs (D442), and embeds what the server serves of them:
// sdk/typescript, @polythought/sdk, which code nodes import and the server bundles into
// the runtime they run in, with @polythought/workflow, the builder of workflows as code;
// and sdk/python (D445), for notebooks.
package sdk

import "embed"

// TypeScript is the TypeScript SDK's source: sdk.ts, with big.js vendored for decimals.
// typescript/types/sdk.d.ts is what tsc makes of it, for editors (make sdk-types).
//
//go:embed typescript/sdk.ts typescript/big.mjs
var TypeScript embed.FS

// TypeScriptEntry is the SDK's entry in TypeScript.
const TypeScriptEntry = "typescript/sdk.ts"

// Workflow is the builder of workflows as code, @polythought/workflow: the editor loads
// the same file.
//
//go:embed typescript/workflow.ts
var Workflow string

// Declarations are the SDK's, as tsc makes them (make sdk-types).
//
//go:embed typescript/types/sdk.d.ts
var Declarations string
