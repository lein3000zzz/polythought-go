package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SDK's types follow the API: regenerate them with make sdk-python-types.
func TestTypesAreCurrent(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", specPath))
	require.NoError(t, err)

	want, err := Generate(spec)
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join("..", "..", outPath))
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got), "sdk/python's types are stale: run make sdk-python-types")
}

func TestGenerate(t *testing.T) {
	out, err := Generate([]byte(`{"components": {"schemas": {
		"Thing": {"description": "A thing.", "type": "object", "required": ["name"], "properties": {
			"name": {"type": "string", "description": "Its name (D12, A3), as given (D398)."},
			"kind": {"type": "string", "enum": ["a", "b"]},
			"count": {"type": "integer"},
			"tags": {"type": "array", "items": {"type": "string"}},
			"by": {"type": "object", "additionalProperties": {"type": "number"}},
			"other": {"$ref": "#/components/schemas/pkg.other"}
		}},
		"pkg.other": {"type": "object", "properties": {"from": {"type": "boolean"}}}
	}}}`))
	require.NoError(t, err)

	s := string(out)
	assert.Contains(t, s, "class Thing(TypedDict):\n    \"\"\"A thing.\"\"\"\n")
	assert.Contains(t, s, "    # Its name, as given.\n    name: str\n")
	assert.Contains(t, s, `    kind: NotRequired[Literal["a", "b"]]`)
	assert.Contains(t, s, "    count: NotRequired[int]\n")
	assert.Contains(t, s, "    tags: NotRequired[list[str]]\n")
	assert.Contains(t, s, "    by: NotRequired[dict[str, float]]\n")
	assert.Contains(t, s, "    other: NotRequired[Other]\n")
	assert.True(t, strings.Contains(s, `Other = TypedDict(`) && strings.Contains(s, `"from": "NotRequired[bool]",`),
		"a keyword field: the functional form")
}
