package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
)

type approvalDTO struct {
	Token    string `json:"token"`
	Spender  string `json:"spender"`
	Standard string `json:"standard" enums:"ERC20,ERC1155"`
	// Name is what it is for people, like "pUSD to the CTF Exchange".
	Name string `json:"name"`
} // @name Approval

type approvalSetupDTO struct {
	// Owner signed, or signs, for the wallet.
	Owner string `json:"owner"`
	// Kind is the wallet's: 1 a proxy wallet, 2 a Safe, 3 a Deposit Wallet.
	Kind int `json:"kind" enums:"1,2,3"`
	// Granting is how many approvals it grants; none when none were missing.
	Granting int `json:"granting"`
	// State: signing waits for the owner's signature, submitted for the chain; a failed one
	// says why in error.
	State     string    `json:"state" enums:"signing,submitted,done,failed"`
	Error     string    `json:"error,omitempty" validate:"optional"`
	CreatedAt time.Time `json:"createdAt" format:"date-time"`
	UpdatedAt time.Time `json:"updatedAt" format:"date-time"`
} // @name ApprovalSetup

func toApprovalSetupDTO(s domain.ApprovalSetup) approvalSetupDTO {
	return approvalSetupDTO{
		Owner: string(s.Owner), Kind: int(s.Kind), Granting: s.Granting, State: string(s.State),
		Error: s.Error, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

type approvalsDTO struct {
	// Ready says no trading approval is missing (D160).
	Ready bool `json:"ready"`
	// Missing are those Polymarket's index shows missing, which may be a minute behind.
	Missing   []approvalDTO `json:"missing"`
	CheckedAt time.Time     `json:"checkedAt" format:"date-time"`
	// Setup is the last setting up of them here.
	Setup *approvalSetupDTO `json:"setup,omitempty" validate:"optional"`
	// Way is how they can be set up here: with the imported key, in the owner's browser
	// wallet, or not at all (no Builder key; an EOA, or a proxy wallet without a key).
	Way string `json:"way" enums:"key,browser,none"`
	// Why says why not, when Way is none.
	Why string `json:"why,omitempty" validate:"optional"`
} // @name Approvals

type approvalSigningDTO struct {
	Setup approvalSetupDTO `json:"setup"`
	// TypedData is what a Deposit Wallet's owner signs, with eth_signTypedData_v4.
	TypedData map[string]any `json:"typedData,omitempty" validate:"optional"`
	// Message is what a Safe's owner signs, hex, with personal_sign.
	Message string `json:"message,omitempty" validate:"optional"`
} // @name ApprovalSigning

type startApprovalSetupRequest struct {
	// Owner is the browser wallet's account, which must control the wallet.
	Owner string `json:"owner"`
} // @name StartApprovalSetupRequest

// approvals shows a wallet's trading approvals.
//
//	@Summary	Show a wallet's trading approvals
//	@ID			getApprovals
//	@Tags		wallets
//	@Param		id	path		string	true	"Wallet ID"
//	@Success	200	{object}	approvalsDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	503	{object}	errorBody	"upstream_unavailable: Polymarket could not be reached"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/wallets/{id}/approvals [get]
func (s *Server) approvals(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	st, err := s.wallets.Approvals(ctx, p, domain.WalletID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := approvalsDTO{
		Ready: len(st.Missing) == 0, Missing: make([]approvalDTO, len(st.Missing)), CheckedAt: st.CheckedAt,
		Way: string(st.Way), Why: st.Why,
	}
	if st.Way == wallets.SetUpNot {
		out.Way = "none"
	}

	for i, a := range st.Missing {
		out.Missing[i] = approvalDTO{Token: string(a.Token), Spender: string(a.Spender), Standard: a.Standard, Name: a.Name}
	}

	if st.Setup != nil {
		dto := toApprovalSetupDTO(*st.Setup)
		out.Setup = &dto
	}

	c.JSON(http.StatusOK, out)
}

// setUpApprovals grants the wallet's missing approvals, signed by the server with its
// imported key, through the Relayer (D161). Only from a browser session, once an admin has
// entered a Builder key.
//
//	@Summary	Set up approvals with the imported key
//	@ID			setUpApprovals
//	@Tags		wallets
//	@Param		id	path		string	true	"Wallet ID"
//	@Success	200	{object}	approvalSetupDTO
//	@Failure	400	{object}	errorBody	"eoa_approvals, not_deployed or relayer_refused"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"builder_key_missing, or forbidden with an API token"
//	@Failure	404	{object}	errorBody	"not_found: no such wallet, or no imported key"
//	@Failure	409	{object}	errorBody	"setup_pending"
//	@Failure	503	{object}	errorBody	"upstream_unavailable: Polymarket or the RPC could not be reached"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/approvals/setup [post]
func (s *Server) setUpApprovals(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	setup, err := s.wallets.SetUpApprovals(ctx, p, domain.WalletID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toApprovalSetupDTO(setup))
}

// startApprovalSetup gives the transaction granting the wallet's missing approvals, for
// the owner's browser wallet to sign: a Deposit Wallet's or a Safe's (D161).
//
//	@Summary	Start setting up approvals in the browser
//	@ID			startApprovalSetup
//	@Tags		wallets
//	@Param		id		path		string						true	"Wallet ID"
//	@Param		body	body		startApprovalSetupRequest	true	"The owner"
//	@Success	200		{object}	approvalSigningDTO
//	@Failure	400		{object}	errorBody	"invalid_address, not_owner, eoa_approvals, needs_imported_key or not_deployed"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"builder_key_missing, or forbidden with an API token"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"setup_pending"
//	@Failure	503		{object}	errorBody	"upstream_unavailable: Polymarket or the RPC could not be reached"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/approvals/signing [post]
func (s *Server) startApprovalSetup(ctx context.Context, c *app.RequestContext) {
	var req startApprovalSetupRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	signing, err := s.wallets.StartApprovalSetup(ctx, p, domain.WalletID(c.Param("id")), req.Owner)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := approvalSigningDTO{Setup: toApprovalSetupDTO(signing.Setup)}
	if signing.Request != nil {
		out.TypedData, out.Message = itemJSON(signing.Request.TypedData), signing.Request.Message
	}

	c.JSON(http.StatusOK, out)
}

// signApprovalSetup submits the owner's signature of the setup startApprovalSetup gave.
//
//	@Summary	Set up approvals with the owner's signature
//	@ID			signApprovalSetup
//	@Tags		wallets
//	@Param		id		path		string				true	"Wallet ID"
//	@Param		body	body		signatureRequest	true	"The owner's signature"
//	@Success	200		{object}	approvalSetupDTO
//	@Failure	400		{object}	errorBody	"invalid_input, wrong_signer, signature_expired or relayer_refused"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"builder_key_missing, or forbidden with an API token"
//	@Failure	404		{object}	errorBody	"not_found: no such wallet, or no setup waits for a signature"
//	@Failure	503		{object}	errorBody	"upstream_unavailable: send the signature again"
//	@Security	SessionCookie
//	@Router		/wallets/{id}/approvals/signature [post]
func (s *Server) signApprovalSetup(ctx context.Context, c *app.RequestContext) {
	var req signatureRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	setup, err := s.wallets.SignApprovalSetup(ctx, p, domain.WalletID(c.Param("id")), req.Signature)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toApprovalSetupDTO(setup))
}
