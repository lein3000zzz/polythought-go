package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.ModelRepository = (*Models)(nil)

// Models keeps users' models and their worker keys, each key by its secret's hash.
type Models struct {
	db *DB
}

func NewModels(db *DB) *Models {
	return &Models{db: db}
}

func (r *Models) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return r.db.conn(ctx).(interface {
		QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	}).QueryRowContext(ctx, q, args...)
}

// credential is a model's credential as the table holds it: NULL for none.
func credential(id domain.CredentialID) (*uuid.UUID, error) {
	if id == "" {
		return nil, nil
	}

	u, err := parseID(id)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

func (r *Models) Create(ctx context.Context, m domain.Model) error {
	cred, err := credential(m.Credential)
	if err != nil {
		return err
	}

	_, err = r.db.conn(ctx).ExecContext(ctx, `
		INSERT INTO models (id, owner_id, name, kind, url, credential_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		m.ID, m.OwnerID, m.Name, string(m.Kind), m.URL, cred, m.CreatedAt.UTC())

	switch {
	case isUniqueViolation(err, ""):
		return domain.ErrModelExists
	case isForeignKeyViolation(err):
		return domain.ErrNotFound // a credential gone meanwhile
	}

	return err
}

const modelColumns = `
	SELECT id, owner_id, name, kind, url, credential_id, last_answer_at, last_error, last_error_at, created_at, updated_at
	FROM models`

func scanModel(row interface{ Scan(...any) error }) (domain.Model, error) {
	var (
		m                    domain.Model
		id, owner            uuid.UUID
		kind                 string
		cred                 *uuid.UUID
		answered, errored    *time.Time
		created, updatedTime time.Time
	)

	err := row.Scan(&id, &owner, &m.Name, &kind, &m.URL, &cred, &answered, &m.LastError, &errored, &created, &updatedTime)
	if err != nil {
		return domain.Model{}, err
	}

	m.ID, m.OwnerID, m.Kind = domain.ModelID(id.String()), domain.UserID(owner.String()), domain.ModelKind(kind)
	m.CreatedAt, m.UpdatedAt = created.UTC(), updatedTime.UTC()

	if cred != nil {
		m.Credential = domain.CredentialID(cred.String())
	}

	if answered != nil {
		m.LastAnswerAt = answered.UTC()
	}

	if errored != nil {
		m.LastErrorAt = errored.UTC()
	}

	return m, nil
}

func (r *Models) List(ctx context.Context, owner domain.UserID) ([]domain.Model, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, nil
	}

	rows, err := r.db.conn(ctx).QueryContext(ctx, modelColumns+` WHERE owner_id = $1 ORDER BY name`, oid)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var out []domain.Model

	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, m)
	}

	return out, rows.Err()
}

func (r *Models) one(ctx context.Context, where string, args ...any) (domain.Model, error) {
	m, err := scanModel(r.queryRow(ctx, modelColumns+" WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Model{}, domain.ErrNotFound
	}

	return m, err
}

func (r *Models) Get(ctx context.Context, owner domain.UserID, name string) (domain.Model, error) {
	oid, err := parseID(owner)
	if err != nil {
		return domain.Model{}, err
	}

	return r.one(ctx, "owner_id = $1 AND name = $2", oid, name)
}

func (r *Models) ByID(ctx context.Context, id domain.ModelID) (domain.Model, error) {
	mid, err := parseID(id)
	if err != nil {
		return domain.Model{}, err
	}

	return r.one(ctx, "id = $1", mid)
}

func (r *Models) SetURL(ctx context.Context, id domain.ModelID, url string, credentialID domain.CredentialID, at time.Time) error {
	cred, err := credential(credentialID)
	if err != nil {
		return err
	}

	res, err := r.db.conn(ctx).ExecContext(ctx, `UPDATE models SET url = $2, credential_id = $3, updated_at = $4 WHERE id = $1`,
		id, url, cred, at.UTC())
	if isForeignKeyViolation(err) {
		return domain.ErrNotFound
	}

	return affectedOne(res, err)
}

func (r *Models) Delete(ctx context.Context, id domain.ModelID) error {
	return affectedOne(r.db.conn(ctx).ExecContext(ctx, `DELETE FROM models WHERE id = $1`, id))
}

func (r *Models) Answered(ctx context.Context, id domain.ModelID, at time.Time) error {
	_, err := r.db.conn(ctx).ExecContext(ctx, `UPDATE models SET last_answer_at = $2 WHERE id = $1`, id, at.UTC())
	return err
}

func (r *Models) Failed(ctx context.Context, id domain.ModelID, reason string, at time.Time) error {
	_, err := r.db.conn(ctx).ExecContext(ctx, `UPDATE models SET last_error = $2, last_error_at = $3 WHERE id = $1`, id, reason, at.UTC())
	return err
}

func (r *Models) CreateKey(ctx context.Context, k domain.WorkerKey, hash []byte) error {
	_, err := r.db.conn(ctx).ExecContext(ctx, `
		INSERT INTO model_keys (id, model_id, name, prefix, hash, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		k.ID, k.ModelID, k.Name, k.Prefix, hash, k.CreatedAt.UTC())
	if isForeignKeyViolation(err) {
		return domain.ErrNotFound
	}

	return err
}

func scanKey(row interface{ Scan(...any) error }, k *domain.WorkerKey) error {
	var (
		id, model uuid.UUID
		used      *time.Time
	)

	if err := row.Scan(&id, &model, &k.Name, &k.Prefix, &k.CreatedAt, &used); err != nil {
		return err
	}

	k.ID, k.ModelID, k.CreatedAt = domain.WorkerKeyID(id.String()), domain.ModelID(model.String()), k.CreatedAt.UTC()

	if used != nil {
		k.LastUsedAt = used.UTC()
	}

	return nil
}

func (r *Models) Keys(ctx context.Context, model domain.ModelID) ([]domain.WorkerKey, error) {
	mid, err := parseID(model)
	if err != nil {
		return nil, nil
	}

	rows, err := r.db.conn(ctx).QueryContext(ctx, `
		SELECT id, model_id, name, prefix, created_at, last_used_at FROM model_keys
		WHERE model_id = $1 ORDER BY created_at DESC, id`, mid)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var out []domain.WorkerKey

	for rows.Next() {
		var k domain.WorkerKey
		if err := scanKey(rows, &k); err != nil {
			return nil, err
		}

		out = append(out, k)
	}

	return out, rows.Err()
}

func (r *Models) KeyByHash(ctx context.Context, hash []byte) (domain.WorkerKey, domain.Model, error) {
	var k domain.WorkerKey

	err := scanKey(r.queryRow(ctx, `
		SELECT id, model_id, name, prefix, created_at, last_used_at FROM model_keys WHERE hash = $1`, hash), &k)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkerKey{}, domain.Model{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.WorkerKey{}, domain.Model{}, err
	}

	m, err := r.ByID(ctx, k.ModelID)

	return k, m, err
}

func (r *Models) KeyUsed(ctx context.Context, id domain.WorkerKeyID, at time.Time) error {
	_, err := r.db.conn(ctx).ExecContext(ctx, `UPDATE model_keys SET last_used_at = $2 WHERE id = $1`, id, at.UTC())
	return err
}

func (r *Models) DeleteKey(ctx context.Context, model domain.ModelID, id domain.WorkerKeyID) error {
	kid, err := parseID(id)
	if err != nil {
		return err
	}

	return affectedOne(r.db.conn(ctx).ExecContext(ctx, `DELETE FROM model_keys WHERE id = $1 AND model_id = $2`, kid, model))
}
