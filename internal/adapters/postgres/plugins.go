package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.PluginRepository = (*Plugins)(nil)

// Plugins keeps what admins decided about plugins (D321) in plugins, plugin_versions,
// plugin_users and plugin_audit; and forgets what plugins' nodes stored (D348).
type Plugins struct {
	db *DB
}

func NewPlugins(db *DB) *Plugins {
	return &Plugins{db: db}
}

func (r *Plugins) List(ctx context.Context) ([]domain.InstalledPlugin, error) {
	var rows []model.Plugins

	p := table.Plugins
	if err := p.SELECT(p.AllColumns).FROM(p).ORDER_BY(p.ID).QueryContext(ctx, r.db.conn(ctx), &rows); err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.InstalledPlugin, 0, len(rows))

	for _, row := range rows {
		ip, err := r.complete(ctx, row)
		if err != nil {
			return nil, err
		}

		out = append(out, ip)
	}

	return out, nil
}

func (r *Plugins) Get(ctx context.Context, id string) (domain.InstalledPlugin, error) {
	var row model.Plugins

	p := table.Plugins

	err := p.SELECT(p.AllColumns).FROM(p).WHERE(p.ID.EQ(jet.String(id))).QueryContext(ctx, r.db.conn(ctx), &row)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.InstalledPlugin{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.InstalledPlugin{}, err
	}

	return r.complete(ctx, row)
}

// complete is a plugin's row with its versions and users.
func (r *Plugins) complete(ctx context.Context, row model.Plugins) (domain.InstalledPlugin, error) {
	ip := domain.InstalledPlugin{
		ID: row.ID, Enabled: row.Enabled, Everyone: row.Everyone, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}

	if row.Version != nil {
		ip.Version = *row.Version
	}

	if row.DataCap != nil {
		ip.Caps.DataMiB = int(*row.DataCap)
	}

	if row.MemoryCap != nil {
		ip.Caps.MemoryMiB = int(*row.MemoryCap)
	}

	var versions []model.PluginVersions

	v := table.PluginVersions

	err := v.SELECT(v.AllColumns).FROM(v).WHERE(v.PluginID.EQ(jet.String(row.ID))).ORDER_BY(v.InstalledAt.DESC(), v.Version.DESC()).
		QueryContext(ctx, r.db.conn(ctx), &versions)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return ip, err
	}

	for _, pv := range versions {
		out := domain.PluginVersion{
			PluginID: pv.PluginID, Version: pv.Version, Manifest: json.RawMessage(pv.Manifest), Signer: pv.Signer, KeyID: pv.KeyID,
			InstalledBy: uuidString[domain.UserID](pv.InstalledBy), InstalledAt: pv.InstalledAt,
			ApprovedBy: uuidString[domain.UserID](pv.ApprovedBy),
		}

		if pv.ApprovedAt != nil {
			out.ApprovedAt = *pv.ApprovedAt
		}

		ip.Versions = append(ip.Versions, out)
	}

	var users []model.PluginUsers

	u := table.PluginUsers
	if err := u.SELECT(u.UserID).FROM(u).WHERE(u.PluginID.EQ(jet.String(row.ID))).QueryContext(ctx, r.db.conn(ctx), &users); err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return ip, err
	}

	for _, pu := range users {
		ip.Users = append(ip.Users, domain.UserID(pu.UserID.String()))
	}

	slices.Sort(ip.Users)

	return ip, nil
}

func (r *Plugins) AddVersion(ctx context.Context, pv domain.PluginVersion, a domain.PluginAudit) error {
	by, err := optionalID(pv.InstalledBy)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		p := table.Plugins

		_, err := p.INSERT(p.ID, p.Enabled, p.Everyone, p.CreatedAt, p.UpdatedAt).
			VALUES(pv.PluginID, true, true, pv.InstalledAt, pv.InstalledAt).
			ON_CONFLICT(p.ID).DO_NOTHING().
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		v := table.PluginVersions
		row := model.PluginVersions{
			PluginID: pv.PluginID, Version: pv.Version, Manifest: string(pv.Manifest), Signer: pv.Signer, KeyID: pv.KeyID,
			InstalledBy: by, InstalledAt: pv.InstalledAt,
		}

		_, err = v.INSERT(v.PluginID, v.Version, v.Manifest, v.Signer, v.KeyID, v.InstalledBy, v.InstalledAt).MODEL(row).
			ExecContext(ctx, r.db.conn(ctx))
		if isUniqueViolation(err, "plugin_versions_pkey") {
			return domain.ErrPluginInstalled
		}

		if err != nil {
			return err
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) Approve(ctx context.Context, a domain.PluginAudit) error {
	by, err := optionalID(a.By)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		v := table.PluginVersions

		res, err := v.UPDATE(v.ApprovedBy, v.ApprovedAt).SET(by, a.At).
			WHERE(v.PluginID.EQ(jet.String(a.PluginID)).AND(v.Version.EQ(jet.String(a.Version)))).
			ExecContext(ctx, r.db.conn(ctx))
		if err := oneRow(res, err); err != nil {
			return err
		}

		p := table.Plugins
		if _, err := p.UPDATE(p.Version, p.UpdatedAt).SET(a.Version, a.At).WHERE(p.ID.EQ(jet.String(a.PluginID))).
			ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) RemoveVersion(ctx context.Context, a domain.PluginAudit) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		v := table.PluginVersions

		res, err := v.DELETE().WHERE(v.PluginID.EQ(jet.String(a.PluginID)).AND(v.Version.EQ(jet.String(a.Version)))).
			ExecContext(ctx, r.db.conn(ctx))
		if err := oneRow(res, err); err != nil {
			return err
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) SetEnabled(ctx context.Context, enabled bool, a domain.PluginAudit) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		p := table.Plugins

		res, err := p.UPDATE(p.Enabled, p.UpdatedAt).SET(enabled, a.At).WHERE(p.ID.EQ(jet.String(a.PluginID))).
			ExecContext(ctx, r.db.conn(ctx))
		if err := oneRow(res, err); err != nil {
			return err
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) SetUsers(ctx context.Context, everyone bool, users []domain.UserID, a domain.PluginAudit) error {
	ids := make([]uuid.UUID, 0, len(users))

	for _, u := range users {
		id, err := parseID(u)
		if err != nil {
			return err
		}

		ids = append(ids, id)
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		p, u := table.Plugins, table.PluginUsers

		res, err := p.UPDATE(p.Everyone, p.UpdatedAt).SET(everyone, a.At).WHERE(p.ID.EQ(jet.String(a.PluginID))).
			ExecContext(ctx, r.db.conn(ctx))
		if err := oneRow(res, err); err != nil {
			return err
		}

		if _, err := u.DELETE().WHERE(u.PluginID.EQ(jet.String(a.PluginID))).ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		if !everyone && len(ids) > 0 {
			rows := make([]model.PluginUsers, len(ids))
			for i, id := range ids {
				rows[i] = model.PluginUsers{PluginID: a.PluginID, UserID: id}
			}

			if _, err := u.INSERT(u.AllColumns).MODELS(rows).ON_CONFLICT(u.PluginID, u.UserID).DO_NOTHING().
				ExecContext(ctx, r.db.conn(ctx)); err != nil {
				return err
			}
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) SetCaps(ctx context.Context, caps domain.PluginCaps, a domain.PluginAudit) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		p := table.Plugins

		res, err := p.UPDATE(p.DataCap, p.MemoryCap, p.UpdatedAt).
			MODEL(model.Plugins{DataCap: mibOf(caps.DataMiB), MemoryCap: mibOf(caps.MemoryMiB), UpdatedAt: a.At}).
			WHERE(p.ID.EQ(jet.String(a.PluginID))).
			ExecContext(ctx, r.db.conn(ctx))
		if err := oneRow(res, err); err != nil {
			return err
		}

		return r.audit(ctx, a)
	})
}

// mibOf is a cap as its column keeps it: NULL for the Config page's.
func mibOf(mib int) *int32 {
	if mib <= 0 {
		return nil
	}

	c := int32(min(mib, 1<<30)) //nolint:gosec // capped

	return &c
}

func (r *Plugins) Delete(ctx context.Context, a domain.PluginAudit, stores bool) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		p, s := table.Plugins, table.PluginStore

		res, err := p.DELETE().WHERE(p.ID.EQ(jet.String(a.PluginID))).ExecContext(ctx, r.db.conn(ctx))
		if err := oneRow(res, err); err != nil {
			return err
		}

		if stores {
			if _, err := s.DELETE().WHERE(s.PluginID.EQ(jet.String(a.PluginID))).ExecContext(ctx, r.db.conn(ctx)); err != nil {
				return err
			}
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) LeftStores(ctx context.Context) (map[string]int64, error) {
	s, p := table.PluginStore, table.Plugins

	var rows []struct {
		Plugin string `alias:"plugin"`
		Bytes  int64  `alias:"bytes"`
	}

	err := jet.SELECT(s.PluginID.AS("plugin"), jet.SUM(s.Size).AS("bytes")).
		FROM(s).
		WHERE(s.PluginID.NOT_IN(p.SELECT(p.ID))).
		GROUP_BY(s.PluginID).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Plugin] = row.Bytes
	}

	return out, nil
}

func (r *Plugins) ForgetStores(ctx context.Context, a domain.PluginAudit) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		s, p := table.PluginStore, table.Plugins

		// Only a plugin no longer installed: an installed one's nodes still use theirs.
		_, err := s.DELETE().
			WHERE(s.PluginID.EQ(jet.String(a.PluginID)).AND(s.PluginID.NOT_IN(p.SELECT(p.ID)))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		return r.audit(ctx, a)
	})
}

func (r *Plugins) Record(ctx context.Context, a domain.PluginAudit) error {
	return r.audit(ctx, a)
}

func (r *Plugins) History(ctx context.Context, id string, limit int) ([]domain.PluginAudit, error) {
	var rows []model.PluginAudit

	a := table.PluginAudit

	err := a.SELECT(a.AllColumns).FROM(a).WHERE(a.PluginID.EQ(jet.String(id))).ORDER_BY(a.At.DESC(), a.ID.DESC()).LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.PluginAudit, len(rows))
	for i, row := range rows {
		out[i] = domain.PluginAudit{
			PluginID: row.PluginID, Version: row.Version, Action: row.Action, Detail: row.Detail,
			By: uuidString[domain.UserID](row.ActorID), At: row.At,
		}
	}

	return out, nil
}

func (r *Plugins) audit(ctx context.Context, a domain.PluginAudit) error {
	by, err := optionalID(a.By)
	if err != nil {
		return err
	}

	row := model.PluginAudit{
		ID: uuid.Must(uuid.NewV7()), PluginID: a.PluginID, Version: a.Version, Action: a.Action, Detail: a.Detail,
		ActorID: by, At: a.At,
	}

	_, err = table.PluginAudit.INSERT(table.PluginAudit.AllColumns).MODEL(row).ExecContext(ctx, r.db.conn(ctx))

	return err
}

// optionalID is a user's ID for a column that may be empty: the server's own doing.
func optionalID(u domain.UserID) (*uuid.UUID, error) {
	if u == "" {
		return nil, nil //nolint:nilnil // no user, no error
	}

	id, err := parseID(u)
	if err != nil {
		return nil, err
	}

	return &id, nil
}

// oneRow says a change hit its row: domain.ErrNotFound when there was none.
func oneRow(res interface{ RowsAffected() (int64, error) }, err error) error {
	if err != nil {
		return err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return domain.ErrNotFound
	}

	return nil
}
