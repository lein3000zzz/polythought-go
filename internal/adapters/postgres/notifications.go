package postgres

import (
	"context"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.NotificationPrefRepository = (*NotificationPrefs)(nil)

type NotificationPrefs struct {
	db *DB
}

func NewNotificationPrefs(db *DB) *NotificationPrefs {
	return &NotificationPrefs{db: db}
}

func (r *NotificationPrefs) Prefs(ctx context.Context, user domain.UserID) (map[string]bool, error) {
	uid, err := parseID(user)
	if err != nil {
		return nil, err
	}

	var rows []model.NotificationPrefs

	err = jet.SELECT(table.NotificationPrefs.AllColumns).
		FROM(table.NotificationPrefs).
		WHERE(table.NotificationPrefs.UserID.EQ(jet.UUID(uid))).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(rows))
	for _, m := range rows {
		out[m.Kind] = m.Enabled
	}

	return out, nil
}

func (r *NotificationPrefs) SetPref(ctx context.Context, user domain.UserID, kind string, enabled bool) error {
	uid, err := parseID(user)
	if err != nil {
		return err
	}

	np := table.NotificationPrefs

	_, err = np.INSERT(np.AllColumns).
		MODEL(model.NotificationPrefs{UserID: uid, Kind: kind, Enabled: enabled}).
		ON_CONFLICT(np.UserID, np.Kind).
		DO_UPDATE(jet.SET(np.Enabled.SET(np.EXCLUDED.Enabled))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}
