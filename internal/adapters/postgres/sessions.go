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

var _ ports.SessionRepository = (*Sessions)(nil)

type Sessions struct {
	db *DB
}

func NewSessions(db *DB) *Sessions {
	return &Sessions{db: db}
}

func (r *Sessions) Create(ctx context.Context, s domain.Session, tokenHash []byte) error {
	id, err := uuid.Parse(string(s.ID))
	if err != nil {
		return err
	}

	userID, err := uuid.Parse(string(s.UserID))
	if err != nil {
		return err
	}

	_, err = table.Sessions.INSERT(table.Sessions.AllColumns).MODEL(model.Sessions{
		ID:                id,
		UserID:            userID,
		TokenHash:         tokenHash,
		UserAgent:         s.UserAgent,
		IP:                s.IP,
		CreatedAt:         s.CreatedAt,
		LastSeenAt:        s.LastSeenAt,
		ExpiresAt:         s.ExpiresAt,
		AbsoluteExpiresAt: s.AbsoluteExpiresAt,
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Sessions) ByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error) {
	var m model.Sessions

	err := jet.SELECT(table.Sessions.AllColumns).
		FROM(table.Sessions).
		WHERE(table.Sessions.TokenHash.EQ(jet.Bytea(tokenHash))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.Session{}, notFound(err)
	}

	return domain.Session{
		ID:                domain.SessionID(m.ID.String()),
		UserID:            domain.UserID(m.UserID.String()),
		UserAgent:         m.UserAgent,
		IP:                m.IP,
		CreatedAt:         m.CreatedAt.UTC(),
		LastSeenAt:        m.LastSeenAt.UTC(),
		ExpiresAt:         m.ExpiresAt.UTC(),
		AbsoluteExpiresAt: m.AbsoluteExpiresAt.UTC(),
	}, nil
}

func (r *Sessions) Touch(ctx context.Context, id domain.SessionID, lastSeen, expiresAt time.Time) error {
	sid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.Sessions.
		UPDATE(table.Sessions.LastSeenAt, table.Sessions.ExpiresAt).
		SET(lastSeen, expiresAt).
		WHERE(table.Sessions.ID.EQ(jet.UUID(sid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Sessions) Delete(ctx context.Context, id domain.SessionID) error {
	sid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.Sessions.DELETE().WHERE(table.Sessions.ID.EQ(jet.UUID(sid))).ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Sessions) DeleteForUser(ctx context.Context, userID domain.UserID, keep domain.SessionID) error {
	uid, err := parseID(userID)
	if err != nil {
		return err
	}

	where := table.Sessions.UserID.EQ(jet.UUID(uid))

	if keep != "" {
		keepID, err := parseID(keep)
		if err != nil {
			return err
		}

		where = where.AND(table.Sessions.ID.NOT_EQ(jet.UUID(keepID)))
	}

	_, err = table.Sessions.DELETE().WHERE(where).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Sessions) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := table.Sessions.DELETE().
		WHERE(table.Sessions.ExpiresAt.LT_EQ(jet.TimestampzT(now)).OR(table.Sessions.AbsoluteExpiresAt.LT_EQ(jet.TimestampzT(now)))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
