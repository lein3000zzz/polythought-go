package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// TemplateRepository keeps the workflows members share. Built-in scenarios are not here.
type TemplateRepository interface {
	// Save stores t, replacing the author's earlier copy of the same source workflow, and
	// returns what was stored: an earlier copy keeps its ID and creation time.
	Save(ctx context.Context, t domain.Template) (domain.Template, error)
	Get(ctx context.Context, id domain.TemplateID) (domain.Template, error)
	// BySource finds the copy author shared of a workflow, or returns domain.ErrNotFound.
	BySource(ctx context.Context, author domain.UserID, source domain.WorkflowID) (domain.Template, error)
	// List returns every member's templates, newest first.
	List(ctx context.Context, f PageFilter) (Page[domain.Template], error)
	CountByAuthor(ctx context.Context, author domain.UserID) (int, error)
	Delete(ctx context.Context, id domain.TemplateID) error
}
