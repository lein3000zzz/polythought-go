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

type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// The app's layout and placing wrote each fixture's places (D403): these give the same.
// With no node kept, the app leaves all at 0, 0 for the canvas to lay out, which Place
// does itself.
func TestPlaceAsTheApp(t *testing.T) {
	files, err := filepath.Glob("testdata/place/*.json")
	require.NoError(t, err)

	for _, file := range files {
		if strings.HasSuffix(file, ".out.json") {
			continue
		}

		t.Run(filepath.Base(file), func(t *testing.T) {
			var in struct {
				Nodes       []struct{ ID string }       `json:"nodes"`
				Connections []struct{ From, To string } `json:"connections"`
				Before      map[string]point            `json:"before"`
			}

			var want struct {
				Layout map[string]point `json:"layout"`
				Place  map[string]point `json:"place"`
			}

			read(t, file, &in)
			read(t, strings.TrimSuffix(file, ".json")+".out.json", &want)

			nodes := make([]domain.NodeSpec, len(in.Nodes))
			for i, n := range in.Nodes {
				nodes[i] = domain.NodeSpec{ID: domain.NodeID(n.ID), Type: "output.log"}
			}

			conns := make([]domain.Connection, len(in.Connections))
			for i, c := range in.Connections {
				conns[i] = domain.Connection{From: domain.NodeID(c.From), FromPort: "main", To: domain.NodeID(c.To), ToPort: "main"}
			}

			before := map[domain.NodeID]domain.CanvasPosition{}
			for id, p := range in.Before {
				before[domain.NodeID(id)] = domain.CanvasPosition{X: p.X, Y: p.Y}
			}

			layout := map[string]point{}
			for id, p := range workflowcode.Layout(nodes, conns) {
				layout[string(id)] = point{p.X, p.Y}
			}

			assert.Equal(t, want.Layout, layout, "layout")

			placed := map[string]point{}
			for _, n := range workflowcode.Place(nodes, conns, before) {
				placed[string(n.ID)] = point{n.Position.X, n.Position.Y}
			}

			if len(in.Before) == 0 {
				assert.Equal(t, want.Layout, placed, "laid out as the canvas would")
			} else {
				assert.Equal(t, want.Place, placed, "placed")
			}
		})
	}
}

func read(t *testing.T, file string, v any) {
	t.Helper()

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, v))
}
