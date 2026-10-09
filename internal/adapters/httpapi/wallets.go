package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
)

type walletDTO struct {
	ID             string    `json:"id"`
	Address        string    `json:"address"`
	Label          string    `json:"label"`
	HasCredentials bool      `json:"hasCredentials"`
	CreatedAt      time.Time `json:"createdAt" format:"date-time"`
} // @name Wallet

func toWalletDTO(w domain.Wallet) walletDTO {
	return walletDTO{
		ID:             string(w.ID),
		Address:        string(w.Address),
		Label:          w.Label,
		HasCredentials: w.HasCredentials,
		CreatedAt:      w.CreatedAt,
	}
}

type walletList struct {
	Wallets []walletDTO `json:"wallets"`
} // @name WalletList

// credentialsDTO never carries a secret: only a hint of the API key.
type credentialsDTO struct {
	APIKeyHint    string    `json:"apiKeyHint"`
	Signer        string    `json:"signer"`
	SignatureType int       `json:"signatureType" enums:"0,1,2,3"`
	CreatedAt     time.Time `json:"createdAt" format:"date-time"`
	UpdatedAt     time.Time `json:"updatedAt" format:"date-time"`
} // @name Credentials

func toCredentialsDTO(c domain.CredentialsInfo) credentialsDTO {
	return credentialsDTO{
		APIKeyHint:    c.APIKeyHint,
		Signer:        string(c.Signer),
		SignatureType: int(c.SignatureType),
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}

type linkWalletRequest struct {
	Address string `json:"address"`
	Label   string `json:"label" validate:"optional"`
} // @name LinkWalletRequest

type setCredentialsRequest struct {
	APIKey     string `json:"apiKey"`
	Secret     string `json:"secret"`
	Passphrase string `json:"passphrase"`
	// Signer defaults to the wallet address.
	Signer string `json:"signer" validate:"optional"`
	// SignatureType: 0 EOA, 1 Polymarket proxy, 2 Gnosis Safe, 3 deposit wallet.
	SignatureType int `json:"signatureType" validate:"optional" enums:"0,1,2,3" default:"0"`
} // @name SetCredentialsRequest

// listWallets lists the user's linked wallets.
//
//	@Summary	List wallets
//	@ID			listWallets
//	@Tags		wallets
//	@Success	200	{object}	walletList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets [get]
func (s *Server) listWallets(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.wallets.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := walletList{Wallets: make([]walletDTO, len(list))}
	for i, w := range list {
		out.Wallets[i] = toWalletDTO(w)
	}

	c.JSON(http.StatusOK, out)
}

// linkWallet adds a wallet by address, for reading its public data.
//
//	@Summary	Link a wallet
//	@ID			linkWallet
//	@Tags		wallets
//	@Param		body	body		linkWalletRequest	true	"Wallet"
//	@Success	201		{object}	walletDTO
//	@Failure	400		{object}	errorBody	"invalid_input or invalid_address"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	409		{object}	errorBody	"wallet_exists"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets [post]
func (s *Server) linkWallet(ctx context.Context, c *app.RequestContext) {
	var req linkWalletRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	w, err := s.wallets.Link(ctx, p, req.Address, req.Label)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toWalletDTO(w))
}

// unlinkWallet removes a wallet and its credentials.
//
//	@Summary	Unlink a wallet
//	@ID			unlinkWallet
//	@Tags		wallets
//	@Param		id	path	string	true	"Wallet ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id} [delete]
func (s *Server) unlinkWallet(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.wallets.Unlink(ctx, p, domain.WalletID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// credentialsInfo describes the stored credentials without revealing them.
//
//	@Summary	Show a wallet's API credentials
//	@ID			getCredentials
//	@Tags		wallets
//	@Param		id	path		string	true	"Wallet ID"
//	@Success	200	{object}	credentialsDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found: no such wallet, or no credentials on it"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id}/credentials [get]
func (s *Server) credentialsInfo(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	info, err := s.wallets.CredentialsInfo(ctx, p, domain.WalletID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCredentialsDTO(info))
}

// setCredentials checks the credentials against Polymarket before storing them encrypted.
//
//	@Summary	Store a wallet's API credentials
//	@ID			setCredentials
//	@Tags		wallets
//	@Param		id		path		string					true	"Wallet ID"
//	@Param		body	body		setCredentialsRequest	true	"Polymarket CLOB API credentials"
//	@Success	200		{object}	credentialsDTO
//	@Failure	400		{object}	errorBody	"invalid_input, invalid_address or credentials_rejected"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	503		{object}	errorBody	"upstream_unavailable: Polymarket could not be reached"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id}/credentials [put]
func (s *Server) setCredentials(ctx context.Context, c *app.RequestContext) {
	var req setCredentialsRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	info, err := s.wallets.SetCredentials(ctx, p, domain.WalletID(c.Param("id")), wallets.CredentialsInput{
		APIKey:        req.APIKey,
		Secret:        req.Secret,
		Passphrase:    req.Passphrase,
		Signer:        req.Signer,
		SignatureType: domain.SignatureType(req.SignatureType),
	})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCredentialsDTO(info))
}

// removeCredentials deletes the stored credentials; the wallet stays linked.
//
//	@Summary	Remove a wallet's API credentials
//	@ID			removeCredentials
//	@Tags		wallets
//	@Param		id	path	string	true	"Wallet ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id}/credentials [delete]
func (s *Server) removeCredentials(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.wallets.RemoveCredentials(ctx, p, domain.WalletID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
