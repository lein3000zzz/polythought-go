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

var _ ports.EmailTokenRepository = (*EmailTokens)(nil)

type EmailTokens struct {
	db *DB
}

func NewEmailTokens(db *DB) *EmailTokens {
	return &EmailTokens{db: db}
}

func (r *EmailTokens) Create(ctx context.Context, t domain.EmailToken, tokenHash []byte) error {
	uid, err := parseID(t.UserID)
	if err != nil {
		return err
	}

	_, err = table.EmailTokens.INSERT(table.EmailTokens.AllColumns).MODEL(model.EmailTokens{
		TokenHash: tokenHash, UserID: uid, Purpose: string(t.Purpose), CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *EmailTokens) Take(ctx context.Context, tokenHash []byte, purpose domain.EmailTokenPurpose, now time.Time) (domain.UserID, error) {
	t := table.EmailTokens

	var m model.EmailTokens

	// One statement: a link is used once, however many open it at the same time.
	err := t.DELETE().
		WHERE(t.TokenHash.EQ(jet.Bytea(tokenHash)).
			AND(t.Purpose.EQ(jet.String(string(purpose)))).
			AND(t.ExpiresAt.GT(jet.TimestampzT(now)))).
		RETURNING(t.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return "", notFound(err)
	}

	return domain.UserID(m.UserID.String()), nil
}

func (r *EmailTokens) CountSince(ctx context.Context, user domain.UserID, purpose domain.EmailTokenPurpose, since time.Time) (int, error) {
	uid, err := parseID(user)
	if err != nil {
		return 0, err
	}

	t := table.EmailTokens

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(t).
		WHERE(t.UserID.EQ(jet.UUID(uid)).AND(t.Purpose.EQ(jet.String(string(purpose)))).AND(t.CreatedAt.GT_EQ(jet.TimestampzT(since)))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *EmailTokens) DeleteForUser(ctx context.Context, user domain.UserID, purpose domain.EmailTokenPurpose) error {
	uid, err := parseID(user)
	if err != nil {
		return err
	}

	t := table.EmailTokens

	_, err = t.DELETE().
		WHERE(t.UserID.EQ(jet.UUID(uid)).AND(t.Purpose.EQ(jet.String(string(purpose))))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *EmailTokens) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	t := table.EmailTokens

	res, err := t.DELETE().WHERE(t.ExpiresAt.LT_EQ(jet.TimestampzT(now))).ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
