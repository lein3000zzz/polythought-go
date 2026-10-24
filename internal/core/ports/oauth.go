package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// OAuthRepository keeps the OAuth server's clients, grants, codes and tokens (D397).
// Codes and tokens are found by their hashes.
type OAuthRepository interface {
	// SaveClient keeps a client, replacing the one of its ID: a document fetched again.
	SaveClient(ctx context.Context, c domain.OAuthClient) error
	// Client is a client; domain.ErrNotFound when there is none.
	Client(ctx context.Context, id string) (domain.OAuthClient, error)
	// PruneClients removes the clients no grant uses: registered ones made before before,
	// and documents' fetched before it.
	PruneClients(ctx context.Context, before time.Time) (int64, error)

	// SaveGrant keeps the user's grant to its client, replacing their last one with its
	// scopes, active again; it gives the grant as kept.
	SaveGrant(ctx context.Context, g domain.Grant) (domain.Grant, error)
	// Grant is a grant, with its client; domain.ErrNotFound when there is none.
	Grant(ctx context.Context, id domain.GrantID) (domain.Grant, error)
	// GrantOf is the user's grant to a client; domain.ErrNotFound when there is none.
	GrantOf(ctx context.Context, user domain.UserID, client string) (domain.Grant, error)
	// Grants are the user's, with their clients, newest first.
	Grants(ctx context.Context, user domain.UserID) ([]domain.Grant, error)
	// RevokeGrant ends a grant at at and drops its codes and tokens; with user set, only
	// that user's. domain.ErrNotFound when there is none.
	RevokeGrant(ctx context.Context, user domain.UserID, id domain.GrantID, at time.Time) error
	// TouchGrant notes the grant was used at at.
	TouchGrant(ctx context.Context, id domain.GrantID, at time.Time) error

	SaveCode(ctx context.Context, hash []byte, code domain.AuthCode) error
	// TakeCode marks a code used at at and gives it as it was, used before or not;
	// domain.ErrNotFound when there is none.
	TakeCode(ctx context.Context, hash []byte, at time.Time) (domain.AuthCode, error)

	SaveToken(ctx context.Context, hash []byte, t domain.OAuthToken) error
	// TakeRefresh marks a refresh token used at at and gives it as it was, used before or
	// not; domain.ErrNotFound when there is none.
	TakeRefresh(ctx context.Context, hash []byte, at time.Time) (domain.OAuthToken, error)
	// Token is a token, of either kind; domain.ErrNotFound when there is none.
	Token(ctx context.Context, hash []byte) (domain.OAuthToken, error)
	// PruneTokens removes the codes and tokens that ended before before.
	PruneTokens(ctx context.Context, before time.Time) (int64, error)
}

// ClientDocuments fetches an OAuth client's metadata document from its https URL, through
// the guard that keeps out the server's own network (D397).
type ClientDocuments interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}
