package domain

import (
	"errors"
	"slices"
	"time"
)

// Agents sign in with OAuth 2.1 (D382, D397): a client is the agent's program, a grant is
// what a user allowed it, and its tokens act within that.

// ErrAgentsOff means an admin has switched agents over MCP off (D398): /mcp and agents'
// sign-in are not there.
var ErrAgentsOff = errors.New("agents over MCP are off on this instance")

type OAuthClientKind string

const (
	// ClientRegistered registered itself with the server (dynamic registration): what it
	// says of itself is unchecked.
	ClientRegistered OAuthClientKind = "registered"
	// ClientDocument is a metadata document at an https URL, its ID: the URL is checked,
	// as only its owner could publish there.
	ClientDocument OAuthClientKind = "document"
)

type OAuthClient struct {
	ID           string
	Kind         OAuthClientKind
	Name         string
	RedirectURIs []string
	ClientURI    string // its homepage, when it gives one
	CreatedAt    time.Time
	FetchedAt    time.Time // a document's, when it was last fetched
}

// Scope is what a grant lets an agent do (D384).
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeBuild Scope = "build"
	ScopePaper Scope = "paper"
	ScopeLive  Scope = "live"
	ScopeAdmin Scope = "admin"
)

// Scopes are every scope, in the order the consent page lists them.
var Scopes = []Scope{ScopeRead, ScopeBuild, ScopePaper, ScopeLive, ScopeAdmin}

func (s Scope) Valid() bool { return slices.Contains(Scopes, s) }

type GrantID string

// Grant is what a user allowed one client: its scopes, for one resource, until revoked.
type Grant struct {
	ID         GrantID
	UserID     UserID
	Client     OAuthClient
	Scopes     []Scope
	Resource   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

func (g Grant) Active() bool { return g.RevokedAt.IsZero() }

// Covers says the grant allows every one of scopes.
func (g Grant) Covers(scopes []Scope) bool {
	for _, s := range scopes {
		if !slices.Contains(g.Scopes, s) {
			return false
		}
	}

	return true
}

// AuthCode is an authorization code, as kept: for its grant, the redirect it was given to
// and the PKCE challenge its exchange must answer.
type AuthCode struct {
	GrantID     GrantID
	RedirectURI string
	Challenge   string
	Scopes      []Scope
	Resource    string
	ExpiresAt   time.Time
	UsedAt      time.Time
}

type OAuthTokenKind string

const (
	AccessToken  OAuthTokenKind = "access"
	RefreshToken OAuthTokenKind = "refresh"
)

// OAuthToken is an access or refresh token, as kept.
type OAuthToken struct {
	GrantID   GrantID
	Kind      OAuthTokenKind
	Scopes    []Scope
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}
