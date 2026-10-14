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

var _ ports.TelegramLinkRepository = (*TelegramLinks)(nil)

type TelegramLinks struct {
	db *DB
}

func NewTelegramLinks(db *DB) *TelegramLinks {
	return &TelegramLinks{db: db}
}

func (r *TelegramLinks) ForUser(ctx context.Context, user domain.UserID) (domain.TelegramLink, error) {
	uid, err := parseID(user)
	if err != nil {
		return domain.TelegramLink{}, err
	}

	return r.one(ctx, table.TelegramLinks.UserID.EQ(jet.UUID(uid)))
}

func (r *TelegramLinks) ForTelegramUser(ctx context.Context, telegramUserID int64) (domain.TelegramLink, error) {
	return r.one(ctx, table.TelegramLinks.TelegramUserID.EQ(jet.Int64(telegramUserID)))
}

func (r *TelegramLinks) one(ctx context.Context, where jet.BoolExpression) (domain.TelegramLink, error) {
	var m model.TelegramLinks

	err := jet.SELECT(table.TelegramLinks.AllColumns).FROM(table.TelegramLinks).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.TelegramLink{}, notFound(err)
	}

	return domain.TelegramLink{
		UserID: domain.UserID(m.UserID.String()), TelegramUserID: m.TelegramUserID, ChatID: m.ChatID,
		Username: m.Username, FirstName: m.FirstName, LinkedAt: m.LinkedAt.UTC(),
	}, nil
}

func (r *TelegramLinks) Save(ctx context.Context, link domain.TelegramLink) error {
	uid, err := parseID(link.UserID)
	if err != nil {
		return err
	}

	tl := table.TelegramLinks
	m := model.TelegramLinks{
		UserID: uid, TelegramUserID: link.TelegramUserID, ChatID: link.ChatID,
		Username: link.Username, FirstName: link.FirstName, LinkedAt: link.LinkedAt,
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		// The Telegram user may have linked another account before: it moves here.
		_, err := tl.DELETE().
			WHERE(tl.TelegramUserID.EQ(jet.Int64(link.TelegramUserID)).AND(tl.UserID.NOT_EQ(jet.UUID(uid)))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		_, err = tl.INSERT(tl.AllColumns).
			MODEL(m).
			ON_CONFLICT(tl.UserID).
			DO_UPDATE(jet.SET(
				tl.TelegramUserID.SET(tl.EXCLUDED.TelegramUserID),
				tl.ChatID.SET(tl.EXCLUDED.ChatID),
				tl.Username.SET(tl.EXCLUDED.Username),
				tl.FirstName.SET(tl.EXCLUDED.FirstName),
				tl.LinkedAt.SET(tl.EXCLUDED.LinkedAt),
			)).
			ExecContext(ctx, r.db.conn(ctx))

		return err
	})
}

func (r *TelegramLinks) Delete(ctx context.Context, user domain.UserID) error {
	uid, err := parseID(user)
	if err != nil {
		return err
	}

	return affectedOne(table.TelegramLinks.DELETE().WHERE(table.TelegramLinks.UserID.EQ(jet.UUID(uid))).ExecContext(ctx, r.db.conn(ctx)))
}

func (r *TelegramLinks) SaveCode(ctx context.Context, user domain.UserID, codeHash []byte, expiresAt time.Time) error {
	uid, err := parseID(user)
	if err != nil {
		return err
	}

	tc := table.TelegramLinkCodes

	_, err = tc.INSERT(tc.AllColumns).
		MODEL(model.TelegramLinkCodes{CodeHash: codeHash, UserID: uid, ExpiresAt: expiresAt}).
		ON_CONFLICT(tc.UserID).
		DO_UPDATE(jet.SET(tc.CodeHash.SET(tc.EXCLUDED.CodeHash), tc.ExpiresAt.SET(tc.EXCLUDED.ExpiresAt))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *TelegramLinks) ConsumeCode(ctx context.Context, codeHash []byte, now time.Time) (domain.UserID, error) {
	tc := table.TelegramLinkCodes

	var m model.TelegramLinkCodes

	// Deleted whether or not it expired: either way it cannot be used again.
	err := tc.DELETE().
		WHERE(tc.CodeHash.EQ(jet.Bytea(codeHash))).
		RETURNING(tc.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return "", notFound(err)
	}

	if !m.ExpiresAt.After(now) {
		return "", domain.ErrNotFound
	}

	return domain.UserID(m.UserID.String()), nil
}
