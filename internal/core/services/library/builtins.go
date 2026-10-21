package library

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// The built-in scenarios ship with the server, one JSON file each. File names order
// them in the library: start with a number.
//
//go:embed scenarios/*.json
var scenarioFiles embed.FS

type scenarioDoc struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Nodes       []struct {
		ID             string         `json:"id"`
		Type           string         `json:"type"`
		Name           string         `json:"name"`
		Params         map[string]any `json:"params"`
		ContinueOnFail bool           `json:"continueOnFail"`
		Notes          string         `json:"notes"`
		Position       struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		} `json:"position"`
	} `json:"nodes"`
	Connections []struct {
		From     string `json:"from"`
		FromPort string `json:"fromPort"`
		To       string `json:"to"`
		ToPort   string `json:"toPort"`
	} `json:"connections"`
}

// Builtins reads the built-in scenarios, in library order.
func Builtins() ([]domain.Template, error) {
	names, err := fs.Glob(scenarioFiles, "scenarios/*.json")
	if err != nil {
		return nil, err
	}

	out := make([]domain.Template, 0, len(names))

	for _, name := range names {
		raw, err := scenarioFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}

		t, err := ParseScenario(raw)
		if err != nil {
			return nil, fmt.Errorf("library: scenario %s: %w", name, err)
		}

		out = append(out, t)
	}

	return out, nil
}

// ParseScenario reads a scenario as the built-in ones are written, strictly: a field it
// does not know is an error, and numbers stay exact. Plugins' scenarios are written so too.
func ParseScenario(raw []byte) (domain.Template, error) {
	var doc scenarioDoc

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber() // 0.02 stays exactly 0.02

	if err := dec.Decode(&doc); err != nil {
		return domain.Template{}, err
	}

	t := domain.Template{ID: domain.TemplateID(doc.ID), Name: doc.Name, Summary: doc.Summary, Description: doc.Description}

	for _, n := range doc.Nodes {
		t.Nodes = append(t.Nodes, domain.NodeSpec{
			ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Params: n.Params,
			ContinueOnFail: n.ContinueOnFail, Notes: n.Notes,
			Position: domain.CanvasPosition{X: n.Position.X, Y: n.Position.Y},
		})
	}

	for _, c := range doc.Connections {
		t.Connections = append(t.Connections, domain.Connection{
			From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort,
		})
	}

	return t, nil
}
