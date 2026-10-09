//go:build db

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func newTemplate(author domain.UserID, source domain.WorkflowID, name string) domain.Template {
	return domain.Template{
		ID: domain.TemplateID(newID()), AuthorID: author, SourceID: source,
		Name: name, Summary: "One line", Description: "First paragraph.\n\nSecond.",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual", Notes: "Starts it", Position: domain.CanvasPosition{X: 10, Y: 20}},
			{ID: "tight", Type: "logic.filter", Params: map[string]any{"field": "spread", "value": json.Number("0.02")}},
		},
		Connections: []domain.Connection{{From: "manual", FromPort: "main", To: "tight", ToPort: "main"}},
		CreatedAt:   at, UpdatedAt: at,
	}
}

func TestTemplates(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, repo *postgres.Templates, author domain.User, source domain.Workflow)
	}{
		{"round trip keeps the graph, notes and exact numbers", func(t *testing.T, _ *postgres.DB, repo *postgres.Templates, author domain.User, source domain.Workflow) {
			tpl := newTemplate(author.ID, source.ID, "spreads")

			saved, err := repo.Save(ctx, tpl)
			require.NoError(t, err)
			assert.Equal(t, tpl, saved)

			got, err := repo.Get(ctx, tpl.ID)
			require.NoError(t, err)
			assert.Equal(t, tpl, got)

			bySource, err := repo.BySource(ctx, author.ID, source.ID)
			require.NoError(t, err)
			assert.Equal(t, tpl.ID, bySource.ID)
		}},
		{"sharing the same workflow again replaces its copy", func(t *testing.T, _ *postgres.DB, repo *postgres.Templates, author domain.User, source domain.Workflow) {
			first, err := repo.Save(ctx, newTemplate(author.ID, source.ID, "v1"))
			require.NoError(t, err)

			again := newTemplate(author.ID, source.ID, "v2") // a new ID, as a caller that did not look first would send
			again.UpdatedAt = at.Add(time.Hour)
			again.CreatedAt = at.Add(time.Hour)

			saved, err := repo.Save(ctx, again)
			require.NoError(t, err)
			assert.Equal(t, first.ID, saved.ID, "the copy keeps its ID")
			assert.Equal(t, "v2", saved.Name)
			assert.Equal(t, at, saved.CreatedAt, "and when it was first shared")
			assert.Equal(t, at.Add(time.Hour), saved.UpdatedAt)

			n, err := repo.CountByAuthor(ctx, author.ID)
			require.NoError(t, err)
			assert.Equal(t, 1, n)
		}},
		{"a copy outlives the workflow it came from", func(t *testing.T, db *postgres.DB, repo *postgres.Templates, author domain.User, source domain.Workflow) {
			tpl, err := repo.Save(ctx, newTemplate(author.ID, source.ID, "kept"))
			require.NoError(t, err)

			require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, author.ID, source.ID))

			got, err := repo.Get(ctx, tpl.ID)
			require.NoError(t, err)
			assert.Empty(t, got.SourceID)
		}},
		{"newest first, paged, across authors", func(t *testing.T, db *postgres.DB, repo *postgres.Templates, author domain.User, source domain.Workflow) {
			other := seedUser(t, db, "eve@x.io", domain.RoleUser)

			var ids []domain.TemplateID
			for i, a := range []domain.UserID{author.ID, other.ID, author.ID} {
				tpl := newTemplate(a, "", string(rune('a'+i)))
				_, err := repo.Save(ctx, tpl)
				require.NoError(t, err)
				ids = append(ids, tpl.ID)
			}

			first, err := repo.List(ctx, ports.PageFilter{Limit: 2})
			require.NoError(t, err)
			require.Len(t, first.Items, 2)
			assert.Equal(t, []domain.TemplateID{ids[2], ids[1]}, []domain.TemplateID{first.Items[0].ID, first.Items[1].ID})

			second, err := repo.List(ctx, ports.PageFilter{Limit: 2, Cursor: first.NextCursor})
			require.NoError(t, err)
			require.Len(t, second.Items, 1)
			assert.Equal(t, ids[0], second.Items[0].ID)
			assert.Empty(t, second.NextCursor)
		}},
		{"delete", func(t *testing.T, _ *postgres.DB, repo *postgres.Templates, author domain.User, source domain.Workflow) {
			tpl, err := repo.Save(ctx, newTemplate(author.ID, source.ID, "gone"))
			require.NoError(t, err)

			require.NoError(t, repo.Delete(ctx, tpl.ID))
			assert.ErrorIs(t, repo.Delete(ctx, tpl.ID), domain.ErrNotFound)

			_, err = repo.Get(ctx, tpl.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
			_, err = repo.Get(ctx, "not-a-uuid")
			assert.ErrorIs(t, err, domain.ErrNotFound)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			author := seedUser(t, db, "author@x.io", domain.RoleUser)
			source := newWorkflow(author.ID, "source")
			require.NoError(t, postgres.NewWorkflows(db).Create(ctx, source))

			tt.run(t, db, postgres.NewTemplates(db), author, source)
		})
	}
}
