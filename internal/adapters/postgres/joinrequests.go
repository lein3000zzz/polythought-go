package postgres

import (
	"context"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.JoinRequestRepository = (*JoinRequests)(nil)

// JoinRequests keeps requests to join (D394).
type JoinRequests struct {
	db *DB
}

func NewJoinRequests(db *DB) *JoinRequests {
	return &JoinRequests{db: db}
}

func (r *JoinRequests) Create(ctx context.Context, req domain.JoinRequest, tokenHash []byte) error {
	id, err := uuid.Parse(string(req.ID))
	if err != nil {
		return err
	}

	m := model.JoinRequests{
		ID: id, Email: req.Email, EmailNormalized: normalizedEmail(req.Email), Note: req.Note, TokenHash: &tokenHash,
		LinkExpiresAt: req.LinkExpiresAt, Mails: 1, MailedAt: req.MailedAt, CreatedAt: req.CreatedAt,
	}

	_, err = table.JoinRequests.INSERT(table.JoinRequests.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *JoinRequests) Open(ctx context.Context, emailNormalized string) (domain.JoinRequest, error) {
	t := table.JoinRequests

	return r.one(ctx, t.EmailNormalized.EQ(jet.String(emailNormalized)).AND(t.DecidedAt.IS_NULL()))
}

func (r *JoinRequests) Get(ctx context.Context, id domain.JoinRequestID) (domain.JoinRequest, error) {
	rid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.JoinRequest{}, domain.ErrNotFound
	}

	return r.one(ctx, table.JoinRequests.ID.EQ(jet.UUID(rid)))
}

func (r *JoinRequests) one(ctx context.Context, where jet.BoolExpression) (domain.JoinRequest, error) {
	t := table.JoinRequests

	var m model.JoinRequests

	err := jet.SELECT(t.AllColumns).FROM(t).WHERE(where).LIMIT(1).QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.JoinRequest{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.JoinRequest{}, err
	}

	return toJoinRequest(m), nil
}

func (r *JoinRequests) DeniedSince(ctx context.Context, emailNormalized string, since time.Time) (bool, error) {
	t := table.JoinRequests

	var found []struct {
		ID uuid.UUID `alias:"join_requests.id"`
	}

	err := jet.SELECT(t.ID).
		FROM(t).
		WHERE(t.EmailNormalized.EQ(jet.String(emailNormalized)).
			AND(t.Approved.EQ(jet.Bool(false))).
			AND(t.DecidedAt.GT_EQ(jet.TimestampzT(since)))).
		LIMIT(1).
		QueryContext(ctx, r.db.conn(ctx), &found)

	return len(found) > 0, err
}

func (r *JoinRequests) Remail(ctx context.Context, id domain.JoinRequestID, tokenHash []byte, expires, at time.Time) error {
	rid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.ErrNotFound
	}

	t := table.JoinRequests

	return affectedOne(t.UPDATE(t.TokenHash, t.LinkExpiresAt, t.MailedAt, t.Mails).
		SET(jet.Bytea(tokenHash), jet.TimestampzT(expires), jet.TimestampzT(at), t.Mails.ADD(jet.Int(1))).
		WHERE(t.ID.EQ(jet.UUID(rid)).AND(t.ConfirmedAt.IS_NULL())).
		ExecContext(ctx, r.db.conn(ctx)))
}

// Confirm takes the link in a single conditional UPDATE, so it confirms once.
func (r *JoinRequests) Confirm(ctx context.Context, tokenHash []byte, at time.Time) (domain.JoinRequest, error) {
	t := table.JoinRequests

	var m model.JoinRequests

	err := t.UPDATE(t.ConfirmedAt, t.TokenHash).
		SET(jet.TimestampzT(at), jet.NULL).
		WHERE(t.TokenHash.EQ(jet.Bytea(tokenHash)).
			AND(t.ConfirmedAt.IS_NULL()).
			AND(t.DecidedAt.IS_NULL()).
			AND(t.LinkExpiresAt.GT(jet.TimestampzT(at)))).
		RETURNING(t.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.JoinRequest{}, domain.ErrInvalidLink
	}

	if err != nil {
		return domain.JoinRequest{}, err
	}

	return toJoinRequest(m), nil
}

func (r *JoinRequests) CountOpen(ctx context.Context) (int, error) {
	t := table.JoinRequests

	var out struct {
		Count int64
	}

	err := jet.SELECT(jet.COUNT(t.ID).AS("count")).
		FROM(t).
		WHERE(t.DecidedAt.IS_NULL()).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *JoinRequests) List(ctx context.Context, since time.Time) ([]domain.JoinRequest, error) {
	t := table.JoinRequests

	var rows []model.JoinRequests

	// Pending first, then decided; each newest first.
	err := jet.SELECT(t.AllColumns).
		FROM(t).
		WHERE(t.ConfirmedAt.IS_NOT_NULL().AND(t.DecidedAt.IS_NULL().OR(t.DecidedAt.GT_EQ(jet.TimestampzT(since))))).
		ORDER_BY(t.DecidedAt.IS_NULL().DESC(), t.ConfirmedAt.DESC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.JoinRequest, len(rows))
	for i, m := range rows {
		out[i] = toJoinRequest(m)
	}

	return out, nil
}

func (r *JoinRequests) Decide(ctx context.Context, id domain.JoinRequestID, approved bool, by domain.UserID, at time.Time, invite domain.InviteID) error {
	rid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.ErrNotFound
	}

	byID, err := uuid.Parse(string(by))
	if err != nil {
		return err
	}

	inviteID := jet.Expression(jet.NULL)

	if invite != "" {
		iid, err := uuid.Parse(string(invite))
		if err != nil {
			return err
		}

		inviteID = jet.UUID(iid)
	}

	t := table.JoinRequests

	return affectedOne(t.UPDATE(t.DecidedAt, t.DecidedBy, t.Approved, t.InviteID).
		SET(jet.TimestampzT(at), jet.UUID(byID), jet.Bool(approved), inviteID).
		WHERE(t.ID.EQ(jet.UUID(rid)).AND(t.ConfirmedAt.IS_NOT_NULL()).AND(t.DecidedAt.IS_NULL())).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *JoinRequests) PruneUnconfirmed(ctx context.Context, before time.Time) (int64, error) {
	t := table.JoinRequests

	res, err := t.DELETE().
		WHERE(t.ConfirmedAt.IS_NULL().AND(t.LinkExpiresAt.LT(jet.TimestampzT(before)))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

func toJoinRequest(m model.JoinRequests) domain.JoinRequest {
	out := domain.JoinRequest{
		ID:            domain.JoinRequestID(m.ID.String()),
		Email:         m.Email,
		Note:          m.Note,
		CreatedAt:     m.CreatedAt.UTC(),
		ConfirmedAt:   timeOf(m.ConfirmedAt),
		DecidedAt:     timeOf(m.DecidedAt),
		DecidedBy:     uuidString[domain.UserID](m.DecidedBy),
		InviteID:      uuidString[domain.InviteID](m.InviteID),
		LinkExpiresAt: m.LinkExpiresAt.UTC(),
		Mails:         int(m.Mails),
		MailedAt:      m.MailedAt.UTC(),
	}

	if m.Approved != nil {
		out.Approved = *m.Approved
	}

	return out
}
