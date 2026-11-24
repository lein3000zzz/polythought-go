// Package sdk holds Polythought's SDKs (D442), and embeds what the server serves of them:
// sdk/typescript, @polythought/sdk, which code nodes import and the server bundles into
// the runtime they run in, with @polythought/workflow, the builder of workflows as code;
// and sdk/python, for notebooks, whose wheel an image's build puts in python/dist.
package sdk

import (
	"embed"
	"io/fs"
	"strings"
)

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

// pythonDist is what the image's build puts in python/dist: the Python SDK's wheel, built
// for the server's version. A build from the code has none.
//
//go:embed all:python/dist
var pythonDist embed.FS

// PythonWheel is the Python SDK's wheel, its file name and its bytes: false where the
// server was built without one.
func PythonWheel() (name string, data []byte, ok bool) {
	entries, err := fs.ReadDir(pythonDist, "python/dist")
	if err != nil {
		return "", nil, false
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "polythought-") && strings.HasSuffix(e.Name(), ".whl") {
			name = e.Name() // the last, should there be more than one
		}
	}

	if name == "" {
		return "", nil, false
	}

	data, err = pythonDist.ReadFile("python/dist/" + name)

	return name, data, err == nil
}
