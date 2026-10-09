package postgres

import (
	"context"
	"errors"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.TemplateRepository = (*Templates)(nil)

type Templates struct {
	db *DB
}

func NewTemplates(db *DB) *Templates {
	return &Templates{db: db}
}

func (r *Templates) Save(ctx context.Context, t domain.Template) (domain.Template, error) {
	m, err := toTemplateModel(t)
	if err != nil {
		return domain.Template{}, err
	}

	tt := table.Templates

	var saved model.Templates

	err = tt.INSERT(tt.AllColumns).
		MODEL(m).
		ON_CONFLICT(tt.AuthorID, tt.SourceWorkflowID).
		DO_UPDATE(jet.SET(
			tt.Name.SET(tt.EXCLUDED.Name),
			tt.Summary.SET(tt.EXCLUDED.Summary),
			tt.Description.SET(tt.EXCLUDED.Description),
			tt.Definition.SET(tt.EXCLUDED.Definition),
			tt.UpdatedAt.SET(tt.EXCLUDED.UpdatedAt),
		)).
		RETURNING(tt.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &saved)
	if err != nil {
		return domain.Template{}, err
	}

	return toTemplate(saved)
}

func (r *Templates) Get(ctx context.Context, id domain.TemplateID) (domain.Template, error) {
	tid, err := parseID(id)
	if err != nil {
		return domain.Template{}, err
	}

	return r.one(ctx, table.Templates.ID.EQ(jet.UUID(tid)))
}

func (r *Templates) BySource(ctx context.Context, author domain.UserID, source domain.WorkflowID) (domain.Template, error) {
	uid, err := parseID(author)
	if err != nil {
		return domain.Template{}, err
	}

	wid, err := parseID(source)
	if err != nil {
		return domain.Template{}, err
	}

	return r.one(ctx, table.Templates.AuthorID.EQ(jet.UUID(uid)).AND(table.Templates.SourceWorkflowID.EQ(jet.UUID(wid))))
}

func (r *Templates) one(ctx context.Context, where jet.BoolExpression) (domain.Template, error) {
	var m model.Templates

	err := jet.SELECT(table.Templates.AllColumns).FROM(table.Templates).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.Template{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.Template{}, err
	}

	return toTemplate(m)
}

func (r *Templates) List(ctx context.Context, f ports.PageFilter) (ports.Page[domain.Template], error) {
	limit := pageLimit(f.Limit)
	where := jet.Bool(true)

	if f.Cursor != "" {
		before, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.Template]{}, domain.ErrInvalidInput
		}

		where = table.Templates.ID.LT(jet.UUID(before))
	}

	var rows []model.Templates

	// IDs are UUIDv7, so descending IDs are newest first.
	err := jet.SELECT(table.Templates.AllColumns).
		FROM(table.Templates).
		WHERE(where).
		ORDER_BY(table.Templates.ID.DESC()).
		LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return ports.Page[domain.Template]{}, err
	}

	page := ports.Page[domain.Template]{Items: make([]domain.Template, len(rows))}

	for i, m := range rows {
		if page.Items[i], err = toTemplate(m); err != nil {
			return ports.Page[domain.Template]{}, err
		}
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

func (r *Templates) CountByAuthor(ctx context.Context, author domain.UserID) (int, error) {
	uid, err := parseID(author)
	if err != nil {
		return 0, err
	}

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(table.Templates).
		WHERE(table.Templates.AuthorID.EQ(jet.UUID(uid))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *Templates) Delete(ctx context.Context, id domain.TemplateID) error {
	tid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.Templates.DELETE().WHERE(table.Templates.ID.EQ(jet.UUID(tid))).ExecContext(ctx, r.db.conn(ctx)))
}

func toTemplateModel(t domain.Template) (model.Templates, error) {
	id, err := parseID(t.ID)
	if err != nil {
		return model.Templates{}, err
	}

	author, err := parseID(t.AuthorID)
	if err != nil {
		return model.Templates{}, err
	}

	def, err := encodeDefinition(t.Nodes, t.Connections)
	if err != nil {
		return model.Templates{}, err
	}

	m := model.Templates{
		ID: id, AuthorID: author, Name: t.Name, Summary: t.Summary, Description: t.Description,
		Definition: def, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}

	if t.SourceID != "" {
		source, err := parseID(t.SourceID)
		if err != nil {
			return model.Templates{}, err
		}

		m.SourceWorkflowID = &source
	}

	return m, nil
}

func toTemplate(m model.Templates) (domain.Template, error) {
	nodes, conns, err := decodeDefinition(m.Definition)
	if err != nil {
		return domain.Template{}, err
	}

	t := domain.Template{
		ID: domain.TemplateID(m.ID.String()), AuthorID: domain.UserID(m.AuthorID.String()),
		Name: m.Name, Summary: m.Summary, Description: m.Description,
		Nodes: nodes, Connections: conns,
		CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC(),
	}

	if m.SourceWorkflowID != nil {
		t.SourceID = domain.WorkflowID(m.SourceWorkflowID.String())
	}

	return t, nil
}
