package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheck(t *testing.T) {
	noop := NodeFunc(func(context.Context, Run, Ports) (Ports, error) { return nil, nil })

	p := Plugin{
		Manifest: Manifest{
			Nodes:           []NodeType{{Name: "a"}},
			CredentialTypes: []CredentialType{{Name: "key", Testable: true}},
		},
		Nodes:       map[string]Node{"a": noop, "b": noop},
		Credentials: map[string]CredentialChecks{"key": {}},
	}

	err := p.check()
	require.Error(t, err)
	assert.ErrorContains(t, err, `node "b" is not in the manifest`)
	assert.ErrorContains(t, err, `credential type "key" is testable but has no test`)

	p.Nodes = map[string]Node{}
	assert.ErrorContains(t, p.check(), `node "a" has no implementation`)
}

func TestPerItem(t *testing.T) {
	node := PerItem(func(_ context.Context, _ Run, item Item) (string, map[string]any, error) {
		switch item.JSON["n"] {
		case 1:
			return "", map[string]any{"n": 1}, nil
		case 2:
			return "big", map[string]any{"n": 2}, nil
		case 3:
			return "", nil, errors.New("three")
		}

		return "", nil, nil // sent on nowhere
	})

	in := Ports{MainPort: {{JSON: map[string]any{"n": 1}}, {JSON: map[string]any{"n": 2}}, {JSON: map[string]any{"n": 3}}, {JSON: map[string]any{"n": 4}}}}

	out, err := node.Run(context.Background(), Run{}, in)
	require.NoError(t, err)

	require.Len(t, out[MainPort], 2, "the first, and the third failed")
	assert.Equal(t, []ItemRef{{Port: MainPort, Index: 0}}, out[MainPort][0].Sources)
	assert.Equal(t, "three", out[MainPort][1].Err)
	assert.Equal(t, []ItemRef{{Port: MainPort, Index: 2}}, out[MainPort][1].Sources)
	require.Len(t, out["big"], 1)
	assert.Equal(t, []ItemRef{{Port: MainPort, Index: 1}}, out["big"][0].Sources)
}
