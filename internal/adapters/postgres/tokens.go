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

const invitesCodeKey = "invites_code_hash_key"

func (r *Invites) Create(ctx context.Context, inv domain.Invite, codeHash, sealed []byte) error {
	id, err := uuid.Parse(string(inv.ID))
	if err != nil {
		return err
	}

	m := model.Invites{
		ID: id, CodeHash: codeHash, Email: inv.Email, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt,
		MaxUses: int32(inv.MaxUses), //nolint:gosec // 1 to 1000, checked by the service and the table
	}

	if sealed != nil {
		m.CodeSealed = &sealed
	}

	if inv.CreatedBy != "" {
		by, err := uuid.Parse(string(inv.CreatedBy))
		if err != nil {
			return err
		}

		m.CreatedBy = &by
	}

	_, err = table.Invites.INSERT(table.Invites.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))
	if isUniqueViolation(err, invitesCodeKey) {
		return domain.ErrInviteCodeTaken
	}

	return err
}

func (r *Invites) List(ctx context.Context) ([]ports.StoredInvite, error) {
	var rows []model.Invites

	err := jet.SELECT(table.Invites.AllColumns).
		FROM(table.Invites).
		ORDER_BY(table.Invites.CreatedAt.DESC(), table.Invites.ID.DESC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	var uses []struct {
		model.InviteUses

		Email *string `alias:"users.email"`
	}

	u := table.InviteUses
	err = jet.SELECT(u.AllColumns, table.Users.Email).
		FROM(u.LEFT_JOIN(table.Users, table.Users.ID.EQ(u.UserID))).
		ORDER_BY(u.UsedAt.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &uses)
	if err != nil {
		return nil, err
	}

	signups := map[uuid.UUID][]domain.InviteUse{}
	for _, use := range uses {
		signup := domain.InviteUse{UserID: uuidString[domain.UserID](use.UserID), At: use.UsedAt.UTC()}
		if use.Email != nil {
			signup.Email = *use.Email
		}

		signups[use.InviteID] = append(signups[use.InviteID], signup)
	}

	out := make([]ports.StoredInvite, len(rows))
	for i, m := range rows {
		out[i] = ports.StoredInvite{Invite: toInvite(m)}
		out[i].Signups = signups[m.ID]

		if m.CodeSealed != nil {
			out[i].Sealed = *m.CodeSealed
		}
	}

	return out, nil
}

func (r *Invites) Taken(ctx context.Context, codeHashes [][]byte) (bool, error) {
	var found []struct {
		ID uuid.UUID `alias:"invites.id"`
	}

	err := jet.SELECT(table.Invites.ID).
		FROM(table.Invites).
		WHERE(table.Invites.CodeHash.IN(byteas(codeHashes)...)).
		LIMIT(1).
		QueryContext(ctx, r.db.conn(ctx), &found)

	return len(found) > 0, err
}

// openInvite is an invite still admitting sign-ups at at.
func openInvite(t *table.InvitesTable, at time.Time) jet.BoolExpression {
	return t.RevokedAt.IS_NULL().AND(t.ExpiresAt.GT(jet.TimestampzT(at))).AND(t.Uses.LT(t.MaxUses))
}

func (r *Invites) ByCode(ctx context.Context, codeHashes [][]byte, at time.Time) (domain.Invite, error) {
	t := table.Invites

	var m model.Invites

	err := jet.SELECT(t.AllColumns).
		FROM(t).
		WHERE(t.CodeHash.IN(byteas(codeHashes)...).AND(openInvite(t, at))).
		LIMIT(1).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.Invite{}, domain.ErrInvalidInvite
	}

	if err != nil {
		return domain.Invite{}, err
	}

	return toInvite(m), nil
}

func toInvite(m model.Invites) domain.Invite {
	return domain.Invite{
		ID:        domain.InviteID(m.ID.String()),
		CreatedBy: uuidString[domain.UserID](m.CreatedBy),
		Email:     m.Email,
		CreatedAt: m.CreatedAt.UTC(),
		ExpiresAt: m.ExpiresAt.UTC(),
		MaxUses:   int(m.MaxUses),
		Used:      int(m.Uses),
		RevokedAt: timeOf(m.RevokedAt),
		RevokedBy: uuidString[domain.UserID](m.RevokedBy),
	}
}

// Consume takes a use in a single conditional UPDATE, so registrations racing for an
// invite's last use cannot both have it, then keeps who took it.
func (r *Invites) Consume(ctx context.Context, id domain.InviteID, userID domain.UserID, at time.Time) error {
	iid, err := uuid.Parse(string(id))
	if err != nil {
		return err
	}

	uid, err := uuid.Parse(string(userID))
	if err != nil {
		return err
	}

	t := table.Invites

	err = affectedOne(t.UPDATE(t.Uses).
		SET(t.Uses.ADD(jet.Int(1))).
		WHERE(t.ID.EQ(jet.UUID(iid)).AND(openInvite(t, at))).
		ExecContext(ctx, r.db.conn(ctx)))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrInvalidInvite
	}

	if err != nil {
		return err
	}

	_, err = table.InviteUses.INSERT(table.InviteUses.AllColumns).
		MODEL(model.InviteUses{InviteID: iid, UserID: &uid, UsedAt: at}).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

// Revoke ends an invite; one revoked before keeps when and by whom it was.
func (r *Invites) Revoke(ctx context.Context, id domain.InviteID, by domain.UserID, at time.Time) error {
	iid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.ErrNotFound
	}

	byID, err := uuid.Parse(string(by))
	if err != nil {
		return err
	}

	t := table.Invites

	return affectedOne(t.UPDATE(t.RevokedAt, t.RevokedBy).
		SET(jet.COALESCE(t.RevokedAt, jet.TimestampzT(at)), jet.COALESCE(t.RevokedBy, jet.UUID(byID))).
		WHERE(t.ID.EQ(jet.UUID(iid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func byteas(hashes [][]byte) []jet.Expression {
	out := make([]jet.Expression, len(hashes))
	for i, h := range hashes {
		out[i] = jet.Bytea(h)
	}

	return out
}

func uuidString[T ~string](u *uuid.UUID) T {
	if u == nil {
		return ""
	}

	return T(u.String())
}
