package postgres

import (
	"context"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var (
	_ ports.APITokenRepository = (*APITokens)(nil)
	_ ports.InviteRepository   = (*Invites)(nil)
)

type APITokens struct {
	db *DB
}

func NewAPITokens(db *DB) *APITokens {
	return &APITokens{db: db}
}

func (r *APITokens) Create(ctx context.Context, t domain.APIToken, tokenHash []byte) error {
	id, err := uuid.Parse(string(t.ID))
	if err != nil {
		return err
	}

	userID, err := uuid.Parse(string(t.UserID))
	if err != nil {
		return err
	}

	_, err = table.APITokens.INSERT(table.APITokens.AllColumns).MODEL(model.APITokens{
		ID:         id,
		UserID:     userID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		TokenHash:  tokenHash,
		CreatedAt:  t.CreatedAt,
		ExpiresAt:  timePtr(t.ExpiresAt),
		LastUsedAt: timePtr(t.LastUsedAt),
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *APITokens) ByTokenHash(ctx context.Context, tokenHash []byte) (domain.APIToken, error) {
	var m model.APITokens

	err := jet.SELECT(table.APITokens.AllColumns).
		FROM(table.APITokens).
		WHERE(table.APITokens.TokenHash.EQ(jet.Bytea(tokenHash))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.APIToken{}, notFound(err)
	}

	return toAPIToken(m), nil
}

func (r *APITokens) ListForUser(ctx context.Context, userID domain.UserID) ([]domain.APIToken, error) {
	uid, err := parseID(userID)
	if err != nil {
		return nil, err
	}

	var rows []model.APITokens

	err = jet.SELECT(table.APITokens.AllColumns).
		FROM(table.APITokens).
		WHERE(table.APITokens.UserID.EQ(jet.UUID(uid))).
		ORDER_BY(table.APITokens.ID.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.APIToken, len(rows))
	for i, m := range rows {
		out[i] = toAPIToken(m)
	}

	return out, nil
}

func (r *APITokens) Delete(ctx context.Context, userID domain.UserID, id domain.APITokenID) error {
	uid, err := parseID(userID)
	if err != nil {
		return err
	}

	tid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.APITokens.DELETE().
		WHERE(table.APITokens.ID.EQ(jet.UUID(tid)).AND(table.APITokens.UserID.EQ(jet.UUID(uid)))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *APITokens) TouchLastUsed(ctx context.Context, id domain.APITokenID, at time.Time) error {
	tid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.APITokens.
		UPDATE(table.APITokens.LastUsedAt).
		SET(at).
		WHERE(table.APITokens.ID.EQ(jet.UUID(tid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func toAPIToken(m model.APITokens) domain.APIToken {
	return domain.APIToken{
		ID:         domain.APITokenID(m.ID.String()),
		UserID:     domain.UserID(m.UserID.String()),
		Name:       m.Name,
		Prefix:     m.Prefix,
		CreatedAt:  m.CreatedAt.UTC(),
		ExpiresAt:  timeOf(m.ExpiresAt),
		LastUsedAt: timeOf(m.LastUsedAt),
	}
}

type Invites struct {
	db *DB
}

func NewInvites(db *DB) *Invites {
	return &Invites{db: db}
}

func (r *Invites) Create(ctx context.Context, inv domain.Invite, codeHash []byte) error {
	id, err := uuid.Parse(string(inv.ID))
	if err != nil {
		return err
	}

	m := model.Invites{ID: id, CodeHash: codeHash, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt}

	if inv.CreatedBy != "" {
		by, err := uuid.Parse(string(inv.CreatedBy))
		if err != nil {
			return err
		}

		m.CreatedBy = &by
	}

	_, err = table.Invites.INSERT(table.Invites.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Invites) List(ctx context.Context) ([]domain.Invite, error) {
	var rows []model.Invites

	err := jet.SELECT(table.Invites.AllColumns).
		FROM(table.Invites).
		ORDER_BY(table.Invites.ID.DESC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Invite, len(rows))
	for i, m := range rows {
		out[i] = domain.Invite{
			ID:        domain.InviteID(m.ID.String()),
			CreatedBy: uuidString[domain.UserID](m.CreatedBy),
			CreatedAt: m.CreatedAt.UTC(),
			ExpiresAt: m.ExpiresAt.UTC(),
			UsedBy:    uuidString[domain.UserID](m.UsedBy),
			UsedAt:    timeOf(m.UsedAt),
		}
	}

	return out, nil
}

// Consume claims the invite in a single conditional UPDATE, so two registrations racing
// for one code cannot both succeed.
func (r *Invites) Consume(ctx context.Context, codeHash []byte, userID domain.UserID, at time.Time) error {
	uid, err := uuid.Parse(string(userID))
	if err != nil {
		return err
	}

	err = affectedOne(table.Invites.
		UPDATE(table.Invites.UsedBy, table.Invites.UsedAt).
		SET(jet.UUID(uid), jet.TimestampzT(at)).
		WHERE(table.Invites.CodeHash.EQ(jet.Bytea(codeHash)).
			AND(table.Invites.UsedAt.IS_NULL()).
			AND(table.Invites.ExpiresAt.GT(jet.TimestampzT(at)))).
		ExecContext(ctx, r.db.conn(ctx)))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrInvalidInvite
	}

	return err
}

func uuidString[T ~string](u *uuid.UUID) T {
	if u == nil {
		return ""
	}

	return T(u.String())
}
