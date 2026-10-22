package postgres

import (
	"context"
	"encoding/json"
	"errors"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.PluginSettings = (*Plugins)(nil)

func (r *Plugins) Settings(ctx context.Context, plugin string) ([]ports.StoredPluginSetting, error) {
	var rows []model.PluginSettings

	s := table.PluginSettings

	err := s.SELECT(s.Key, s.Value, s.Sealed).FROM(s).WHERE(s.PluginID.EQ(jet.String(plugin))).ORDER_BY(s.Key).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]ports.StoredPluginSetting, len(rows))
	for i, row := range rows {
		out[i] = ports.StoredPluginSetting{Key: row.Key}

		if row.Value != nil {
			out[i].Value = json.RawMessage(*row.Value)
		}

		if row.Sealed != nil {
			out[i].Sealed = *row.Sealed
		}
	}

	return out, nil
}

func (r *Plugins) SetSettings(ctx context.Context, plugin string, changes []ports.PluginSettingChange, a domain.PluginAudit) error {
	by, err := optionalID(a.By)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		s := table.PluginSettings

		for _, c := range changes {
			where := s.PluginID.EQ(jet.String(plugin)).AND(s.Key.EQ(jet.String(c.Key)))

			if c.Value == nil && c.Sealed == nil {
				if _, err := s.DELETE().WHERE(where).ExecContext(ctx, r.db.conn(ctx)); err != nil {
					return err
				}

				continue
			}

			row := model.PluginSettings{PluginID: plugin, Key: c.Key, UpdatedAt: a.At, UpdatedBy: by}

			if c.Value != nil {
				v := string(c.Value)
				row.Value = &v
			} else {
				sealed := c.Sealed
				row.Sealed = &sealed
			}

			_, err := s.INSERT(s.AllColumns).MODEL(row).
				ON_CONFLICT(s.PluginID, s.Key).
				DO_UPDATE(jet.SET(s.Value.SET(s.EXCLUDED.Value), s.Sealed.SET(s.EXCLUDED.Sealed),
					s.UpdatedAt.SET(s.EXCLUDED.UpdatedAt), s.UpdatedBy.SET(s.EXCLUDED.UpdatedBy))).
				ExecContext(ctx, r.db.conn(ctx))
			if err != nil {
				return err
			}
		}

		return r.audit(ctx, a)
	})
}
