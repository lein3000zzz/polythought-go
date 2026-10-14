package postgres

import (
	"context"
	"encoding/json"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.SettingsRepository = (*Settings)(nil)

type Settings struct {
	db *DB
}

func NewSettings(db *DB) *Settings {
	return &Settings{db: db}
}

func (r *Settings) All(ctx context.Context) ([]ports.StoredSetting, error) {
	var rows []model.Settings

	err := jet.SELECT(table.Settings.AllColumns).
		FROM(table.Settings).
		ORDER_BY(table.Settings.Key).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ports.StoredSetting, len(rows))
	for i, m := range rows {
		out[i] = ports.StoredSetting{Key: m.Key, Value: rawJSON(m.Value)}
		if m.Sealed != nil {
			out[i].Sealed = *m.Sealed
		}
	}

	return out, nil
}

func (r *Settings) Apply(ctx context.Context, changes []ports.SettingChange, actor domain.UserID, at time.Time) error {
	aid, err := parseID(actor)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		for _, c := range changes {
			if err := r.apply(ctx, c, aid, at); err != nil {
				return err
			}

			audit := model.SettingsAudit{
				ID:        uuid.Must(uuid.NewV7()),
				Key:       c.Key,
				OldValue:  textJSON(c.OldValue),
				NewValue:  textJSON(c.NewValue),
				Secret:    c.Secret,
				ActorID:   &aid,
				ChangedAt: at,
			}

			_, err := table.SettingsAudit.INSERT(table.SettingsAudit.AllColumns).MODEL(audit).ExecContext(ctx, r.db.conn(ctx))
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func (r *Settings) apply(ctx context.Context, c ports.SettingChange, actor uuid.UUID, at time.Time) error {
	st := table.Settings

	if c.Reset {
		_, err := st.DELETE().WHERE(st.Key.EQ(jet.String(c.Key))).ExecContext(ctx, r.db.conn(ctx))
		return err
	}

	m := model.Settings{Key: c.Key, Value: textJSON(c.Value), UpdatedAt: at, UpdatedBy: &actor}
	if c.Sealed != nil {
		m.Sealed = &c.Sealed
	}

	_, err := st.INSERT(st.AllColumns).
		MODEL(m).
		ON_CONFLICT(st.Key).
		DO_UPDATE(jet.SET(
			st.Value.SET(st.EXCLUDED.Value),
			st.Sealed.SET(st.EXCLUDED.Sealed),
			st.UpdatedAt.SET(st.EXCLUDED.UpdatedAt),
			st.UpdatedBy.SET(st.EXCLUDED.UpdatedBy),
		)).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

// auditRow is a change with its actor's email, from a LEFT JOIN: the actor may be gone.
type auditRow struct {
	model.SettingsAudit
	Actor *struct {
		ID    uuid.UUID `sql:"primary_key" alias:"users.id"`
		Email string    `alias:"users.email"`
	}
}

func (r *Settings) History(ctx context.Context, f ports.PageFilter) (ports.Page[ports.SettingAudit], error) {
	limit := pageLimit(f.Limit)
	where := jet.Bool(true)

	if f.Cursor != "" {
		before, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[ports.SettingAudit]{}, domain.ErrInvalidInput
		}

		where = table.SettingsAudit.ID.LT(jet.UUID(before))
	}

	var rows []auditRow

	// IDs are UUIDv7, so descending IDs are newest first.
	err := jet.SELECT(table.SettingsAudit.AllColumns, table.Users.ID, table.Users.Email).
		FROM(table.SettingsAudit.LEFT_JOIN(table.Users, table.Users.ID.EQ(table.SettingsAudit.ActorID))).
		WHERE(where).
		ORDER_BY(table.SettingsAudit.ID.DESC()).
		LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return ports.Page[ports.SettingAudit]{}, err
	}

	page := ports.Page[ports.SettingAudit]{Items: make([]ports.SettingAudit, len(rows))}

	for i, m := range rows {
		a := ports.SettingAudit{
			ID:       m.ID.String(),
			Key:      m.Key,
			OldValue: rawJSON(m.OldValue),
			NewValue: rawJSON(m.NewValue),
			Secret:   m.Secret,
			At:       m.ChangedAt.UTC(),
		}

		if m.Actor != nil {
			a.ActorID = domain.UserID(m.Actor.ID.String())
			a.ActorEmail = m.Actor.Email
		}

		page.Items[i] = a
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

func textJSON(v json.RawMessage) *string {
	if v == nil {
		return nil
	}

	s := string(v)

	return &s
}

func rawJSON(s *string) json.RawMessage {
	if s == nil {
		return nil
	}

	return json.RawMessage(*s)
}
