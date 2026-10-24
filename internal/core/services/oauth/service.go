// Package oauth is the OAuth 2.1 server agents sign in with (D382, D384, D397): clients
// register themselves or describe themselves in a metadata document; a user allows one
// scopes on the consent page, for the MCP endpoint; it exchanges the code, with PKCE, for
// an access token, an hour, and a refresh token, 30 days, rotated at each refresh. What
// admins set, on the Config page, is its Policy (D398).
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// The server's bounds.
const (
	codeLasts = 10 * time.Minute
	docFresh  = 24 * time.Hour  // a client's document is fetched again after this
	unusedFor = 24 * time.Hour  // a registered client no grant uses goes after this
	touchEach = 1 * time.Minute // how often a grant's last use is written, at most

	maxRedirects  = 10
	maxClientName = 100
	maxRedirect   = 2048
	maxClientURI  = 2048

	// A document not fetched is not tried again for a while, and only so many are kept so
	// (A12).
	failedFor  = 5 * time.Minute
	maxFailing = 1024

	accessPrefix  = "poa_" // an OAuth access token, told apart from an API token's pt_
	refreshPrefix = "por_"
)

type Config struct {
	// Issuer is the server's public URL, which the metadata and tokens name.
	Issuer string
	// Resource is the MCP endpoint's URL, the one resource tokens are for (RFC 8707).
	Resource string
}

// Policy is what admins set for agents (D398), read as each request comes.
type Policy struct {
	// Enabled off, agents' sign-in and /mcp are not there; grants stay.
	Enabled bool
	// Registration lets agents with no metadata document register themselves.
	Registration bool
	// SkipConsent lets an agent asking for what it has through without asking again.
	SkipConsent bool
	AccessTTL   time.Duration
	RefreshTTL  time.Duration
}

func DefaultPolicy() Policy {
	return Policy{Enabled: true, Registration: true, SkipConsent: true, AccessTTL: time.Hour, RefreshTTL: 30 * 24 * time.Hour}
}

type Service struct {
	cfg     Config
	repo    ports.OAuthRepository
	docs    ports.ClientDocuments
	users   ports.UserRepository
	limiter ports.RateLimiter
	log     logging.Logger
	now     func() time.Time
	policy  func() Policy

	mu        sync.Mutex
	unfetched map[string]time.Time // the documents not fetched lately, and when (A12)
}

type Option func(*Service)

// WithPolicy reads the policy from the settings as it changes.
func WithPolicy(policy func() Policy) Option {
	return func(s *Service) { s.policy = policy }
}

func New(cfg Config, repo ports.OAuthRepository, docs ports.ClientDocuments, users ports.UserRepository, limiter ports.RateLimiter, log logging.Logger, opts ...Option) *Service {
	cfg.Issuer = strings.TrimRight(cfg.Issuer, "/")

	s := &Service{
		cfg: cfg, repo: repo, docs: docs, users: users, limiter: limiter,
		log: log.With("component", "oauth"),
		// At the database's precision, so what it gives equals what is read back.
		now:       func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		policy:    DefaultPolicy,
		unfetched: map[string]time.Time{},
	}

	for _, o := range opts {
		o(s)
	}

	return s
}

// Enabled says agents over MCP are on (D398).
func (s *Service) Enabled() bool { return s.policy().Enabled }

// Error is an OAuth error, said to the client as RFC 6749 says: its code and a
// description. Redirect, when set, is where the browser goes with it: a request whose
// client or redirect is wrong is never sent back to that redirect.
type Error struct {
	Code        string
	Description string
	Redirect    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

func oauthErr(code, description string) *Error { return &Error{Code: code, Description: description} }

// Metadata is the authorization server's, as RFC 8414 has it.
type Metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	RevocationEndpoint    string   `json:"revocation_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
	ResponseTypes         []string `json:"response_types_supported"`
	GrantTypes            []string `json:"grant_types_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	RevocationAuthMethods []string `json:"revocation_endpoint_auth_methods_supported"`
	ClientIDDocuments     bool     `json:"client_id_metadata_document_supported"`
	AuthorizationIssuer   bool     `json:"authorization_response_iss_parameter_supported"`
	Documentation         string   `json:"service_documentation"`
}

// Metadata is the authorization server's; with registration off, it names no registration
// endpoint, so agents use a metadata document.
func (s *Service) Metadata() Metadata {
	m := Metadata{
		Issuer:                s.cfg.Issuer,
		AuthorizationEndpoint: s.cfg.Issuer + "/oauth/authorize",
		TokenEndpoint:         s.cfg.Issuer + "/api/v1/oauth/token",
		RevocationEndpoint:    s.cfg.Issuer + "/api/v1/oauth/revoke",
		ScopesSupported:       allScopes(),
		ResponseTypes:         []string{"code"},
		GrantTypes:            []string{"authorization_code", "refresh_token"},
		CodeChallengeMethods:  []string{"S256"},
		TokenAuthMethods:      []string{"none"},
		RevocationAuthMethods: []string{"none"},
		ClientIDDocuments:     true,
		AuthorizationIssuer:   true,
		Documentation:         s.cfg.Issuer + "/docs/",
	}

	if s.policy().Registration {
		m.RegistrationEndpoint = s.cfg.Issuer + "/api/v1/oauth/register"
	}

	return m
}

// ResourceMetadata is the MCP endpoint's, as RFC 9728 has it: where its tokens come from.
type ResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
	ResourceDocumentation  string   `json:"resource_documentation"`
}

func (s *Service) ResourceMetadata() ResourceMetadata {
	return ResourceMetadata{
		Resource:               s.cfg.Resource,
		AuthorizationServers:   []string{s.cfg.Issuer},
		ScopesSupported:        allScopes(),
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Polythought",
		ResourceDocumentation:  s.cfg.Issuer + "/docs/",
	}
}

func allScopes() []string { return scopeStrings(domain.Scopes) }

// Registration is what a client says of itself to register (RFC 7591).
type Registration struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientURI               string   `json:"client_uri"`
}

// Register registers a client as it describes itself. Every client is public: none gets a
// secret, as an agent on someone's machine could not keep one; PKCE stands in for it.
func (s *Service) Register(ctx context.Context, reg Registration, ip string) (domain.OAuthClient, error) {
	switch policy := s.policy(); {
	case !policy.Enabled:
		return domain.OAuthClient{}, domain.ErrAgentsOff
	case !policy.Registration:
		return domain.OAuthClient{}, oauthErr("access_denied", "agents do not register themselves here: use a client ID metadata document")
	}

	if !s.limiter.Allow("oauth-register:ip:" + ip) {
		return domain.OAuthClient{}, domain.ErrRateLimited
	}

	if len(reg.GrantTypes) > 0 && !slices.Contains(reg.GrantTypes, "authorization_code") {
		return domain.OAuthClient{}, oauthErr("invalid_client_metadata", "a client here uses the authorization_code grant")
	}

	if len(reg.ResponseTypes) > 0 && !slices.Equal(reg.ResponseTypes, []string{"code"}) {
		return domain.OAuthClient{}, oauthErr("invalid_client_metadata", "a client here uses the code response type")
	}

	c, err := clientOf("", domain.ClientRegistered, reg.ClientName, reg.RedirectURIs, reg.ClientURI)
	if err != nil {
		return domain.OAuthClient{}, err
	}

	if c.ID, err = secret("pc_"); err != nil {
		return domain.OAuthClient{}, err
	}

	c.CreatedAt = s.now()

	if err := s.repo.SaveClient(ctx, c); err != nil {
		return domain.OAuthClient{}, err
	}

	s.log.Info(ctx, "OAuth client registered", "client_id", c.ID, "name", c.Name, "ip", ip)

	return c, nil
}

// document is the part of a client's metadata document read here.
type document struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	ClientURI    string   `json:"client_uri"`
}

// client is the client of an ID: a registered one, or the document at its URL, fetched
// for who asks when it was not, or long ago.
func (s *Service) client(ctx context.Context, who domain.UserID, id string) (domain.OAuthClient, error) {
	kept, keptErr := s.repo.Client(ctx, id)

	switch {
	case keptErr == nil && (kept.Kind == domain.ClientRegistered || s.now().Sub(kept.FetchedAt) < docFresh):
		return kept, nil
	case keptErr != nil && !errors.Is(keptErr, domain.ErrNotFound):
		return domain.OAuthClient{}, keptErr
	case !strings.HasPrefix(id, "https://"):
		return domain.OAuthClient{}, oauthErr("invalid_client", "no such client")
	}

	if err := s.mayFetch(id, who); err != nil {
		return domain.OAuthClient{}, err
	}

	body, err := s.docs.Fetch(ctx, id)
	if err != nil {
		s.fetchFailed(id)
		s.log.Warn(ctx, "OAuth client's document not fetched", "client_id", id, logging.Err(err))

		return domain.OAuthClient{}, oauthErr("invalid_client", "the client's document could not be fetched")
	}

	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		return domain.OAuthClient{}, oauthErr("invalid_client", "the client's document is not JSON")
	}

	if doc.ClientID != id {
		return domain.OAuthClient{}, oauthErr("invalid_client", "the client's document names another client")
	}

	c, err := clientOf(id, domain.ClientDocument, doc.ClientName, doc.RedirectURIs, doc.ClientURI)
	if err != nil {
		return domain.OAuthClient{}, err
	}

	c.CreatedAt, c.FetchedAt = s.now(), s.now()
	if keptErr == nil {
		c.CreatedAt = kept.CreatedAt // fetched again: still the client it was
	}

	if err := s.repo.SaveClient(ctx, c); err != nil {
		return domain.OAuthClient{}, err
	}

	return c, nil
}

// mayFetch refuses to fetch a document that failed lately, or one more for a user, or from
// a host, past the limit (A12): each fetch costs the server a connection, and a new
// client a row.
func (s *Service) mayFetch(id string, who domain.UserID) error {
	s.mu.Lock()
	at, failed := s.unfetched[id]
	s.mu.Unlock()

	if failed && s.now().Sub(at) < failedFor {
		return oauthErr("invalid_client", "the client's document could not be fetched a moment ago: try again in a few minutes")
	}

	host := id
	if u, err := url.Parse(id); err == nil {
		host = u.Hostname()
	}

	if !s.limiter.Allow("oauth-fetch:user:"+string(who)) || !s.limiter.Allow("oauth-fetch:host:"+host) {
		return fmt.Errorf("%w: too many agents' documents fetched lately; try again in a minute", domain.ErrRateLimited)
	}

	return nil
}

// fetchFailed keeps that id's document was not fetched, for a while.
func (s *Service) fetchFailed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()

	if len(s.unfetched) >= maxFailing {
		for k, at := range s.unfetched {
			if now.Sub(at) >= failedFor {
				delete(s.unfetched, k)
			}
		}
	}

	if len(s.unfetched) < maxFailing {
		s.unfetched[id] = now
	}
}

// clientOf is a client as it describes itself, checked: a name, kept to plain text, and
// redirects of the kinds allowed.
func clientOf(id string, kind domain.OAuthClientKind, name string, redirects []string, uri string) (domain.OAuthClient, error) {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}

		return r
	}, strings.TrimSpace(name))

	if name == "" {
		name = "An unnamed agent"
	}

	if len([]rune(name)) > maxClientName {
		name = string([]rune(name)[:maxClientName])
	}

	if len(redirects) == 0 || len(redirects) > maxRedirects {
		return domain.OAuthClient{}, oauthErr("invalid_redirect_uri", fmt.Sprintf("a client has 1 to %d redirect URIs", maxRedirects))
	}

	for _, r := range redirects {
		if err := allowedRedirect(r); err != nil {
			return domain.OAuthClient{}, oauthErr("invalid_redirect_uri", err.Error())
		}
	}

	// A home page too long to be one is left out, as one not https is (A13).
	if u, err := url.Parse(uri); uri != "" && (err != nil || u.Scheme != "https" || len(uri) > maxClientURI) {
		uri = ""
	}

	return domain.OAuthClient{ID: id, Kind: kind, Name: name, RedirectURIs: redirects, ClientURI: uri}, nil
}

// Schemes a redirect may never use: they run or read things in the browser itself.
var refusedSchemes = []string{"javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss", "mailto"}

// allowedRedirect says what is wrong with a redirect URI, if anything: https; http to the
// machine itself, as a CLI agent listens there (RFC 8252); or an app's own scheme.
func allowedRedirect(raw string) error {
	if len(raw) > maxRedirect {
		return errors.New("a redirect URI is too long")
	}

	u, err := url.Parse(raw)

	switch {
	case err != nil || u.Scheme == "" || u.Fragment != "" || u.User != nil:
		return fmt.Errorf("%q is not a redirect URI", raw)
	case u.Scheme == "https":
		if u.Host == "" {
			return fmt.Errorf("%q has no host", raw)
		}
	case u.Scheme == "http":
		if !loopback(u.Hostname()) {
			return fmt.Errorf("%q: http only to this machine, 127.0.0.1, [::1] or localhost", raw)
		}
	case slices.Contains(refusedSchemes, strings.ToLower(u.Scheme)):
		return fmt.Errorf("%q: no %s redirects", raw, u.Scheme)
	}

	return nil
}

func loopback(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// matchesRedirect says the redirect asked for is one the client registered: the same, or,
// to the machine itself over http, the same but for its port, which a CLI agent picks as
// it starts (RFC 8252 7.3).
func matchesRedirect(c domain.OAuthClient, asked string) bool {
	if slices.Contains(c.RedirectURIs, asked) {
		return true
	}

	a, err := url.Parse(asked)
	if err != nil || a.Scheme != "http" || !loopback(a.Hostname()) {
		return false
	}

	for _, r := range c.RedirectURIs {
		u, err := url.Parse(r)
		if err == nil && u.Scheme == "http" && u.Hostname() == a.Hostname() && u.Path == a.Path && u.RawQuery == a.RawQuery {
			return true
		}
	}

	return false
}

// secret is a random 256-bit secret, as base64url, after prefix.
func secret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: random: %w", err)
	}

	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// hash is how codes and tokens are kept: the secrets are random, so a fast hash is enough.
func hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// challenge is a PKCE S256 challenge: 32 bytes as base64url, unpadded.
var challenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// verifier is a PKCE code verifier (RFC 7636 4.1).
var verifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func newID() string { return uuid.Must(uuid.NewV7()).String() }
