package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
)

// sessionKeyDTO never carries the key: only its address.
type sessionKeyDTO struct {
	// Signer is the key's address.
	Signer string `json:"signer"`
	// Owner is the account that controls the Deposit Wallet, and signs for it.
	Owner string `json:"owner"`
	// State: signing waits for the owner's signature, authorizing for the chain; the
	// wallet trades with an active key; a failed one says why in error.
	State      string    `json:"state" enums:"signing,authorizing,active,failed"`
	ValidUntil time.Time `json:"validUntil" format:"date-time"`
	Error      string    `json:"error,omitempty" validate:"optional"`
	CreatedAt  time.Time `json:"createdAt" format:"date-time"`
	UpdatedAt  time.Time `json:"updatedAt" format:"date-time"`
} // @name SessionKey

func toSessionKeyDTO(k domain.SessionKey) sessionKeyDTO {
	return sessionKeyDTO{
		Signer: string(k.Signer), Owner: string(k.Owner), State: string(k.State), ValidUntil: k.ValidUntil,
		Error: k.Error, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt,
	}
}

func toSessionKeyPtr(k *domain.SessionKey) *sessionKeyDTO {
	if k == nil {
		return nil
	}

	dto := toSessionKeyDTO(*k)

	return &dto
}

type sessionKeysDTO struct {
	// Active is the key the wallet trades with.
	Active *sessionKeyDTO `json:"active,omitempty" validate:"optional"`
	// Pending is the key being authorised, which replaces the active one once confirmed;
	// or the last that failed.
	Pending *sessionKeyDTO `json:"pending,omitempty" validate:"optional"`
} // @name SessionKeys

type sessionKeySigningDTO struct {
	Key sessionKeyDTO `json:"key"`
	// TypedData is what the owner's browser wallet signs, with eth_signTypedData_v4 on
	// Polygon.
	TypedData map[string]any `json:"typedData"`
} // @name SessionKeySigning

func toSessionKeySigningDTO(s wallets.SessionKeySigning) sessionKeySigningDTO {
	return sessionKeySigningDTO{Key: toSessionKeyDTO(s.Key), TypedData: itemJSON(s.TypedData)}
}

type startSessionKeyRequest struct {
	// Owner is the browser wallet's account, which must control the Deposit Wallet.
	Owner string `json:"owner"`
} // @name StartSessionKeyRequest

type signatureRequest struct {
	// Signature is what eth_signTypedData_v4 gave, hex.
	Signature string `json:"signature"`
} // @name SignatureRequest

type revocationDTO struct {
	// Revoked says the Relayer took the revocation; the key is removed here either way.
	Revoked bool `json:"revoked"`
	// Problem says why it was not revoked.
	Problem string `json:"problem,omitempty" validate:"optional"`
} // @name Revocation

// sessionKeys shows a wallet's session keys.
//
//	@Summary	Show a wallet's session keys
//	@ID			getSessionKeys
//	@Tags		wallets
//	@Param		id	path		string	true	"Wallet ID"
//	@Success	200	{object}	sessionKeysDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id}/session-key [get]
func (s *Server) sessionKeys(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	keys, err := s.wallets.SessionKeys(ctx, p, domain.WalletID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, sessionKeysDTO{Active: toSessionKeyPtr(keys.Active), Pending: toSessionKeyPtr(keys.Pending)})
}

// startSessionKey makes a session key for a Deposit Wallet, and gives the batch
// authorising it for the owner's browser wallet to sign (D156, D158). Only from a browser
// session, once an admin has entered a Builder key.
//
//	@Summary	Start authorising a session key
//	@ID			startSessionKey
//	@Tags		wallets
//	@Param		id		path		string					true	"Wallet ID"
//	@Param		body	body		startSessionKeyRequest	true	"The owner"
//	@Success	200		{object}	sessionKeySigningDTO
//	@Failure	400		{object}	errorBody	"invalid_address, not_deposit_wallet or relayer_refused"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"session_keys_off, or forbidden with an API token"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"session_key_pending"
//	@Failure	503		{object}	errorBody	"upstream_unavailable: the Relayer could not be reached"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/session-key [post]
func (s *Server) startSessionKey(ctx context.Context, c *app.RequestContext) {
	var req startSessionKeyRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	signing, err := s.wallets.StartSessionKey(ctx, p, domain.WalletID(c.Param("id")), req.Owner)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toSessionKeySigningDTO(signing))
}

// signSessionKey submits the owner's signature authorising the session key. It is active
// once the chain confirms it, in a minute or so.
//
//	@Summary	Authorise a session key
//	@ID			signSessionKey
//	@Tags		wallets
//	@Param		id		path		string				true	"Wallet ID"
//	@Param		body	body		signatureRequest	true	"The owner's signature"
//	@Success	200		{object}	sessionKeyDTO
//	@Failure	400		{object}	errorBody	"invalid_input, wrong_signer, signature_expired or relayer_refused"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"session_keys_off, or forbidden with an API token"
//	@Failure	404		{object}	errorBody	"not_found: no such wallet, or no key waits for a signature"
//	@Failure	503		{object}	errorBody	"upstream_unavailable: send the signature again"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/session-key/signature [post]
func (s *Server) signSessionKey(ctx context.Context, c *app.RequestContext) {
	var req signatureRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	k, err := s.wallets.SignSessionKey(ctx, p, domain.WalletID(c.Param("id")), req.Signature)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toSessionKeyDTO(k))
}

// startRevocation gives the batch revoking the wallet's active session key, for its
// owner's browser wallet to sign (D159).
//
//	@Summary	Start revoking a session key
//	@ID			startRevocation
//	@Tags		wallets
//	@Param		id	path		string	true	"Wallet ID"
//	@Success	200	{object}	sessionKeySigningDTO
//	@Failure	400	{object}	errorBody	"relayer_refused"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"session_keys_off, or forbidden with an API token"
//	@Failure	404	{object}	errorBody	"not_found: no such wallet, or no active session key"
//	@Failure	503	{object}	errorBody	"upstream_unavailable: the Relayer could not be reached"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/session-key/revocation [post]
func (s *Server) startRevocation(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	signing, err := s.wallets.StartRevocation(ctx, p, domain.WalletID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toSessionKeySigningDTO(signing))
}

// revokeSessionKey submits the owner's signature revoking the session key, and removes
// it, with the credentials it made, whatever the Relayer answers (D159): Polymarket then
// cancels its open orders.
//
//	@Summary	Revoke and remove a session key
//	@ID			revokeSessionKey
//	@Tags		wallets
//	@Param		id		path		string				true	"Wallet ID"
//	@Param		body	body		signatureRequest	true	"The owner's signature"
//	@Success	200		{object}	revocationDTO
//	@Failure	400		{object}	errorBody	"invalid_input, wrong_signer or signature_expired: nothing removed"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"session_keys_off, or forbidden with an API token"
//	@Failure	404		{object}	errorBody	"not_found: no such wallet, or no active session key"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/session-key/revocation/signature [post]
func (s *Server) revokeSessionKey(ctx context.Context, c *app.RequestContext) {
	var req signatureRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	r, err := s.wallets.RevokeSessionKey(ctx, p, domain.WalletID(c.Param("id")), req.Signature)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, revocationDTO{Revoked: r.Revoked, Problem: r.Problem})
}

// removeSessionKey removes a wallet's session keys, and the credentials the active one
// made, without revoking it: for when its owner cannot sign (D159). Nobody can use it
// then; it expires on chain.
//
//	@Summary	Remove a session key without revoking it
//	@ID			removeSessionKey
//	@Tags		wallets
//	@Param		id	path	string	true	"Wallet ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id}/session-key [delete]
func (s *Server) removeSessionKey(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.wallets.RemoveSessionKey(ctx, p, domain.WalletID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
