package postgres

import (
	"context"
	"strings"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.UserRepository = (*Users)(nil)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

type Users struct {
	db *DB
}

func NewUsers(db *DB) *Users {
	return &Users{db: db}
}

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (r *Users) Create(ctx context.Context, u domain.User, passwordHash string) error {
	id, err := uuid.Parse(string(u.ID))
	if err != nil {
		return err
	}

	m := model.Users{
		ID:                id,
		Email:             u.Email,
		EmailNormalized:   normalizedEmail(u.Email),
		PasswordHash:      passwordHash,
		Role:              string(u.Role),
		EmailVerifiedAt:   timePtr(u.EmailVerifiedAt),
		DisabledAt:        timePtr(u.DisabledAt),
		CodeChosen:        u.CodeChosen,
		LiveStoppedAt:     timePtr(u.LiveStoppedAt),
		MaxLiveOrder:      capOf(u.LiveCaps.PerOrder),
		MaxLiveDay:        capOf(u.LiveCaps.PerDay),
		MaxAgentDollars:   u.AgentCaps.Dollars,
		MaxAgentInput:     u.AgentCaps.InputTokens,
		MaxAgentOutput:    u.AgentCaps.OutputTokens,
		MaxAgentTotal:     u.AgentCaps.TotalTokens,
		KillStopsAgents:   u.KillStopsAgents,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
		AgentsSeePersonal: u.Agents.SeePersonal,
		AgentsMayDelete:   u.Agents.Delete,
		AgentsMayShare:    u.Agents.Share,
	}

	_, err = table.Users.INSERT(table.Users.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))
	if isUniqueViolation(err, "users_email_normalized_key") {
		return domain.ErrEmailTaken
	}

	return err
}

func (r *Users) ByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	uid, err := parseID(id)
	if err != nil {
		return domain.User{}, err
	}

	return r.one(ctx, table.Users.ID.EQ(jet.UUID(uid)))
}

func (r *Users) ByEmail(ctx context.Context, email string) (domain.User, string, error) {
	var m model.Users

	err := jet.SELECT(table.Users.AllColumns).
		FROM(table.Users).
		WHERE(table.Users.EmailNormalized.EQ(jet.String(normalizedEmail(email)))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.User{}, "", notFound(err)
	}

	return toUser(m), m.PasswordHash, nil
}

func (r *Users) PasswordHash(ctx context.Context, id domain.UserID) (string, error) {
	uid, err := parseID(id)
	if err != nil {
		return "", err
	}

	var m model.Users

	err = jet.SELECT(table.Users.PasswordHash).
		FROM(table.Users).
		WHERE(table.Users.ID.EQ(jet.UUID(uid))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return "", notFound(err)
	}

	return m.PasswordHash, nil
}

func (r *Users) SetPasswordHash(ctx context.Context, id domain.UserID, hash string, at time.Time) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(table.Users.
		UPDATE(table.Users.PasswordHash, table.Users.UpdatedAt).
		SET(hash, at).
		WHERE(table.Users.ID.EQ(jet.UUID(uid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Users) Update(ctx context.Context, u domain.User) error {
	uid, err := parseID(u.ID)
	if err != nil {
		return err
	}

	t := table.Users

	return affectedOne(t.
		UPDATE(t.Role, t.DisabledAt, t.CodeChosen, t.MaxLiveOrder, t.MaxLiveDay,
			t.MaxAgentDollars, t.MaxAgentInput, t.MaxAgentOutput, t.MaxAgentTotal, t.UpdatedAt).
		MODEL(model.Users{
			Role: string(u.Role), DisabledAt: timePtr(u.DisabledAt), CodeChosen: u.CodeChosen,
			MaxLiveOrder: capOf(u.LiveCaps.PerOrder), MaxLiveDay: capOf(u.LiveCaps.PerDay),
			MaxAgentDollars: u.AgentCaps.Dollars, MaxAgentInput: u.AgentCaps.InputTokens,
			MaxAgentOutput: u.AgentCaps.OutputTokens, MaxAgentTotal: u.AgentCaps.TotalTokens, UpdatedAt: u.UpdatedAt,
		}).
		WHERE(t.ID.EQ(jet.UUID(uid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

// SetKillStopsAgents records the user's choice that their kill switch stops their agents
// too (D276).
func (r *Users) SetKillStopsAgents(ctx context.Context, id domain.UserID, on bool) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}

	t := table.Users

	return affectedOne(t.UPDATE(t.KillStopsAgents).
		SET(on).
		WHERE(t.ID.EQ(jet.UUID(uid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

// SetAgentAccess records what the user lets their agents and API tokens do (D401, D403).
func (r *Users) SetAgentAccess(ctx context.Context, id domain.UserID, a domain.AgentAccess) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}

	t := table.Users

	return affectedOne(t.UPDATE(t.AgentsSeePersonal, t.AgentsMayDelete, t.AgentsMayShare).
		MODEL(model.Users{AgentsSeePersonal: a.SeePersonal, AgentsMayDelete: a.Delete, AgentsMayShare: a.Share}).
		WHERE(t.ID.EQ(jet.UUID(uid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

// SetLiveStopped turns the user's kill switch on at at, or off with the zero time (D220).
func (r *Users) SetLiveStopped(ctx context.Context, id domain.UserID, at time.Time) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}

	t := table.Users

	return affectedOne(t.UPDATE(t.LiveStoppedAt).
		SET(timePtr(at)).
		WHERE(t.ID.EQ(jet.UUID(uid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func capOf(c *int) *int32 {
	if c == nil {
		return nil
	}

	v := int32(*c) //nolint:gosec // caps are checked to at most 10,000,000

	return &v
}

func capFrom(c *int32) *int {
	if c == nil {
		return nil
	}

	v := int(*c)

	return &v
}

// List pages by ID; UUIDv7 IDs sort by creation time.
func (r *Users) List(ctx context.Context, f ports.PageFilter) (ports.Page[domain.User], error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}

	limit = min(limit, maxPageSize)

	where := jet.Bool(true)

	if f.Cursor != "" {
		after, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.User]{}, domain.ErrInvalidInput
		}

		where = table.Users.ID.GT(jet.UUID(after))
	}

	var rows []model.Users

	err := jet.SELECT(table.Users.AllColumns).
		FROM(table.Users).
		WHERE(where).
		ORDER_BY(table.Users.ID.ASC()).
		LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return ports.Page[domain.User]{}, err
	}

	page := ports.Page[domain.User]{Items: make([]domain.User, len(rows))}
	for i, m := range rows {
		page.Items[i] = toUser(m)
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

func (r *Users) CountActiveAdmins(ctx context.Context) (int, error) {
	var out struct {
		Count int64
	}

	err := jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(table.Users).
		WHERE(table.Users.Role.EQ(jet.String(string(domain.RoleAdmin))).AND(table.Users.DisabledAt.IS_NULL())).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

// Admins are the instance's admins who are not disabled, for what only they are told.
func (r *Users) Admins(ctx context.Context) ([]domain.User, error) {
	var rows []model.Users

	err := table.Users.SELECT(table.Users.AllColumns).
		FROM(table.Users).
		WHERE(table.Users.Role.EQ(jet.String(string(domain.RoleAdmin))).AND(table.Users.DisabledAt.IS_NULL())).
		ORDER_BY(table.Users.CreatedAt).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.User, len(rows))
	for i, row := range rows {
		out[i] = toUser(row)
	}

	return out, nil
}

// LockActiveAdmins uses SELECT ... FOR UPDATE: a concurrent transaction changing an admin
// waits, then sees this one's result, so two demotions cannot both pass a "not the last
// admin" check.
func (r *Users) LockActiveAdmins(ctx context.Context) (int, error) {
	var rows []model.Users

	err := jet.SELECT(table.Users.ID).
		FROM(table.Users).
		WHERE(table.Users.Role.EQ(jet.String(string(domain.RoleAdmin))).AND(table.Users.DisabledAt.IS_NULL())).
		FOR(jet.UPDATE()).
		QueryContext(ctx, r.db.conn(ctx), &rows)

	return len(rows), err
}

func (r *Users) one(ctx context.Context, where jet.BoolExpression) (domain.User, error) {
	var m model.Users

	err := jet.SELECT(table.Users.AllColumns).FROM(table.Users).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.User{}, notFound(err)
	}

	return toUser(m), nil
}

func toUser(m model.Users) domain.User {
	return domain.User{
		ID:              domain.UserID(m.ID.String()),
		Email:           m.Email,
		Role:            domain.Role(m.Role),
		EmailVerifiedAt: timeOf(m.EmailVerifiedAt),
		DisabledAt:      timeOf(m.DisabledAt),
		CodeChosen:      m.CodeChosen,
		LiveStoppedAt:   timeOf(m.LiveStoppedAt),
		LiveCaps:        domain.LiveCaps{PerOrder: capFrom(m.MaxLiveOrder), PerDay: capFrom(m.MaxLiveDay)},
		AgentCaps: domain.AgentCaps{
			Dollars: m.MaxAgentDollars, InputTokens: m.MaxAgentInput, OutputTokens: m.MaxAgentOutput, TotalTokens: m.MaxAgentTotal,
		},
		KillStopsAgents: m.KillStopsAgents,
		Agents:          domain.AgentAccess{SeePersonal: m.AgentsSeePersonal, Delete: m.AgentsMayDelete, Share: m.AgentsMayShare},
		CreatedAt:       m.CreatedAt.UTC(),
		UpdatedAt:       m.UpdatedAt.UTC(),
	}
}

func (r *Users) SetEmailVerified(ctx context.Context, id domain.UserID, at time.Time) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}

	u := table.Users

	return affectedOne(u.UPDATE(u.EmailVerifiedAt, u.UpdatedAt).
		SET(jet.TimestampzT(at), jet.TimestampzT(at)).
		WHERE(u.ID.EQ(jet.UUID(uid))).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Users) DeleteUnverified(ctx context.Context, id domain.UserID) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}

	u := table.Users

	return affectedOne(u.DELETE().
		WHERE(u.ID.EQ(jet.UUID(uid)).AND(u.EmailVerifiedAt.IS_NULL())).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Users) PruneUnverified(ctx context.Context, before time.Time) (int64, error) {
	u := table.Users

	res, err := u.DELETE().
		WHERE(u.EmailVerifiedAt.IS_NULL().AND(u.CreatedAt.LT(jet.TimestampzT(before)))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
