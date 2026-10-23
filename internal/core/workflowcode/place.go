package workflowcode

import (
	"math"
	"slices"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Where an agent's nodes go on the canvas (D403), as the editor puts them: a port of the
// app's layout (web/src/features/workflow/layout.ts) and placing
// (web/src/features/workflowcode/place.ts), which the fixtures in testdata/place hold the
// two to.

const (
	columnWidth = 300
	rowHeight   = 140
)

// Layout places nodes left to right: each node's column is its longest distance from a
// node with no incoming connections, and nodes keep their order within a column.
// Connections naming unknown nodes are ignored.
func Layout(nodes []domain.NodeSpec, connections []domain.Connection) map[domain.NodeID]domain.CanvasPosition {
	column := make(map[domain.NodeID]int, len(nodes))
	incoming := make(map[domain.NodeID]int, len(nodes))
	outgoing := map[domain.NodeID][]domain.NodeID{}

	for _, n := range nodes {
		column[n.ID], incoming[n.ID] = 0, 0
	}

	for _, c := range connections {
		if _, ok := column[c.From]; !ok {
			continue
		}

		if _, ok := column[c.To]; !ok {
			continue
		}

		incoming[c.To]++
		outgoing[c.From] = append(outgoing[c.From], c.To)
	}

	// Kahn's algorithm: a node is placed once everything feeding it is. Nodes in a cycle,
	// which a saved workflow cannot have, simply stay in their column.
	var ready []domain.NodeID

	for _, n := range nodes {
		if incoming[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}

	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]

		for _, next := range outgoing[id] {
			column[next] = max(column[next], column[id]+1)
			incoming[next]--

			if incoming[next] == 0 {
				ready = append(ready, next)
			}
		}
	}

	rows := map[int]int{}
	out := make(map[domain.NodeID]domain.CanvasPosition, len(nodes))

	for _, n := range nodes {
		col := column[n.ID]
		row := rows[col]

		rows[col] = row + 1
		out[n.ID] = domain.CanvasPosition{X: float64(col * columnWidth), Y: float64(row * rowHeight)}
	}

	return out
}

// Place gives the nodes of an edit their places: those the workflow had keep theirs, by
// ID; a new one goes after the node feeding it, else before the one it feeds, else under
// the rest, moved down past any node already there. With none kept, it is laid out as the
// canvas lays out a workflow with no places (where the app leaves them at 0, 0 for it).
func Place(nodes []domain.NodeSpec, connections []domain.Connection, before map[domain.NodeID]domain.CanvasPosition) []domain.NodeSpec {
	at := map[domain.NodeID]domain.CanvasPosition{}

	for _, n := range nodes {
		if kept, ok := before[n.ID]; ok {
			at[n.ID] = kept
		}
	}

	if len(at) == 0 {
		at = Layout(nodes, connections)
	} else {
		place(nodes, connections, at)
	}

	out := slices.Clone(nodes)
	for i := range out {
		out[i].Position = at[out[i].ID]
	}

	return out
}

func place(nodes []domain.NodeSpec, connections []domain.Connection, at map[domain.NodeID]domain.CanvasPosition) {
	// Left to right, so a chain of new nodes grows from where it joins the rest.
	columns := Layout(nodes, connections)

	var fresh []domain.NodeSpec

	for _, n := range nodes {
		if _, ok := at[n.ID]; !ok {
			fresh = append(fresh, n)
		}
	}

	slices.SortStableFunc(fresh, func(a, b domain.NodeSpec) int {
		return int(columns[a.ID].X - columns[b.ID].X)
	})

	for _, n := range fresh {
		var p domain.CanvasPosition

		from, fromOK := firstConnection(connections, func(c domain.Connection) bool { _, ok := at[c.From]; return c.To == n.ID && ok })
		to, toOK := firstConnection(connections, func(c domain.Connection) bool { _, ok := at[c.To]; return c.From == n.ID && ok })

		switch {
		case fromOK:
			f := at[from.From]
			p = domain.CanvasPosition{X: f.X + columnWidth, Y: f.Y}
		case toOK:
			t := at[to.To]
			p = domain.CanvasPosition{X: t.X - columnWidth, Y: t.Y}
		default:
			minX, maxY := math.Inf(1), math.Inf(-1)
			for _, q := range at {
				minX, maxY = math.Min(minX, q.X), math.Max(maxY, q.Y)
			}

			p = domain.CanvasPosition{X: minX, Y: maxY + rowHeight}
		}

		for taken(at, p) {
			p.Y += rowHeight
		}

		at[n.ID] = p
	}
}

func firstConnection(cs []domain.Connection, match func(domain.Connection) bool) (domain.Connection, bool) {
	for _, c := range cs {
		if match(c) {
			return c, true
		}
	}

	return domain.Connection{}, false
}

// taken says a node is already about at p.
func taken(at map[domain.NodeID]domain.CanvasPosition, p domain.CanvasPosition) bool {
	for _, q := range at {
		if math.Abs(q.X-p.X) < columnWidth/2 && math.Abs(q.Y-p.Y) < rowHeight/2 {
			return true
		}
	}

	return false
}
