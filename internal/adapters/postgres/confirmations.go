package postgres

import (
	"context"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.ConfirmationRepository = (*Confirmations)(nil)

type Confirmations struct {
	db *DB
}

func NewConfirmations(db *DB) *Confirmations {
	return &Confirmations{db: db}
}

func (r *Confirmations) Create(ctx context.Context, c domain.Confirmation) error {
	id, err := uuid.Parse(string(c.ID))
	if err != nil {
		return err
	}

	owner, err := parseID(c.OwnerID)
	if err != nil {
		return err
	}

	m := model.Confirmations{
		ID: id, OwnerID: owner, Kind: string(c.Kind), GrantID: string(c.GrantID), TokenID: string(c.TokenID), Agent: c.Agent,
		Scopes: scopesArray(c.Scopes), What: c.What, About: c.About, Detail: c.Detail, Payload: string(c.Payload), Status: string(c.Status), Result: c.Result,
		CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt, DecidedAt: timePtr(c.DecidedAt), TelegramMessage: int64(c.Message),
		DecidedVia: string(c.DecidedVia),
	}

	if c.WorkflowID != "" {
		wf, err := parseID(c.WorkflowID)
		if err != nil {
			return err
		}

		m.WorkflowID = &wf
	}

	if m.Payload == "" {
		m.Payload = "{}"
	}

	_, err = table.Confirmations.INSERT(table.Confirmations.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Confirmations) Get(ctx context.Context, owner domain.UserID, id domain.ConfirmationID) (domain.Confirmation, error) {
	where, err := ofOwner(owner, id)
	if err != nil {
		return domain.Confirmation{}, err
	}

	var m model.Confirmations

	err = table.Confirmations.SELECT(table.Confirmations.AllColumns).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.Confirmation{}, notFound(err)
	}

	return confirmationOf(m), nil
}

func (r *Confirmations) Recent(ctx context.Context, owner domain.UserID, limit int) ([]domain.Confirmation, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	var rows []model.Confirmations

	err = table.Confirmations.SELECT(table.Confirmations.AllColumns).
		WHERE(table.Confirmations.OwnerID.EQ(jet.UUID(oid))).
		ORDER_BY(table.Confirmations.CreatedAt.DESC()).
		LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Confirmation, len(rows))
	for i, m := range rows {
		out[i] = confirmationOf(m)
	}

	return out, nil
}

func (r *Confirmations) CountPending(ctx context.Context, owner domain.UserID, now time.Time) (int, error) {
	oid, err := parseID(owner)
	if err != nil {
		return 0, err
	}

	c := table.Confirmations

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(c).
		WHERE(c.OwnerID.EQ(jet.UUID(oid)).
			AND(c.Status.EQ(jet.String(string(domain.ConfirmPending)))).
			AND(c.ExpiresAt.GT(jet.TimestampzT(now)))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *Confirmations) Decide(
	ctx context.Context, owner domain.UserID, id domain.ConfirmationID, status domain.ConfirmStatus, now time.Time, via domain.ConfirmVia,
) (domain.Confirmation, error) {
	where, err := ofOwner(owner, id)
	if err != nil {
		return domain.Confirmation{}, err
	}

	c := table.Confirmations

	var rows []model.Confirmations

	// Pending and in time, in the same statement, so it is decided once.
	err = c.UPDATE(c.Status, c.DecidedAt, c.DecidedVia).
		SET(jet.String(string(status)), jet.TimestampzT(now), jet.String(string(via))).
		WHERE(where.AND(c.Status.EQ(jet.String(string(domain.ConfirmPending)))).AND(c.ExpiresAt.GT(jet.TimestampzT(now)))).
		RETURNING(c.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return domain.Confirmation{}, err
	}

	if len(rows) == 0 {
		if _, err := r.Get(ctx, owner, id); err != nil {
			return domain.Confirmation{}, err
		}

		return domain.Confirmation{}, domain.ErrConflict
	}

	return confirmationOf(rows[0]), nil
}

func (r *Confirmations) Finish(ctx context.Context, id domain.ConfirmationID, status domain.ConfirmStatus, result string) error {
	cid, err := parseID(id)
	if err != nil {
		return err
	}

	c := table.Confirmations

	return affectedOne(c.UPDATE(c.Status, c.Result).
		SET(jet.String(string(status)), jet.String(result)).
		WHERE(c.ID.EQ(jet.UUID(cid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Confirmations) SetMessage(ctx context.Context, id domain.ConfirmationID, message int) error {
	cid, err := parseID(id)
	if err != nil {
		return err
	}

	c := table.Confirmations

	return affectedOne(c.UPDATE(c.TelegramMessage).
		SET(jet.Int64(int64(message))).
		WHERE(c.ID.EQ(jet.UUID(cid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func ofOwner(owner domain.UserID, id domain.ConfirmationID) (jet.BoolExpression, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	cid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	return table.Confirmations.ID.EQ(jet.UUID(cid)).AND(table.Confirmations.OwnerID.EQ(jet.UUID(oid))), nil
}

func confirmationOf(m model.Confirmations) domain.Confirmation {
	c := domain.Confirmation{
		ID: domain.ConfirmationID(m.ID.String()), OwnerID: domain.UserID(m.OwnerID.String()), Kind: domain.ConfirmKind(m.Kind),
		GrantID: domain.GrantID(m.GrantID), TokenID: domain.APITokenID(m.TokenID), Agent: m.Agent, Scopes: toScopes(m.Scopes),
		What: m.What, About: m.About, Detail: m.Detail, Payload: []byte(m.Payload), Status: domain.ConfirmStatus(m.Status), Result: m.Result,
		CreatedAt: m.CreatedAt.UTC(), ExpiresAt: m.ExpiresAt.UTC(), Message: int(m.TelegramMessage),
		DecidedVia: domain.ConfirmVia(m.DecidedVia),
	}

	if m.WorkflowID != nil {
		c.WorkflowID = domain.WorkflowID(m.WorkflowID.String())
	}

	if m.DecidedAt != nil {
		c.DecidedAt = m.DecidedAt.UTC()
	}

	return c
}
