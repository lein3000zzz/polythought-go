package postgres

import (
	"context"
	"encoding/json"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.ApprovalSetupRepository = (*Wallets)(nil)

// approvalJSON is an approval as approval_setups keeps it.
type approvalJSON struct {
	Token    string `json:"token"`
	Spender  string `json:"spender"`
	Standard string `json:"standard"`
	Name     string `json:"name,omitempty"`
}

func (r *Wallets) SaveApprovalSetup(ctx context.Context, s ports.StoredApprovalSetup) error {
	wid, err := parseID(s.Info.WalletID)
	if err != nil {
		return err
	}

	approvals := make([]approvalJSON, len(s.Approvals))
	for i, a := range s.Approvals {
		approvals[i] = approvalJSON{Token: string(a.Token), Spender: string(a.Spender), Standard: a.Standard, Name: a.Name}
	}

	raw, err := json.Marshal(approvals)
	if err != nil {
		return err
	}

	t := table.ApprovalSetups
	m := model.ApprovalSetups{
		WalletID:      wid,
		OwnerAddress:  string(s.Info.Owner),
		SignatureType: int16(s.Info.Kind),
		Approvals:     string(raw),
		Nonce:         s.Nonce,
		Deadline:      timePtr(s.Deadline),
		State:         string(s.Info.State),
		TransactionID: s.TransactionID,
		Error:         s.Info.Error,
		CreatedAt:     s.Info.CreatedAt,
		UpdatedAt:     s.Info.UpdatedAt,
	}

	_, err = t.INSERT(t.AllColumns).
		MODEL(m).
		ON_CONFLICT(t.WalletID).
		DO_UPDATE(jet.SET(
			t.OwnerAddress.SET(t.EXCLUDED.OwnerAddress),
			t.SignatureType.SET(t.EXCLUDED.SignatureType),
			t.Approvals.SET(t.EXCLUDED.Approvals),
			t.Nonce.SET(t.EXCLUDED.Nonce),
			t.Deadline.SET(t.EXCLUDED.Deadline),
			t.State.SET(t.EXCLUDED.State),
			t.TransactionID.SET(t.EXCLUDED.TransactionID),
			t.Error.SET(t.EXCLUDED.Error),
			t.CreatedAt.SET(t.EXCLUDED.CreatedAt),
			t.UpdatedAt.SET(t.EXCLUDED.UpdatedAt),
		)).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Wallets) ApprovalSetup(ctx context.Context, wallet domain.WalletID) (ports.StoredApprovalSetup, error) {
	wid, err := parseID(wallet)
	if err != nil {
		return ports.StoredApprovalSetup{}, err
	}

	var m model.ApprovalSetups

	err = jet.SELECT(table.ApprovalSetups.AllColumns).
		FROM(table.ApprovalSetups).
		WHERE(table.ApprovalSetups.WalletID.EQ(jet.UUID(wid))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return ports.StoredApprovalSetup{}, notFound(err)
	}

	return toStoredApprovalSetup(m)
}

func (r *Wallets) SubmittedApprovalSetups(ctx context.Context) ([]ports.StoredApprovalSetup, error) {
	t := table.ApprovalSetups

	var rows []model.ApprovalSetups

	err := jet.SELECT(t.AllColumns).
		FROM(t).
		WHERE(t.State.EQ(jet.String(string(domain.ApprovalSetupSubmitted)))).
		ORDER_BY(t.UpdatedAt.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ports.StoredApprovalSetup, len(rows))
	for i, m := range rows {
		if out[i], err = toStoredApprovalSetup(m); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func toStoredApprovalSetup(m model.ApprovalSetups) (ports.StoredApprovalSetup, error) {
	var raw []approvalJSON
	if err := json.Unmarshal([]byte(m.Approvals), &raw); err != nil {
		return ports.StoredApprovalSetup{}, err
	}

	approvals := make([]domain.Approval, len(raw))
	for i, a := range raw {
		approvals[i] = domain.Approval{Token: domain.WalletAddress(a.Token), Spender: domain.WalletAddress(a.Spender), Standard: a.Standard, Name: a.Name}
	}

	out := ports.StoredApprovalSetup{
		Info: domain.ApprovalSetup{
			WalletID:  domain.WalletID(m.WalletID.String()),
			Owner:     domain.WalletAddress(m.OwnerAddress),
			Kind:      domain.SignatureType(m.SignatureType),
			Granting:  len(approvals),
			State:     domain.ApprovalSetupState(m.State),
			Error:     m.Error,
			CreatedAt: m.CreatedAt.UTC(),
			UpdatedAt: m.UpdatedAt.UTC(),
		},
		Approvals:     approvals,
		Nonce:         m.Nonce,
		TransactionID: m.TransactionID,
	}

	if m.Deadline != nil {
		out.Deadline = m.Deadline.UTC()
	}

	return out, nil
}
