package postgres

import (
	"context"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.SessionKeyRepository = (*Wallets)(nil)

func (r *Wallets) AddSessionKey(ctx context.Context, k ports.SealedSessionKey) error {
	m, err := toSessionKeyModel(k)
	if err != nil {
		return err
	}

	t := table.SessionKeys

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		_, err := t.DELETE().
			WHERE(t.WalletID.EQ(jet.UUID(m.WalletID)).AND(t.State.NOT_EQ(jet.String(string(domain.SessionKeyActive))))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		_, err = t.INSERT(t.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

		return err
	})
}

func (r *Wallets) SaveSessionKey(ctx context.Context, k ports.SealedSessionKey) error {
	m, err := toSessionKeyModel(k)
	if err != nil {
		return err
	}

	t := table.SessionKeys

	// Not when it was reminded of: a key is reminded of once.
	return affectedOne(t.UPDATE(t.OwnerAddress, t.KeySealed, t.State, t.ValidUntil, t.Nonce, t.Deadline, t.TransactionID, t.Error, t.UpdatedAt).
		MODEL(m).
		WHERE(t.WalletID.EQ(jet.UUID(m.WalletID)).AND(t.SignerAddress.EQ(jet.String(m.SignerAddress)))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Wallets) SessionKeys(ctx context.Context, wallet domain.WalletID) ([]ports.SealedSessionKey, error) {
	wid, err := parseID(wallet)
	if err != nil {
		return nil, nil
	}

	return r.sessionKeys(ctx, table.SessionKeys.WalletID.EQ(jet.UUID(wid)), table.SessionKeys.CreatedAt.DESC())
}

func (r *Wallets) AuthorizingSessionKeys(ctx context.Context) ([]ports.SealedSessionKey, error) {
	t := table.SessionKeys
	return r.sessionKeys(ctx, t.State.EQ(jet.String(string(domain.SessionKeyAuthorizing))), t.UpdatedAt.ASC())
}

func (r *Wallets) sessionKeys(ctx context.Context, where jet.BoolExpression, order jet.OrderByClause) ([]ports.SealedSessionKey, error) {
	var rows []model.SessionKeys

	err := jet.SELECT(table.SessionKeys.AllColumns).
		FROM(table.SessionKeys).
		WHERE(where).
		ORDER_BY(order).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ports.SealedSessionKey, len(rows))
	for i, m := range rows {
		out[i] = toSealedSessionKey(m)
	}

	return out, nil
}

func (r *Wallets) ActivateSessionKey(ctx context.Context, k ports.SealedSessionKey, creds ports.SealedCredentials) error {
	m, err := toSessionKeyModel(k)
	if err != nil {
		return err
	}

	t := table.SessionKeys
	wallet := t.WalletID.EQ(jet.UUID(m.WalletID))

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		// The key it replaces goes first: a wallet has one active key.
		_, err := t.DELETE().
			WHERE(wallet.AND(t.SignerAddress.NOT_EQ(jet.String(m.SignerAddress)))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		err = affectedOne(t.UPDATE(t.KeySealed, t.State, t.Error, t.UpdatedAt).
			MODEL(m).
			WHERE(wallet.AND(t.SignerAddress.EQ(jet.String(m.SignerAddress)))).
			ExecContext(ctx, r.db.conn(ctx)))
		if err != nil {
			return err
		}

		return r.SaveCredentials(ctx, creds)
	})
}

func (r *Wallets) DeleteSessionKeys(ctx context.Context, wallet domain.WalletID) error {
	wid, err := parseID(wallet)
	if err != nil {
		return nil
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		_, err := table.SessionKeys.DELETE().
			WHERE(table.SessionKeys.WalletID.EQ(jet.UUID(wid))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		pc := table.PolymarketCredentials

		_, err = pc.DELETE().
			WHERE(pc.WalletID.EQ(jet.UUID(wid)).AND(pc.SessionKey.IS_TRUE())).
			ExecContext(ctx, r.db.conn(ctx))

		return err
	})
}

func (r *Wallets) ExpiringSessionKeys(ctx context.Context, before time.Time) ([]ports.ExpiringSessionKey, error) {
	t := table.SessionKeys

	var rows []struct {
		model.SessionKeys
		Wallet walletRow
	}

	err := jet.SELECT(t.AllColumns, table.Wallets.AllColumns, table.PolymarketCredentials.WalletID,
		table.PolymarketCredentials.PrivateKeySealed, table.PolymarketCredentials.SessionKey).
		FROM(t.INNER_JOIN(table.Wallets, table.Wallets.ID.EQ(t.WalletID)).
			LEFT_JOIN(table.PolymarketCredentials, table.PolymarketCredentials.WalletID.EQ(t.WalletID))).
		WHERE(t.State.EQ(jet.String(string(domain.SessionKeyActive))).
			AND(t.RemindedAt.IS_NULL()).
			AND(t.ValidUntil.LT(jet.TimestampzT(before)))).
		ORDER_BY(t.ValidUntil.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ports.ExpiringSessionKey, len(rows))
	for i, row := range rows {
		out[i] = ports.ExpiringSessionKey{Key: toSealedSessionKey(row.SessionKeys).Info, Wallet: toWallet(row.Wallet)}
	}

	return out, nil
}

func (r *Wallets) MarkReminded(ctx context.Context, wallet domain.WalletID, signer domain.WalletAddress, at time.Time) error {
	wid, err := parseID(wallet)
	if err != nil {
		return err
	}

	t := table.SessionKeys

	return affectedOne(t.UPDATE(t.RemindedAt).
		SET(jet.TimestampzT(at)).
		WHERE(t.WalletID.EQ(jet.UUID(wid)).AND(t.SignerAddress.EQ(jet.String(string(signer))))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func toSessionKeyModel(k ports.SealedSessionKey) (model.SessionKeys, error) {
	wid, err := parseID(k.Info.WalletID)
	if err != nil {
		return model.SessionKeys{}, err
	}

	return model.SessionKeys{
		WalletID:      wid,
		SignerAddress: string(k.Info.Signer),
		OwnerAddress:  string(k.Info.Owner),
		KeySealed:     optionalBytes(k.Key),
		State:         string(k.Info.State),
		ValidUntil:    k.Info.ValidUntil,
		Nonce:         k.Nonce,
		Deadline:      k.Deadline,
		TransactionID: k.TransactionID,
		Error:         k.Info.Error,
		CreatedAt:     k.Info.CreatedAt,
		UpdatedAt:     k.Info.UpdatedAt,
	}, nil
}

func toSealedSessionKey(m model.SessionKeys) ports.SealedSessionKey {
	return ports.SealedSessionKey{
		Info: domain.SessionKey{
			WalletID:   domain.WalletID(m.WalletID.String()),
			Signer:     domain.WalletAddress(m.SignerAddress),
			Owner:      domain.WalletAddress(m.OwnerAddress),
			State:      domain.SessionKeyState(m.State),
			ValidUntil: m.ValidUntil.UTC(),
			Error:      m.Error,
			CreatedAt:  m.CreatedAt.UTC(),
			UpdatedAt:  m.UpdatedAt.UTC(),
		},
		Key:           derefBytes(m.KeySealed),
		Nonce:         m.Nonce,
		Deadline:      m.Deadline.UTC(),
		TransactionID: m.TransactionID,
	}
}
