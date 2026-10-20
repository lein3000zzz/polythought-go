package postgres

import (
	"context"
	"strings"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.WalletRepository = (*Wallets)(nil)

type Wallets struct {
	db *DB
}

func NewWallets(db *DB) *Wallets {
	return &Wallets{db: db}
}

func (r *Wallets) Create(ctx context.Context, w domain.Wallet) error {
	id, err := uuid.Parse(string(w.ID))
	if err != nil {
		return err
	}

	userID, err := uuid.Parse(string(w.UserID))
	if err != nil {
		return err
	}

	_, err = table.Wallets.INSERT(table.Wallets.AllColumns).MODEL(model.Wallets{
		ID:                id,
		UserID:            userID,
		Address:           string(w.Address),
		AddressNormalized: strings.ToLower(string(w.Address)),
		Label:             w.Label,
		CreatedAt:         w.CreatedAt,
	}).ExecContext(ctx, r.db.conn(ctx))
	if isUniqueViolation(err, "wallets_user_id_address_normalized_key") {
		return domain.ErrWalletExists
	}

	return err
}

// walletRow is a wallet with a flag for stored credentials, from a LEFT JOIN.
type walletRow struct {
	model.Wallets
	Credentials *struct {
		WalletID         uuid.UUID `sql:"primary_key" alias:"polymarket_credentials.wallet_id"`
		PrivateKeySealed *[]byte   `alias:"polymarket_credentials.private_key_sealed"`
		SessionKey       bool      `alias:"polymarket_credentials.session_key"`
	}
}

func (r *Wallets) selectWallets(where jet.BoolExpression) jet.SelectStatement {
	return jet.SELECT(table.Wallets.AllColumns, table.PolymarketCredentials.WalletID, table.PolymarketCredentials.PrivateKeySealed,
		table.PolymarketCredentials.SessionKey).
		FROM(table.Wallets.LEFT_JOIN(table.PolymarketCredentials, table.PolymarketCredentials.WalletID.EQ(table.Wallets.ID))).
		WHERE(where)
}

func (r *Wallets) ListForUser(ctx context.Context, userID domain.UserID) ([]domain.Wallet, error) {
	uid, err := parseID(userID)
	if err != nil {
		return nil, err
	}

	var rows []walletRow

	err = r.selectWallets(table.Wallets.UserID.EQ(jet.UUID(uid))).
		ORDER_BY(table.Wallets.ID.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Wallet, len(rows))
	for i, row := range rows {
		out[i] = toWallet(row)
	}

	return out, nil
}

func (r *Wallets) Get(ctx context.Context, userID domain.UserID, id domain.WalletID) (domain.Wallet, error) {
	uid, err := parseID(userID)
	if err != nil {
		return domain.Wallet{}, err
	}

	wid, err := parseID(id)
	if err != nil {
		return domain.Wallet{}, err
	}

	var row walletRow

	err = r.selectWallets(table.Wallets.ID.EQ(jet.UUID(wid)).AND(table.Wallets.UserID.EQ(jet.UUID(uid)))).
		QueryContext(ctx, r.db.conn(ctx), &row)
	if err != nil {
		return domain.Wallet{}, notFound(err)
	}

	return toWallet(row), nil
}

func (r *Wallets) CountForUser(ctx context.Context, userID domain.UserID) (int, error) {
	uid, err := parseID(userID)
	if err != nil {
		return 0, err
	}

	var out struct {
		Count int64
	}

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(table.Wallets).
		WHERE(table.Wallets.UserID.EQ(jet.UUID(uid))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *Wallets) Delete(ctx context.Context, userID domain.UserID, id domain.WalletID) error {
	uid, err := parseID(userID)
	if err != nil {
		return err
	}

	wid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.Wallets.DELETE().
		WHERE(table.Wallets.ID.EQ(jet.UUID(wid)).AND(table.Wallets.UserID.EQ(jet.UUID(uid)))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Wallets) SaveCredentials(ctx context.Context, c ports.SealedCredentials) error {
	wid, err := parseID(c.Info.WalletID)
	if err != nil {
		return err
	}

	m := model.PolymarketCredentials{
		WalletID:         wid,
		APIKeySealed:     c.APIKey,
		SecretSealed:     c.Secret,
		PassphraseSealed: c.Passphrase,
		PrivateKeySealed: optionalBytes(c.PrivateKey),
		APIKeyHint:       c.Info.APIKeyHint,
		SignerAddress:    string(c.Info.Signer),
		SignatureType:    int16(c.Info.SignatureType),
		SessionKey:       c.Info.SessionKey,
		CreatedAt:        c.Info.CreatedAt,
		UpdatedAt:        c.Info.UpdatedAt,
	}

	t := table.PolymarketCredentials

	_, err = t.INSERT(t.AllColumns).
		MODEL(m).
		ON_CONFLICT(t.WalletID).
		DO_UPDATE(jet.SET(
			t.APIKeySealed.SET(t.EXCLUDED.APIKeySealed),
			t.SecretSealed.SET(t.EXCLUDED.SecretSealed),
			t.PassphraseSealed.SET(t.EXCLUDED.PassphraseSealed),
			t.PrivateKeySealed.SET(t.EXCLUDED.PrivateKeySealed),
			t.APIKeyHint.SET(t.EXCLUDED.APIKeyHint),
			t.SignerAddress.SET(t.EXCLUDED.SignerAddress),
			t.SignatureType.SET(t.EXCLUDED.SignatureType),
			t.SessionKey.SET(t.EXCLUDED.SessionKey),
			t.UpdatedAt.SET(t.EXCLUDED.UpdatedAt),
		)).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Wallets) Credentials(ctx context.Context, walletID domain.WalletID) (ports.SealedCredentials, error) {
	wid, err := parseID(walletID)
	if err != nil {
		return ports.SealedCredentials{}, err
	}

	var m model.PolymarketCredentials

	err = jet.SELECT(table.PolymarketCredentials.AllColumns).
		FROM(table.PolymarketCredentials).
		WHERE(table.PolymarketCredentials.WalletID.EQ(jet.UUID(wid))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return ports.SealedCredentials{}, notFound(err)
	}

	return ports.SealedCredentials{
		Info: domain.CredentialsInfo{
			WalletID:      domain.WalletID(m.WalletID.String()),
			APIKeyHint:    m.APIKeyHint,
			Signer:        domain.WalletAddress(m.SignerAddress),
			SignatureType: domain.SignatureType(m.SignatureType),
			HasKey:        m.PrivateKeySealed != nil,
			SessionKey:    m.SessionKey,
			CreatedAt:     m.CreatedAt.UTC(),
			UpdatedAt:     m.UpdatedAt.UTC(),
		},
		APIKey:     m.APIKeySealed,
		Secret:     m.SecretSealed,
		Passphrase: m.PassphraseSealed,
		PrivateKey: derefBytes(m.PrivateKeySealed),
	}, nil
}

func optionalBytes(b []byte) *[]byte {
	if b == nil {
		return nil
	}

	return &b
}

func derefBytes(b *[]byte) []byte {
	if b == nil {
		return nil
	}

	return *b
}

func (r *Wallets) DeleteCredentials(ctx context.Context, walletID domain.WalletID) error {
	wid, err := parseID(walletID)
	if err != nil {
		return err
	}

	return affectedOne(table.PolymarketCredentials.DELETE().
		WHERE(table.PolymarketCredentials.WalletID.EQ(jet.UUID(wid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func toWallet(row walletRow) domain.Wallet {
	return domain.Wallet{
		ID:             domain.WalletID(row.ID.String()),
		UserID:         domain.UserID(row.UserID.String()),
		Address:        domain.WalletAddress(row.Address),
		Label:          row.Label,
		CreatedAt:      row.CreatedAt.UTC(),
		HasCredentials: row.Credentials != nil,
		HasKey:         row.Credentials != nil && row.Credentials.PrivateKeySealed != nil,
		HasSessionKey:  row.Credentials != nil && row.Credentials.SessionKey,
		Heartbeat:      row.Heartbeat,
	}
}

func (r *Wallets) SetHeartbeat(ctx context.Context, userID domain.UserID, id domain.WalletID, on bool) error {
	uid, err := parseID(userID)
	if err != nil {
		return err
	}

	wid, err := parseID(id)
	if err != nil {
		return err
	}

	w := table.Wallets

	return affectedOne(w.UPDATE(w.Heartbeat).SET(jet.Bool(on)).
		WHERE(w.ID.EQ(jet.UUID(wid)).AND(w.UserID.EQ(jet.UUID(uid)))).
		ExecContext(ctx, r.db.conn(ctx)))
}
