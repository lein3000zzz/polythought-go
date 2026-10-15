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

var errOutputsParam = errors.New("outputs param must name a list param")

// OutputsOf lists the output ports of a node of this type with params: the fixed ones,
// then one per value of its OutputsParam, or of that param's default when it is unset.
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

	ports, err := Params{t.OutputsParam: value}.Strings(t.OutputsParam)

	return ports, err == nil
}

func checkOutputsParam(typ NodeType) error {
	if typ.OutputsParam == "" {
		return nil
	}

	for _, p := range typ.Params {
		if p.Name == typ.OutputsParam && p.Kind == ParamList {
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

	switch {
	case len(ports) == 0:
		problem("needs at least one value")
	case len(ports) > MaxParamPorts:
		problem("has %d values, more than the %d allowed", len(ports), MaxParamPorts)
	}

	seen := make(map[string]bool, len(ports))

	for _, port := range ports {
		switch {
		case strings.TrimSpace(port) == "":
			problem("has an empty value")
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
