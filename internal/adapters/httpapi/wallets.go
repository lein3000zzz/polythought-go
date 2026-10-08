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
	CreatedAt      time.Time `json:"createdAt"`
}

func toWalletDTO(w domain.Wallet) walletDTO {
	return walletDTO{
		ID:             string(w.ID),
		Address:        string(w.Address),
		Label:          w.Label,
		HasCredentials: w.HasCredentials,
		CreatedAt:      w.CreatedAt,
	}
}

// credentialsDTO never carries a secret: only a hint of the API key.
type credentialsDTO struct {
	APIKeyHint    string    `json:"apiKeyHint"`
	Signer        string    `json:"signer"`
	SignatureType int       `json:"signatureType"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func toCredentialsDTO(c domain.CredentialsInfo) credentialsDTO {
	return credentialsDTO{
		APIKeyHint:    c.APIKeyHint,
		Signer:        string(c.Signer),
		SignatureType: int(c.SignatureType),
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}

func (s *Server) listWallets(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.wallets.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := make([]walletDTO, len(list))
	for i, w := range list {
		out[i] = toWalletDTO(w)
	}

	c.JSON(http.StatusOK, map[string]any{"wallets": out})
}

func (s *Server) linkWallet(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Address string `json:"address"`
		Label   string `json:"label"`
	}

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

func (s *Server) unlinkWallet(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.wallets.Unlink(ctx, p, domain.WalletID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) credentialsInfo(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	info, err := s.wallets.CredentialsInfo(ctx, p, domain.WalletID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCredentialsDTO(info))
}

func (s *Server) setCredentials(ctx context.Context, c *app.RequestContext) {
	var req struct {
		APIKey        string `json:"apiKey"`
		Secret        string `json:"secret"`
		Passphrase    string `json:"passphrase"`
		Signer        string `json:"signer"`
		SignatureType int    `json:"signatureType"`
	}

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

func (s *Server) removeCredentials(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.wallets.RemoveCredentials(ctx, p, domain.WalletID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
