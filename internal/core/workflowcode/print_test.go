package workflowcode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/workflowcode"
)

type fixture struct {
	Workflow struct {
		Name  string `json:"name"`
		Nodes []struct {
			ID             string         `json:"id"`
			Type           string         `json:"type"`
			Name           string         `json:"name"`
			Notes          string         `json:"notes"`
			ContinueOnFail bool           `json:"continueOnFail"`
			Params         map[string]any `json:"params"`
		} `json:"nodes"`
		Connections []struct {
			From     string `json:"from"`
			FromPort string `json:"fromPort"`
			To       string `json:"to"`
			ToPort   string `json:"toPort"`
		} `json:"connections"`
	} `json:"workflow"`
	Types []struct {
		Name    string   `json:"name"`
		Trigger bool     `json:"trigger"`
		Params  []string `json:"params"`
	} `json:"types"`
}

// The app's printer wrote each fixture's code (D400): this one prints the same, character
// for character, so an agent reads what the Code view shows.
func TestPrintAsTheApp(t *testing.T) {
	files, err := filepath.Glob("testdata/print/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			require.NoError(t, err)

			var f fixture
			require.NoError(t, json.Unmarshal(data, &f))

			nodes := make([]domain.NodeSpec, len(f.Workflow.Nodes))
			for i, n := range f.Workflow.Nodes {
				nodes[i] = domain.NodeSpec{
					ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Notes: n.Notes,
					ContinueOnFail: n.ContinueOnFail, Params: n.Params,
				}
			}

			conns := make([]domain.Connection, len(f.Workflow.Connections))
			for i, c := range f.Workflow.Connections {
				conns[i] = domain.Connection{From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort}
			}

			types := map[string]workflowcode.Type{}
			for _, ty := range f.Types {
				types[ty.Name] = workflowcode.Type{Trigger: ty.Trigger, Params: ty.Params}
			}

			got := workflowcode.Print(f.Workflow.Name, nodes, conns, func(name string) (workflowcode.Type, bool) {
				ty, ok := types[name]
				return ty, ok
			})

			want, err := os.ReadFile(strings.TrimSuffix(file, ".json") + ".ts")
			require.NoError(t, err)
			assert.Equal(t, string(want), got)
		})
	}
}
