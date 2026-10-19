package polymarket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// DefaultRelayerURL is Polymarket's Relayer, which submits Deposit Wallet batches.
const DefaultRelayerURL = "https://relayer-v2.polymarket.com"

// defaultRelayerRPS keeps well under the Relayer's 25 a minute on /submit.
const defaultRelayerRPS = 1

const (
	opExecuteParams = "execute_params"
	opAuthorize     = "authorize_session_signer"
	opRevoke        = "revoke_session_signer"
	opTransaction   = "transaction"
	opSubmit        = "submit"
	opDeployed      = "deployed"
)

// RelayerClient submits smart wallets' transactions through the Relayer, and authorises
// and revokes session keys, with the operator's Builder key (D156, D161).
type RelayerClient interface {
	// ExecuteParams are what signer's next transaction of kind typ (RelayerDepositWallet,
	// RelayerSafe or RelayerProxy) takes.
	ExecuteParams(ctx context.Context, auth BuilderCredentials, signer, typ string) (ExecuteParams, error)
	// Submit sends a signed gasless transaction, once: it is not retried, as it has no
	// idempotency key.
	Submit(ctx context.Context, auth BuilderCredentials, req SubmitRequest) (RelayerSubmission, error)
	// Deployed says whether a smart wallet is deployed; depositWallet for a Deposit Wallet.
	Deployed(ctx context.Context, auth BuilderCredentials, wallet string, depositWallet bool) (bool, error)
	// AuthorizeSessionSigner submits the owner's signed batch authorising a session key.
	AuthorizeSessionSigner(ctx context.Context, auth BuilderCredentials, req SessionSignerRequest) (RelayerOperation, error)
	// RevokeSessionSigner submits the owner's signed batch revoking one.
	RevokeSessionSigner(ctx context.Context, auth BuilderCredentials, req SessionSignerRequest) (RelayerOperation, error)
	// Transaction is how a submitted transaction is doing.
	Transaction(ctx context.Context, auth BuilderCredentials, id string) (RelayerTransaction, error)
}

var _ RelayerClient = (*DefaultRelayerClient)(nil)

// SessionSignerRequest is a signed batch for a session key, as the Relayer takes it:
// ValidUntil and Scopes for an authorisation only. IdempotencyKey makes a retry of the
// same batch harmless.
type SessionSignerRequest struct {
	Wallet, SessionSigner string
	Scopes                []string
	ValidUntil            int64
	Nonce                 string
	Deadline              int64
	Signature             string
	IdempotencyKey        string
}

// ExecuteParams are a signer's next transaction's: the Relayer's address, which a proxy
// wallet's signature covers, and the nonce.
type ExecuteParams struct {
	Address string `json:"address"`
	Nonce   string `json:"nonce"`
}

// RelayerSubmission is the Relayer's answer to a submitted transaction.
type RelayerSubmission struct {
	State         string `json:"state"`
	TransactionID string `json:"transactionID"`
}

// RelayerOperation is the Relayer's answer to a session-signer request.
type RelayerOperation struct {
	Status        string `json:"status"`
	TransactionID string `json:"transactionId"`
}

// Failed operation statuses, as the official client counts them.
var failedOperations = map[string]bool{"FAILED": true, "SUPERSEDED": true, "REPAIR_REQUIRED": true}

// Transaction states.
const (
	TransactionConfirmed = "STATE_CONFIRMED"
	TransactionFailed    = "STATE_FAILED"
	TransactionInvalid   = "STATE_INVALID"
)

// RelayerTransaction is a submitted transaction: Done once confirmed, Failed with why.
type RelayerTransaction struct {
	State string
	Hash  string
	Error string
}

func (t RelayerTransaction) Done() bool {
	return t.State == TransactionConfirmed
}

func (t RelayerTransaction) Failed() bool {
	return t.State == TransactionFailed || t.State == TransactionInvalid
}

// ErrRelayerRefused means the Relayer took the request but failed it at once.
var ErrRelayerRefused = errors.New("polymarket: the relayer refused it")

type DefaultRelayerClient struct {
	core  *httpCore
	once  *httpCore // without retries, for submissions
	kit   *redkit.Kit
	scope tracing.Scope
	log   logging.Logger
}

func NewRelayerClient(util *observability.Utility, cfg HTTPConfig) (*DefaultRelayerClient, error) {
	log := util.Logger.With("component", "polymarket.relayer")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "polymarket_relayer",
		Buckets:   redkit.MediumLongBuckets,
		Component: "polymarket relayer",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register relayer metrics: %w", err)
	}

	once := cfg
	once.MaxRetries = -1

	return &DefaultRelayerClient{
		core:  newHTTPCore(log, cfg.withDefaults(DefaultRelayerURL, defaultRelayerRPS)),
		once:  newHTTPCore(log, once.withDefaults(DefaultRelayerURL, defaultRelayerRPS)),
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
	}, nil
}

func (c *DefaultRelayerClient) ExecuteParams(ctx context.Context, auth BuilderCredentials, signer, typ string) (_ ExecuteParams, err error) {
	done := c.kit.Start(ctx, opExecuteParams)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opExecuteParams, func(ctx context.Context, span trace.Span) (ExecuteParams, error) {
		span.SetAttributes(attribute.String(attrAddress, signer), attribute.String("polymarket.transaction_type", typ))

		var out ExecuteParams

		params := url.Values{"address": {signer}, "type": {typ}}
		if err := c.core.do(ctx, http.MethodGet, "/v1/account/transactions/params", params, nil, auth, &out); err != nil {
			return ExecuteParams{}, failed(ctx, c.log, opExecuteParams, err)
		}

		return out, nil
	})
}

func (c *DefaultRelayerClient) Submit(ctx context.Context, auth BuilderCredentials, req SubmitRequest) (_ RelayerSubmission, err error) {
	done := c.kit.Start(ctx, opSubmit)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opSubmit, func(ctx context.Context, span trace.Span) (RelayerSubmission, error) {
		span.SetAttributes(attribute.String(attrAddress, req.From), attribute.String("polymarket.transaction_type", req.Type))

		var out RelayerSubmission
		if err := c.once.do(ctx, http.MethodPost, "/submit", nil, req, auth, &out); err != nil {
			return RelayerSubmission{}, failed(ctx, c.log, opSubmit, err)
		}

		if out.State == TransactionFailed || out.State == TransactionInvalid {
			return out, fmt.Errorf("%w: %s", ErrRelayerRefused, out.State)
		}

		return out, nil
	})
}

func (c *DefaultRelayerClient) Deployed(ctx context.Context, auth BuilderCredentials, wallet string, depositWallet bool) (_ bool, err error) {
	done := c.kit.Start(ctx, opDeployed)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opDeployed, func(ctx context.Context, span trace.Span) (bool, error) {
		span.SetAttributes(attribute.String(attrAddress, wallet))

		params := url.Values{"address": {wallet}}
		if depositWallet {
			params.Set("type", RelayerDepositWallet)
		}

		var out struct {
			Deployed bool `json:"deployed"`
		}
		if err := c.core.do(ctx, http.MethodGet, "/deployed", params, nil, auth, &out); err != nil {
			return false, failed(ctx, c.log, opDeployed, err)
		}

		return out.Deployed, nil
	})
}

func (c *DefaultRelayerClient) AuthorizeSessionSigner(ctx context.Context, auth BuilderCredentials, req SessionSignerRequest) (RelayerOperation, error) {
	body := sessionSignerBody(req)
	body.Scopes, body.ValidUntil = req.Scopes, strconv.FormatInt(req.ValidUntil, 10)

	return c.sessionSigner(ctx, opAuthorize, "/v1/session-signers/authorizations", auth, req, body)
}

func (c *DefaultRelayerClient) RevokeSessionSigner(ctx context.Context, auth BuilderCredentials, req SessionSignerRequest) (RelayerOperation, error) {
	return c.sessionSigner(ctx, opRevoke, "/v1/session-signers/revocations", auth, req, sessionSignerBody(req))
}

// sessionSigner is a session-signer request's body, as the official client sends it.
type sessionSigner struct {
	WalletAddress        string   `json:"walletAddress"`
	SessionSignerAddress string   `json:"sessionSignerAddress"`
	Scopes               []string `json:"scopes,omitempty"`
	ValidUntil           string   `json:"validUntil,omitempty"`
	Nonce                string   `json:"nonce"`
	Deadline             string   `json:"deadline"`
	Signature            string   `json:"signature"`
}

func sessionSignerBody(req SessionSignerRequest) sessionSigner {
	return sessionSigner{
		WalletAddress: lowerAddress(req.Wallet), SessionSignerAddress: lowerAddress(req.SessionSigner),
		Nonce: req.Nonce, Deadline: strconv.FormatInt(req.Deadline, 10), Signature: req.Signature,
	}
}

func (c *DefaultRelayerClient) sessionSigner(ctx context.Context, op, path string, auth BuilderCredentials, req SessionSignerRequest, body sessionSigner) (_ RelayerOperation, err error) {
	done := c.kit.Start(ctx, op)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, op, func(ctx context.Context, span trace.Span) (RelayerOperation, error) {
		span.SetAttributes(attribute.String(attrAddress, req.Wallet))

		var out RelayerOperation
		if err := c.core.do(ctx, http.MethodPost, path, nil, body, idempotent{auth: auth, key: req.IdempotencyKey}, &out); err != nil {
			return RelayerOperation{}, failed(ctx, c.log, op, err)
		}

		if failedOperations[out.Status] {
			return out, fmt.Errorf("%w: %s", ErrRelayerRefused, out.Status)
		}

		return out, nil
	})
}

func (c *DefaultRelayerClient) Transaction(ctx context.Context, auth BuilderCredentials, id string) (_ RelayerTransaction, err error) {
	done := c.kit.Start(ctx, opTransaction)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opTransaction, func(ctx context.Context, span trace.Span) (RelayerTransaction, error) {
		span.SetAttributes(attribute.String("polymarket.transaction_id", id))

		var resp struct {
			State string  `json:"state"`
			Hash  string  `json:"transaction_hash"`
			Error *string `json:"error_msg"`
		}

		if err := c.core.do(ctx, http.MethodGet, "/v1/account/transactions/"+url.PathEscape(id), nil, nil, auth, &resp); err != nil {
			return RelayerTransaction{}, failed(ctx, c.log, opTransaction, err)
		}

		out := RelayerTransaction{State: resp.State, Hash: resp.Hash}
		if resp.Error != nil {
			out.Error = *resp.Error
		}

		return out, nil
	})
}
