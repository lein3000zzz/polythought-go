package workflows

import (
	"slices"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// keepBorrowed puts another user's text back where p was shown it as domain.BorrowedText
// (A21), and says what of the workflow stays borrowed once def is saved over stored: what
// the user changes at a browser is theirs from then on; what a token or an agent changes
// may keep the words it read, so it stays marked.
func keepBorrowed(p auth.Principal, def Definition, stored domain.Workflow) (Definition, domain.Borrowed) {
	had := make(map[domain.NodeID]domain.NodeSpec, len(stored.Nodes))
	for _, n := range stored.Nodes {
		had[n.ID] = n
	}

	back := func(text, was string, borrowed bool) string {
		switch {
		case text != domain.BorrowedText:
			return text
		case borrowed:
			return was
		}

		return ""
	}

	def.Name = back(def.Name, stored.Name, stored.Borrowed.Name)

	var kept domain.Borrowed

	kept.Name = stored.Borrowed.Name && (def.Name == stored.Name || !p.Itself())

	nodes := slices.Clone(def.Nodes)

	for i, n := range nodes {
		old, ok := had[n.ID]
		borrowed := ok && stored.Borrowed.Node(n.ID)

		n.Name = back(n.Name, old.Name, borrowed)
		n.Notes = back(n.Notes, old.Notes, borrowed)
		nodes[i] = n

		changed := n.Name != old.Name || n.Notes != old.Notes
		if borrowed && (n.Name != "" || n.Notes != "") && (!changed || !p.Itself()) {
			kept.Nodes = append(kept.Nodes, n.ID)
		}
	}

	def.Nodes = nodes

	return def, kept
}

// borrowedOf is what a new workflow keeps of another user's, from a library entry of
// theirs: of the nodes it has.
func borrowedOf(def Definition) domain.Borrowed {
	if def.Borrowed == nil {
		return domain.Borrowed{}
	}

	b := domain.Borrowed{Name: def.Borrowed.Name}

	for _, n := range def.Nodes {
		if def.Borrowed.Node(n.ID) && (n.Name != "" || n.Notes != "") {
			b.Nodes = append(b.Nodes, n.ID)
		}
	}

	return b
}
