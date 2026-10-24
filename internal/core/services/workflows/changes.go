package workflows

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// maxValue is the most of one setting's value a change's description shows.
const maxValue = 160

// changes says what a new graph changes of a workflow, a line each, for its user to read
// before they confirm it (A3): its name, the nodes added, removed and changed, setting by
// setting, and the connections. Personal settings say only that they changed.
func (s *Service) changes(before, after domain.Workflow) string {
	var lines []string

	if before.Name != after.Name {
		lines = append(lines, fmt.Sprintf("Name: %q → %q", before.Name, after.Name))
	}

	had := make(map[domain.NodeID]domain.NodeSpec, len(before.Nodes))
	for _, n := range before.Nodes {
		had[n.ID] = n
	}

	kept := map[domain.NodeID]bool{}

	for _, n := range after.Nodes {
		old, ok := had[n.ID]
		if !ok {
			lines = append(lines, fmt.Sprintf("+ node %s (%s)%s", n.ID, n.Type, s.settingsOf(n, nil)))
			continue
		}

		kept[n.ID] = true

		var what []string

		if old.Type != n.Type {
			what = append(what, fmt.Sprintf("type %s → %s", old.Type, n.Type))
		}

		if old.Name != n.Name {
			what = append(what, fmt.Sprintf("name %q → %q", old.Name, n.Name))
		}

		if old.Notes != n.Notes {
			what = append(what, "notes changed")
		}

		if old.ContinueOnFail != n.ContinueOnFail {
			what = append(what, fmt.Sprintf("continue on failure %t → %t", old.ContinueOnFail, n.ContinueOnFail))
		}

		if changed := s.settingsOf(n, &old); changed != "" {
			what = append(what, strings.TrimPrefix(changed, ": "))
		}

		if len(what) > 0 {
			lines = append(lines, fmt.Sprintf("~ node %s (%s): %s", n.ID, n.Type, strings.Join(what, "; ")))
		}
	}

	for _, n := range before.Nodes {
		if !kept[n.ID] {
			lines = append(lines, fmt.Sprintf("- node %s (%s)", n.ID, n.Type))
		}
	}

	conn := func(c domain.Connection) string {
		return fmt.Sprintf("%s.%s → %s.%s", c.From, c.FromPort, c.To, c.ToPort)
	}

	wasConnected := map[string]bool{}
	for _, c := range before.Connections {
		wasConnected[conn(c)] = true
	}

	isConnected := map[string]bool{}
	for _, c := range after.Connections {
		isConnected[conn(c)] = true

		if !wasConnected[conn(c)] {
			lines = append(lines, "+ connection "+conn(c))
		}
	}

	for _, c := range before.Connections {
		if !isConnected[conn(c)] {
			lines = append(lines, "- connection "+conn(c))
		}
	}

	if len(lines) == 0 {
		return "Nothing but where its nodes sit on the canvas."
	}

	return strings.Join(lines, "\n")
}

// settingsOf is a node's settings, as ": key value, …" for one added, or those that
// changed since old, as "key old → new; …"; empty when none did.
func (s *Service) settingsOf(n domain.NodeSpec, old *domain.NodeSpec) string {
	personal := map[string]bool{}
	if s.personal != nil {
		for _, k := range s.personal(n.Type) {
			personal[k] = true
		}
	}

	keys := slices.Sorted(maps.Keys(n.Params))
	if old != nil {
		for k := range old.Params {
			if _, ok := n.Params[k]; !ok {
				keys = append(keys, k)
			}
		}

		slices.Sort(keys)
	}

	var parts []string

	for _, k := range keys {
		v, now := n.Params[k]

		if old == nil {
			if personal[k] {
				parts = append(parts, k+" set (personal)")
			} else {
				parts = append(parts, k+" "+shown(v))
			}

			continue
		}

		was, before := old.Params[k]

		switch {
		case before && now && shown(was) == shown(v):
			continue
		case personal[k]:
			parts = append(parts, k+" changed (personal)")
		case !before:
			parts = append(parts, k+" "+shown(v))
		case !now:
			parts = append(parts, k+" removed")
		default:
			parts = append(parts, fmt.Sprintf("%s %s → %s", k, shown(was), shown(v)))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	if old == nil {
		return ": " + strings.Join(parts, ", ")
	}

	return strings.Join(parts, "; ")
}

// shown is a setting's value as JSON, cut to maxValue.
func shown(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	text := string(data)
	if len([]rune(text)) > maxValue {
		text = string([]rune(text)[:maxValue]) + "…"
	}

	return text
}
