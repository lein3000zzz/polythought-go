package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Limits on ports that come from a param, so a node stays readable on the canvas.
const (
	MaxParamPorts   = 10
	MaxPortNameSize = 40 // characters
)

var errOutputsParam = errors.New("outputs param must name a list param, or a table and its text column")

// OutputsOf lists the output ports of a node of this type with params: the fixed ones,
// then one per value of its OutputsParam, or of that param's default when it is unset;
// for a table, one per row, named by its OutputsColumn.
func (t NodeType) OutputsOf(params map[string]any) []string {
	ports, _ := t.paramPorts(params)

	return append(slices.Clone(t.Outputs), ports...)
}

// paramPorts is the values of the node's OutputsParam; ok is false when they are not a
// list of strings, which checkParams reports.
func (t NodeType) paramPorts(params map[string]any) (ports []string, ok bool) {
	if t.OutputsParam == "" {
		return nil, true
	}

	value := params[t.OutputsParam]
	if value == nil {
		for _, p := range t.Params {
			if p.Name == t.OutputsParam {
				value = p.Default
			}
		}
	}

	if value == nil {
		return nil, true
	}

	if t.OutputsColumn != "" {
		return t.columnPorts(value)
	}

	ports, err := Params{t.OutputsParam: value}.Strings(t.OutputsParam)

	return ports, err == nil
}

// columnPorts is the OutputsColumn of each row of a table, empty where a row has none.
func (t NodeType) columnPorts(value any) ([]string, bool) {
	rows, err := Params{t.OutputsParam: value}.Table(t.OutputsParam, nil)
	if err != nil {
		return nil, false
	}

	ports := make([]string, len(rows))
	for i, row := range rows {
		ports[i], _ = row[t.OutputsColumn].(string)
	}

	return ports, true
}

func checkOutputsParam(typ NodeType) error {
	if typ.OutputsParam == "" {
		return nil
	}

	for _, p := range typ.Params {
		switch {
		case p.Name != typ.OutputsParam:
		case typ.OutputsColumn == "" && p.Kind == ParamList:
			return nil
		case typ.OutputsColumn != "" && p.Kind == ParamTable && slices.ContainsFunc(p.Columns, func(c ParamSpec) bool {
			return c.Name == typ.OutputsColumn && c.Kind == ParamString
		}):
			return nil
		}
	}

	return fmt.Errorf("register node type %q: %w, not %q", typ.Name, errOutputsParam, typ.OutputsParam)
}

// checkParamPorts reports values of a node's OutputsParam that cannot name a port.
func checkParamPorts(v *domain.ValidationError, n domain.NodeSpec, typ NodeType) {
	ports, ok := typ.paramPorts(n.Params)
	if typ.OutputsParam == "" || !ok {
		return
	}

	problem := func(format string, args ...any) {
		v.Add(domain.Problem{
			NodeID:  n.ID,
			Param:   typ.OutputsParam,
			Message: fmt.Sprintf("node %q: param %q ", n.ID, typ.OutputsParam) + fmt.Sprintf(format, args...),
			Err:     ErrInvalidParam,
		})
	}

	value, empty := "value", "has an empty value"
	if typ.OutputsColumn != "" {
		value, empty = "row", "has a row without a port name"
	}

	switch {
	case len(ports) == 0:
		problem("needs at least one %s", value)
	case len(ports) > MaxParamPorts:
		problem("has %d %ss, more than the %d allowed", len(ports), value, MaxParamPorts)
	}

	seen := make(map[string]bool, len(ports))

	for _, port := range ports {
		switch {
		case strings.TrimSpace(port) == "":
			problem("%s", empty)
		case utf8.RuneCountInString(port) > MaxPortNameSize:
			problem("%q is longer than %d characters", port, MaxPortNameSize)
		case seen[port]:
			problem("has %q twice", port)
		case port == ErrorPort || slices.Contains(typ.Outputs, port):
			problem("cannot use %q, the name of another port", port)
		}

		seen[port] = true
	}
}
