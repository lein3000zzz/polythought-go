package postgres

import (
	"context"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.OAuthRepository = (*OAuth)(nil)

// OAuth keeps the OAuth server's clients, grants, codes and tokens (D397).
type OAuth struct {
	db *DB
}

func NewOAuth(db *DB) *OAuth {
	return &OAuth{db: db}
}

func (r *OAuth) SaveClient(ctx context.Context, c domain.OAuthClient) error {
	t := table.OAuthClients
	m := model.OAuthClients{
		ID: c.ID, Kind: string(c.Kind), Name: c.Name, RedirectUris: pq.StringArray(c.RedirectURIs),
		ClientURI: c.ClientURI, CreatedAt: c.CreatedAt, FetchedAt: optional(c.FetchedAt),
	}

	_, err := t.INSERT(t.AllColumns).MODEL(m).
		ON_CONFLICT(t.ID).DO_UPDATE(jet.SET(
		t.Name.SET(t.EXCLUDED.Name),
		t.RedirectUris.SET(t.EXCLUDED.RedirectUris),
		t.ClientURI.SET(t.EXCLUDED.ClientURI),
		t.FetchedAt.SET(t.EXCLUDED.FetchedAt),
	)).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *OAuth) Client(ctx context.Context, id string) (domain.OAuthClient, error) {
	t := table.OAuthClients

	var m model.OAuthClients

	err := jet.SELECT(t.AllColumns).FROM(t).WHERE(t.ID.EQ(jet.String(id))).QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.OAuthClient{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.OAuthClient{}, err
	}

	return toOAuthClient(m), nil
}

func (r *OAuth) PruneClients(ctx context.Context, before time.Time) (int64, error) {
	c, g := table.OAuthClients, table.OAuthGrants

	res, err := c.DELETE().
		WHERE(c.Kind.EQ(jet.String(string(domain.ClientRegistered))).
			AND(c.CreatedAt.LT(jet.TimestampzT(before))).
			AND(jet.NOT(jet.EXISTS(jet.SELECT(g.ID).FROM(g).WHERE(g.ClientID.EQ(c.ID)))))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

func (r *OAuth) SaveGrant(ctx context.Context, gr domain.Grant) (domain.Grant, error) {
	id, err := uuid.Parse(string(gr.ID))
	if err != nil {
		return domain.Grant{}, err
	}

	user, err := uuid.Parse(string(gr.UserID))
	if err != nil {
		return domain.Grant{}, err
	}

	t := table.OAuthGrants
	m := model.OAuthGrants{
		ID: id, UserID: user, ClientID: gr.Client.ID, Scopes: scopesArray(gr.Scopes), Resource: gr.Resource,
		CreatedAt: gr.CreatedAt, UpdatedAt: gr.UpdatedAt,
	}

	// The user's grant to a client is one: allowing again replaces its scopes, and
	// revives it if it was revoked. A revoked grant's tokens are not the revived one's:
	// any given as it was revoked go now.
	var kept model.OAuthGrants

	err = r.db.WithinTx(ctx, func(ctx context.Context) error {
		var was model.OAuthGrants

		err := jet.SELECT(t.ID, t.RevokedAt).FROM(t).
			WHERE(t.UserID.EQ(jet.UUID(user)).AND(t.ClientID.EQ(jet.String(gr.Client.ID)))).
			FOR(jet.UPDATE()).
			QueryContext(ctx, r.db.conn(ctx), &was)

		switch {
		case errors.Is(err, qrm.ErrNoRows):
		case err != nil:
			return err
		case was.RevokedAt != nil:
			if err := r.dropIssued(ctx, was.ID); err != nil {
				return err
			}
		}

		return t.INSERT(t.ID, t.UserID, t.ClientID, t.Scopes, t.Resource, t.CreatedAt, t.UpdatedAt).MODEL(m).
			ON_CONFLICT(t.UserID, t.ClientID).DO_UPDATE(jet.SET(
			t.Scopes.SET(t.EXCLUDED.Scopes),
			t.Resource.SET(t.EXCLUDED.Resource),
			t.UpdatedAt.SET(t.EXCLUDED.UpdatedAt),
			t.RevokedAt.SET(jet.TimestampzExp(jet.NULL)),
		)).RETURNING(t.AllColumns).QueryContext(ctx, r.db.conn(ctx), &kept)
	})
	if err != nil {
		return domain.Grant{}, err
	}

	return r.Grant(ctx, domain.GrantID(kept.ID.String()))
}

type grantRow struct {
	model.OAuthGrants

	Client model.OAuthClients
}

func (r *OAuth) grants(ctx context.Context, where jet.BoolExpression) ([]domain.Grant, error) {
	g, c := table.OAuthGrants, table.OAuthClients

	var rows []grantRow

	err := jet.SELECT(g.AllColumns, c.AllColumns).
		FROM(g.INNER_JOIN(c, c.ID.EQ(g.ClientID))).
		WHERE(where).
		ORDER_BY(g.CreatedAt.DESC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Grant, len(rows))
	for i, row := range rows {
		out[i] = toGrant(row)
	}

	return out, nil
}

func (r *OAuth) Grant(ctx context.Context, id domain.GrantID) (domain.Grant, error) {
	gid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.Grant{}, domain.ErrNotFound
	}

	return r.oneGrant(ctx, table.OAuthGrants.ID.EQ(jet.UUID(gid)))
}

func (r *OAuth) GrantOf(ctx context.Context, user domain.UserID, client string) (domain.Grant, error) {
	uid, err := uuid.Parse(string(user))
	if err != nil {
		return domain.Grant{}, domain.ErrNotFound
	}

	g := table.OAuthGrants

	return r.oneGrant(ctx, g.UserID.EQ(jet.UUID(uid)).AND(g.ClientID.EQ(jet.String(client))))
}

func (r *OAuth) oneGrant(ctx context.Context, where jet.BoolExpression) (domain.Grant, error) {
	found, err := r.grants(ctx, where)
	if err != nil {
		return domain.Grant{}, err
	}

	if len(found) == 0 {
		return domain.Grant{}, domain.ErrNotFound
	}

	return found[0], nil
}

func (r *OAuth) Grants(ctx context.Context, user domain.UserID) ([]domain.Grant, error) {
	uid, err := uuid.Parse(string(user))
	if err != nil {
		return nil, nil
	}

	return r.grants(ctx, table.OAuthGrants.UserID.EQ(jet.UUID(uid)))
}

func (r *OAuth) RevokeGrant(ctx context.Context, user domain.UserID, id domain.GrantID, at time.Time) error {
	gid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.ErrNotFound
	}

	g := table.OAuthGrants
	where := g.ID.EQ(jet.UUID(gid))

	if user != "" {
		uid, err := uuid.Parse(string(user))
		if err != nil {
			return domain.ErrNotFound
		}

		where = where.AND(g.UserID.EQ(jet.UUID(uid)))
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		err := affectedOne(g.UPDATE(g.RevokedAt).
			SET(jet.COALESCE(g.RevokedAt, jet.TimestampzT(at))).
			WHERE(where).
			ExecContext(ctx, r.db.conn(ctx)))
		if err != nil {
			return err
		}

		// Its tokens and codes go with it: none works again.
		return r.dropIssued(ctx, gid)
	})
}

// dropIssued deletes a grant's codes and tokens.
func (r *OAuth) dropIssued(ctx context.Context, grant uuid.UUID) error {
	if _, err := table.OAuthTokens.DELETE().WHERE(table.OAuthTokens.GrantID.EQ(jet.UUID(grant))).ExecContext(ctx, r.db.conn(ctx)); err != nil {
		return err
	}

	_, err := table.OAuthCodes.DELETE().WHERE(table.OAuthCodes.GrantID.EQ(jet.UUID(grant))).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *OAuth) TouchGrant(ctx context.Context, id domain.GrantID, at time.Time) error {
	gid, err := uuid.Parse(string(id))
	if err != nil {
		return domain.ErrNotFound
	}

	g := table.OAuthGrants

	_, err = g.UPDATE(g.LastUsedAt).SET(jet.TimestampzT(at)).WHERE(g.ID.EQ(jet.UUID(gid))).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *OAuth) SaveCode(ctx context.Context, hash []byte, code domain.AuthCode) error {
	gid, err := uuid.Parse(string(code.GrantID))
	if err != nil {
		return err
	}

	t := table.OAuthCodes
	m := model.OAuthCodes{
		Hash: hash, GrantID: gid, RedirectURI: code.RedirectURI, Challenge: code.Challenge,
		Scopes: scopesArray(code.Scopes), Resource: code.Resource, ExpiresAt: code.ExpiresAt,
	}

	_, err = t.INSERT(t.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return err
}

// TakeCode marks the code used only if it was not, in one statement: of two exchanges at
// once, the second waits for the first and finds it used.
func (r *OAuth) TakeCode(ctx context.Context, hash []byte, at time.Time) (domain.AuthCode, error) {
	t := table.OAuthCodes

	var m model.OAuthCodes

	err := t.UPDATE(t.UsedAt).SET(jet.TimestampzT(at)).
		WHERE(t.Hash.EQ(jet.Bytea(hash)).AND(t.UsedAt.IS_NULL())).
		RETURNING(t.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err == nil {
		m.UsedAt = nil // unused until now

		return toAuthCode(m), nil
	}

	if !errors.Is(err, qrm.ErrNoRows) {
		return domain.AuthCode{}, err
	}

	// Used already, or none.
	err = jet.SELECT(t.AllColumns).FROM(t).WHERE(t.Hash.EQ(jet.Bytea(hash))).QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.AuthCode{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.AuthCode{}, err
	}

	return toAuthCode(m), nil
}

func (r *OAuth) SaveToken(ctx context.Context, hash []byte, tok domain.OAuthToken) error {
	gid, err := uuid.Parse(string(tok.GrantID))
	if err != nil {
		return err
	}

	t := table.OAuthTokens
	m := model.OAuthTokens{
		Hash: hash, GrantID: gid, Kind: string(tok.Kind), Scopes: scopesArray(tok.Scopes),
		CreatedAt: tok.CreatedAt, ExpiresAt: tok.ExpiresAt,
	}

	_, err = t.INSERT(t.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return err
}

// TakeRefresh marks a refresh token used only if it was not, in one statement, as
// TakeCode does: a token raced by two refreshes is rotated once, and the other finds it
// used.
func (r *OAuth) TakeRefresh(ctx context.Context, hash []byte, at time.Time) (domain.OAuthToken, error) {
	t := table.OAuthTokens
	refresh := t.Hash.EQ(jet.Bytea(hash)).AND(t.Kind.EQ(jet.String(string(domain.RefreshToken))))

	var m model.OAuthTokens

	err := t.UPDATE(t.UsedAt).SET(jet.TimestampzT(at)).
		WHERE(refresh.AND(t.UsedAt.IS_NULL())).
		RETURNING(t.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err == nil {
		m.UsedAt = nil // unused until now

		return toOAuthToken(m), nil
	}

	if !errors.Is(err, qrm.ErrNoRows) {
		return domain.OAuthToken{}, err
	}

	err = jet.SELECT(t.AllColumns).FROM(t).WHERE(refresh).QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.OAuthToken{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.OAuthToken{}, err
	}

	return toOAuthToken(m), nil
}

func (r *OAuth) Token(ctx context.Context, hash []byte) (domain.OAuthToken, error) {
	t := table.OAuthTokens

	var m model.OAuthTokens

	err := jet.SELECT(t.AllColumns).FROM(t).WHERE(t.Hash.EQ(jet.Bytea(hash))).QueryContext(ctx, r.db.conn(ctx), &m)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.OAuthToken{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.OAuthToken{}, err
	}

	return toOAuthToken(m), nil
}

func (r *OAuth) PruneTokens(ctx context.Context, before time.Time) (int64, error) {
	tokens, err := table.OAuthTokens.DELETE().
		WHERE(table.OAuthTokens.ExpiresAt.LT(jet.TimestampzT(before))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	codes, err := table.OAuthCodes.DELETE().
		WHERE(table.OAuthCodes.ExpiresAt.LT(jet.TimestampzT(before))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	a, _ := tokens.RowsAffected()
	b, _ := codes.RowsAffected()

	return a + b, nil
}

func toOAuthClient(m model.OAuthClients) domain.OAuthClient {
	return domain.OAuthClient{
		ID: m.ID, Kind: domain.OAuthClientKind(m.Kind), Name: m.Name, RedirectURIs: []string(m.RedirectUris),
		ClientURI: m.ClientURI, CreatedAt: m.CreatedAt.UTC(), FetchedAt: timeOf(m.FetchedAt),
	}
}

func toGrant(row grantRow) domain.Grant {
	return domain.Grant{
		ID: domain.GrantID(row.ID.String()), UserID: domain.UserID(row.UserID.String()), Client: toOAuthClient(row.Client),
		Scopes: toScopes(row.Scopes), Resource: row.Resource, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		LastUsedAt: timeOf(row.LastUsedAt), RevokedAt: timeOf(row.RevokedAt),
	}
}

func toAuthCode(m model.OAuthCodes) domain.AuthCode {
	return domain.AuthCode{
		GrantID: domain.GrantID(m.GrantID.String()), RedirectURI: m.RedirectURI, Challenge: m.Challenge,
		Scopes: toScopes(m.Scopes), Resource: m.Resource, ExpiresAt: m.ExpiresAt.UTC(), UsedAt: timeOf(m.UsedAt),
	}
}

func toOAuthToken(m model.OAuthTokens) domain.OAuthToken {
	return domain.OAuthToken{
		GrantID: domain.GrantID(m.GrantID.String()), Kind: domain.OAuthTokenKind(m.Kind), Scopes: toScopes(m.Scopes),
		CreatedAt: m.CreatedAt.UTC(), ExpiresAt: m.ExpiresAt.UTC(), UsedAt: timeOf(m.UsedAt),
	}
}

func scopesArray(scopes []domain.Scope) pq.StringArray {
	out := make(pq.StringArray, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}

	return out
}

func toScopes(a pq.StringArray) []domain.Scope {
	out := make([]domain.Scope, len(a))
	for i, s := range a {
		out[i] = domain.Scope(s)
	}

	return out
}

func optional(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}
