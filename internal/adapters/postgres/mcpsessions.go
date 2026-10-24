package postgres

import (
	"context"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.MCPSessionRepository = (*MCPSessions)(nil)

// MCPSessions keeps the sessions open on /mcp (D414).
type MCPSessions struct {
	db *DB
}

func NewMCPSessions(db *DB) *MCPSessions {
	return &MCPSessions{db: db}
}

func (r *MCPSessions) Open(ctx context.Context, s ports.MCPSession) error {
	uid, err := parseID(s.UserID)
	if err != nil {
		return err
	}

	t := table.McpSessions

	_, err = t.INSERT(t.AllColumns).MODEL(model.McpSessions{
		ID: s.ID, UserID: uid, Holder: s.Holder, Name: s.Name, Client: s.Client, Boot: s.Boot,
		OpenedAt: s.OpenedAt, SeenAt: s.SeenAt,
	}).ON_CONFLICT(t.ID).DO_NOTHING().ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *MCPSessions) Seen(ctx context.Context, id string, at time.Time) error {
	t := table.McpSessions

	_, err := t.UPDATE(t.SeenAt).SET(jet.TimestampzT(at)).WHERE(t.ID.EQ(jet.String(id))).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *MCPSessions) Close(ctx context.Context, id string) error {
	t := table.McpSessions

	_, err := t.DELETE().WHERE(t.ID.EQ(jet.String(id))).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *MCPSessions) Supersede(ctx context.Context, user domain.UserID, holder, boot string) error {
	uid, err := parseID(user)
	if err != nil {
		return err
	}

	t := table.McpSessions

	_, err = t.DELETE().
		WHERE(t.UserID.EQ(jet.UUID(uid)).AND(t.Holder.EQ(jet.String(holder))).AND(t.Boot.NOT_EQ(jet.String(boot)))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *MCPSessions) Of(ctx context.Context, user domain.UserID, since time.Time) ([]ports.MCPSession, error) {
	uid, err := parseID(user)
	if err != nil {
		return nil, err
	}

	t := table.McpSessions

	var rows []model.McpSessions

	err = t.SELECT(t.AllColumns).
		WHERE(t.UserID.EQ(jet.UUID(uid)).AND(t.SeenAt.GT_EQ(jet.TimestampzT(since)))).
		ORDER_BY(t.OpenedAt.DESC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ports.MCPSession, len(rows))
	for i, m := range rows {
		out[i] = ports.MCPSession{
			ID: m.ID, UserID: domain.UserID(m.UserID.String()), Holder: m.Holder, Name: m.Name, Client: m.Client, Boot: m.Boot,
			OpenedAt: m.OpenedAt.UTC(), SeenAt: m.SeenAt.UTC(),
		}
	}

	return out, nil
}

func (r *MCPSessions) Prune(ctx context.Context, before time.Time) error {
	t := table.McpSessions

	_, err := t.DELETE().WHERE(t.SeenAt.LT(jet.TimestampzT(before))).ExecContext(ctx, r.db.conn(ctx))

	return err
}
