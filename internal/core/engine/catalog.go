package engine

import (
	"context"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Catalog gives the node types one owner's workflows use: the built-in ones and the
// owner's custom nodes.
type Catalog interface {
	For(ctx context.Context, owner domain.UserID) (*Registry, error)
}

var _ Catalog = (*Registry)(nil)

// WithCatalog has the executor take each workflow's node types from c, for its owner.
// Without it, every owner gets the registry the executor was made with.
func WithCatalog(c Catalog) ExecutorOption {
	return func(e *Executor) { e.catalog = c }
}

// CustomTypePrefix starts the type name of every custom node: custom.spread-split.
const CustomTypePrefix = "custom."

// metricName is the type a node run is counted under. Custom nodes count together:
// their names are their owners', and would make a label value each.
func metricName(typ NodeType) string {
	if strings.HasPrefix(typ.Name, CustomTypePrefix) {
		return "custom"
	}

	return typ.Name
}
