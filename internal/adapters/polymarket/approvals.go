package polymarket

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.TradingApprovals = (*Approvals)(nil)

// Approvals serves ports.TradingApprovals: the approvals of @polymarket/client's trading
// setup (D160), read from the Data API and a Polygon RPC, granted through the Relayer with
// the Builder key builder gives (D161, D163).
type Approvals struct {
	data       pm.DataClient
	rpc        pm.RPCClient
	relayer    pm.RelayerClient
	builder    func() (pm.BuilderCredentials, bool)
	derivation pm.Derivation
	contracts  pm.Contracts
	now        func() time.Time
}

func NewApprovals(data pm.DataClient, rpc pm.RPCClient, relayer pm.RelayerClient, builder func() (pm.BuilderCredentials, bool)) *Approvals {
	return &Approvals{
		data: data, rpc: rpc, relayer: relayer, builder: builder,
		derivation: pm.Production, contracts: pm.ProductionContracts, now: time.Now,
	}
}

func (a *Approvals) Status(ctx context.Context, wallet domain.WalletAddress) ([]domain.Approval, time.Time, error) {
	snap, err := a.data.Approvals(ctx, string(wallet))
	if err != nil {
		return nil, time.Time{}, accountError(err)
	}

	missing, err := snap.Missing(common.HexToAddress(string(wallet)), a.contracts.TradingApprovals())
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
	}

	return a.toDomain(missing), snap.CheckedAt.UTC(), nil
}

func (a *Approvals) Missing(ctx context.Context, wallet domain.WalletAddress) ([]domain.Approval, error) {
	required := a.contracts.TradingApprovals()
	owner := common.HexToAddress(string(wallet))

	checks := make([]pm.Call, len(required))
	for i, r := range required {
		checks[i] = r.Check(owner)
	}

	results, err := a.rpc.Call(ctx, checks)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
	}

	var missing []pm.Approval

	for i, r := range required {
		granted, err := r.Granted(results[i])
		if err != nil {
			return nil, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
		}

		if !granted {
			missing = append(missing, r)
		}
	}

	return a.toDomain(missing), nil
}

func (a *Approvals) Available() bool {
	_, ok := a.builder()
	return ok
}

func (a *Approvals) Kind(owner, wallet domain.WalletAddress) (domain.SignatureType, bool) {
	typ, ok := a.derivation.Classify(common.HexToAddress(string(owner)), common.HexToAddress(string(wallet)))
	return domain.SignatureType(typ), ok
}

func (a *Approvals) Deployed(ctx context.Context, wallet domain.WalletAddress, kind domain.SignatureType) (bool, error) {
	auth, ok := a.builder()
	if !ok {
		return false, domain.ErrNoBuilderKey
	}

	deployed, err := a.relayer.Deployed(ctx, auth, string(wallet), kind == domain.SignatureDepositWallet)
	if err != nil {
		return false, relayerError(err)
	}

	return deployed, nil
}

// relayerTypes are the Relayer's transaction kinds of the smart wallets.
var relayerTypes = map[domain.SignatureType]string{
	domain.SignaturePolyProxy:     pm.RelayerProxy,
	domain.SignatureGnosisSafe:    pm.RelayerSafe,
	domain.SignatureDepositWallet: pm.RelayerDepositWallet,
}

func (a *Approvals) Prepare(ctx context.Context, kind domain.SignatureType, owner, wallet domain.WalletAddress, approvals []domain.Approval) (ports.ApprovalBatch, error) {
	auth, ok := a.builder()
	if !ok {
		return ports.ApprovalBatch{}, domain.ErrNoBuilderKey
	}

	typ, ok := relayerTypes[kind]
	if !ok {
		return ports.ApprovalBatch{}, domain.ErrEOAApprovals
	}

	b := ports.ApprovalBatch{Kind: kind, Wallet: wallet, Owner: owner, Approvals: approvals}

	calls, err := a.calls(b)
	if err != nil {
		return ports.ApprovalBatch{}, err
	}

	params, err := a.relayer.ExecuteParams(ctx, auth, string(owner), typ)
	if err != nil {
		return ports.ApprovalBatch{}, relayerError(err)
	}

	b.Nonce = params.Nonce

	switch kind {
	case domain.SignatureDepositWallet:
		b.Deadline = a.now().UTC().Truncate(time.Second).Add(pm.BatchDeadline)
	case domain.SignaturePolyProxy:
		data, err := pm.ProxyCall(calls)
		if err != nil {
			return ports.ApprovalBatch{}, err
		}

		gas, err := a.rpc.EstimateGas(ctx, common.HexToAddress(string(owner)), pm.Call{To: a.derivation.ProxyFactory, Data: data})
		if err != nil {
			return ports.ApprovalBatch{}, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
		}

		b.Relay, b.GasLimit = params.Address, gas
	}

	return b, nil
}

// calls grant the batch's approvals: only those of the trading setup, whatever it asks.
func (a *Approvals) calls(b ports.ApprovalBatch) ([]pm.Call, error) {
	required := a.contracts.TradingApprovals()
	calls := make([]pm.Call, 0, len(b.Approvals))

	for _, want := range b.Approvals {
		found := false

		for _, r := range required {
			if string(r.Standard) == want.Standard && r.Token == common.HexToAddress(string(want.Token)) &&
				r.Spender == common.HexToAddress(string(want.Spender)) {
				calls, found = append(calls, r.Grant()), true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("%w: %s is no trading approval", domain.ErrInvalidInput, want.Name)
		}
	}

	if len(calls) == 0 {
		return nil, fmt.Errorf("%w: nothing to grant", domain.ErrInvalidInput)
	}

	return calls, nil
}

func (a *Approvals) transaction(b ports.ApprovalBatch) (pm.GaslessTransaction, error) {
	calls, err := a.calls(b)
	if err != nil {
		return nil, err
	}

	owner, wallet := string(b.Owner), string(b.Wallet)

	switch b.Kind {
	case domain.SignatureDepositWallet:
		return pm.NewDepositWalletTransaction(owner, wallet, b.Nonce, b.Deadline, calls, a.derivation), nil
	case domain.SignatureGnosisSafe:
		return pm.NewSafeTransaction(owner, wallet, b.Nonce, calls, a.contracts), nil
	case domain.SignaturePolyProxy:
		data, err := pm.ProxyCall(calls)
		if err != nil {
			return nil, err
		}

		return pm.NewProxyTransaction(owner, wallet, b.Nonce, b.Relay, b.GasLimit, data, a.derivation, a.contracts), nil
	}

	return nil, domain.ErrEOAApprovals
}

func (a *Approvals) SigningRequest(b ports.ApprovalBatch) (ports.SigningRequest, error) {
	tx, err := a.transaction(b)
	if err != nil {
		return ports.SigningRequest{}, err
	}

	switch t := tx.(type) {
	case pm.DepositWalletTransaction:
		typed, err := t.Batch.TypedDataJSON(t.ChainID)
		return ports.SigningRequest{TypedData: typed}, err
	case pm.SafeTransaction:
		hash, err := t.Hash()
		return ports.SigningRequest{Message: hexutil.Encode(hash)}, err
	}

	return ports.SigningRequest{}, domain.ErrNeedsImportedKey
}

func (a *Approvals) Signer(b ports.ApprovalBatch, signature string) (domain.WalletAddress, error) {
	tx, err := a.transaction(b)
	if err != nil {
		return "", err
	}

	sig, err := hexutil.Decode(signature)
	if err != nil {
		return "", fmt.Errorf("%w: a signature is hex", domain.ErrInvalidInput)
	}

	signer, err := pm.GaslessSigner(tx, sig)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
	}

	return domain.WalletAddress(signer.Hex()), nil
}

func (a *Approvals) Sign(b ports.ApprovalBatch, key string) (string, error) {
	tx, err := a.transaction(b)
	if err != nil {
		return "", err
	}

	sig, err := pm.SignGasless(tx, key)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
	}

	return hexutil.Encode(sig), nil
}

func (a *Approvals) Submit(ctx context.Context, b ports.ApprovalBatch, signature string) (string, error) {
	auth, ok := a.builder()
	if !ok {
		return "", domain.ErrNoBuilderKey
	}

	tx, err := a.transaction(b)
	if err != nil {
		return "", err
	}

	sig, err := hexutil.Decode(signature)
	if err != nil {
		return "", fmt.Errorf("%w: a signature is hex", domain.ErrInvalidInput)
	}

	req, err := tx.Submission(sig)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
	}

	sub, err := a.relayer.Submit(ctx, auth, req)
	if err != nil {
		return "", relayerError(err)
	}

	return sub.TransactionID, nil
}

func (a *Approvals) Confirmed(ctx context.Context, transactionID string) (bool, error) {
	auth, ok := a.builder()
	if !ok {
		return false, domain.ErrNoBuilderKey
	}

	return confirmed(ctx, a.relayer, auth, transactionID)
}

func (a *Approvals) toDomain(list []pm.Approval) []domain.Approval {
	c := a.contracts
	tokens := map[common.Address]string{
		c.Collateral: "pUSD", c.ConditionalTokens: "Outcome tokens", c.PositionManager: "V2 outcome tokens",
	}
	spenders := map[common.Address]string{
		c.CTFExchange: "the CTF Exchange", c.NegRiskExchange: "the Neg Risk Exchange", c.ExchangeV3: "Exchange V3",
		c.CollateralAdapter: "the collateral adapter", c.NegRiskCollateralAdapter: "the Neg Risk collateral adapter",
		c.V2Router: "the V2 router", c.PerpsDeposit: "the perps deposit", c.AutoRedeem: "auto-redeem",
		c.BinaryModule: "the binary module", c.NegRiskModule: "the Neg Risk module",
	}

	out := make([]domain.Approval, len(list))
	for i, x := range list {
		out[i] = domain.Approval{
			Token: domain.WalletAddress(x.Token.Hex()), Spender: domain.WalletAddress(x.Spender.Hex()),
			Standard: string(x.Standard), Name: tokens[x.Token] + " to " + spenders[x.Spender],
		}
	}

	return out
}
