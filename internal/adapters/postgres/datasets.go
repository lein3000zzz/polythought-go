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

var _ ports.DatasetRepository = (*Datasets)(nil)

// Datasets keeps users' datasets and their rows. A row staged for a replace has its
// stage; the dataset's own have none.
type Datasets struct {
	db *DB
}

func NewDatasets(db *DB) *Datasets {
	return &Datasets{db: db}
}

// queryRow is one row's query, on the transaction in ctx or the pool: both take one.
func (r *Datasets) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return r.db.conn(ctx).(interface {
		QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	}).QueryRowContext(ctx, q, args...)
}

func (r *Datasets) Create(ctx context.Context, d domain.Dataset) error {
	_, err := r.db.conn(ctx).ExecContext(ctx, `
		INSERT INTO datasets (id, owner_id, name, key_field, keep_days, keep_rows, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		d.ID, d.OwnerID, d.Name, d.KeyField, d.KeepDays, d.KeepRows, d.CreatedAt.UTC())
	if isUniqueViolation(err, "") {
		return domain.ErrDatasetExists
	}

	return err
}

const datasetColumns = `
	SELECT d.id, d.owner_id, d.name, d.key_field, d.keep_days, d.keep_rows, d.created_at, d.updated_at,
	       COUNT(r.seq), COALESCE(SUM(r.size), 0)
	FROM datasets d
	LEFT JOIN dataset_rows r ON r.dataset_id = d.id AND r.stage_id IS NULL`

func scanDataset(row interface{ Scan(...any) error }) (domain.Dataset, error) {
	var (
		d        domain.Dataset
		id, ownr uuid.UUID
	)

	err := row.Scan(&id, &ownr, &d.Name, &d.KeyField, &d.KeepDays, &d.KeepRows, &d.CreatedAt, &d.UpdatedAt, &d.Rows, &d.Bytes)
	if err != nil {
		return domain.Dataset{}, err
	}

	d.ID, d.OwnerID = domain.DatasetID(id.String()), domain.UserID(ownr.String())
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()

	return d, nil
}

func (r *Datasets) List(ctx context.Context, owner domain.UserID) ([]domain.Dataset, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, nil
	}

	rows, err := r.db.conn(ctx).QueryContext(ctx, datasetColumns+` WHERE d.owner_id = $1 GROUP BY d.id ORDER BY d.name`, oid)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var out []domain.Dataset

	for rows.Next() {
		d, err := scanDataset(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, d)
	}

	return out, rows.Err()
}

func (r *Datasets) Get(ctx context.Context, owner domain.UserID, name string) (domain.Dataset, error) {
	oid, err := parseID(owner)
	if err != nil {
		return domain.Dataset{}, domain.ErrNotFound
	}

	d, err := scanDataset(r.queryRow(ctx,
		datasetColumns+` WHERE d.owner_id = $1 AND d.name = $2 GROUP BY d.id`, oid, name))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dataset{}, domain.ErrNotFound
	}

	return d, err
}

func (r *Datasets) SetKeep(ctx context.Context, id domain.DatasetID, days, rows int, at time.Time) error {
	res, err := r.db.conn(ctx).ExecContext(ctx,
		`UPDATE datasets SET keep_days = $2, keep_rows = $3, updated_at = $4 WHERE id = $1`, id, days, rows, at.UTC())

	return affectedOne(res, err)
}

func (r *Datasets) Delete(ctx context.Context, id domain.DatasetID) error {
	res, err := r.db.conn(ctx).ExecContext(ctx, `DELETE FROM datasets WHERE id = $1`, id)
	return affectedOne(res, err)
}

func (r *Datasets) Rows(ctx context.Context, id domain.DatasetID, after int64, limit int) ([]domain.DatasetRow, error) {
	rows, err := r.db.conn(ctx).QueryContext(ctx, `
		SELECT seq, key, at, row FROM dataset_rows
		WHERE dataset_id = $1 AND stage_id IS NULL AND seq > $2
		ORDER BY seq LIMIT $3`, id, after, limit)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var out []domain.DatasetRow

	for rows.Next() {
		var row domain.DatasetRow
		if err := rows.Scan(&row.Seq, &row.Key, &row.At, &row.Row); err != nil {
			return nil, err
		}

		row.At = row.At.UTC()
		out = append(out, row)
	}

	return out, rows.Err()
}

func (r *Datasets) Latest(ctx context.Context, id domain.DatasetID, key string) (domain.DatasetRow, error) {
	var row domain.DatasetRow

	err := r.queryRow(ctx, `
		SELECT seq, key, at, row FROM dataset_rows
		WHERE dataset_id = $1 AND stage_id IS NULL AND key = $2
		ORDER BY seq DESC LIMIT 1`, id, key).Scan(&row.Seq, &row.Key, &row.At, &row.Row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DatasetRow{}, domain.ErrNotFound
	}

	row.At = row.At.UTC()

	return row, err
}

func (r *Datasets) insert(ctx context.Context, id domain.DatasetID, stage *string, rows []ports.NewRow) error {
	for _, row := range rows {
		_, err := r.db.conn(ctx).ExecContext(ctx, `
			INSERT INTO dataset_rows (dataset_id, stage_id, key, at, row, size) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, stage, row.Key, row.At.UTC(), string(row.Row), len(row.Row))
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *Datasets) Append(ctx context.Context, id domain.DatasetID, rows []ports.NewRow, keep int, at time.Time) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		// One append at a time a dataset, so the oldest dropped are the oldest.
		if _, err := r.db.conn(ctx).ExecContext(ctx, `SELECT 1 FROM datasets WHERE id = $1 FOR UPDATE`, id); err != nil {
			return err
		}

		if err := r.insert(ctx, id, nil, rows); err != nil {
			return err
		}

		if keep > 0 {
			_, err := r.db.conn(ctx).ExecContext(ctx, `
				DELETE FROM dataset_rows WHERE dataset_id = $1 AND stage_id IS NULL AND seq <= (
					SELECT seq FROM dataset_rows WHERE dataset_id = $1 AND stage_id IS NULL
					ORDER BY seq DESC OFFSET $2 LIMIT 1)`, id, keep)
			if err != nil {
				return err
			}
		}

		_, err := r.db.conn(ctx).ExecContext(ctx, `UPDATE datasets SET updated_at = $2 WHERE id = $1`, id, at.UTC())

		return err
	})
}

func (r *Datasets) Stage(ctx context.Context, id domain.DatasetID, at time.Time) (ports.DatasetStageID, error) {
	stage := uuid.Must(uuid.NewV7()).String()

	_, err := r.db.conn(ctx).ExecContext(ctx,
		`INSERT INTO dataset_stages (id, dataset_id, created_at) VALUES ($1, $2, $3)`, stage, id, at.UTC())

	return ports.DatasetStageID(stage), err
}

// ownStage checks the stage is the dataset's.
func (r *Datasets) ownStage(ctx context.Context, id domain.DatasetID, stage ports.DatasetStageID) error {
	if _, err := uuid.Parse(string(stage)); err != nil {
		return domain.ErrNotFound
	}

	var n int

	err := r.queryRow(ctx,
		`SELECT COUNT(*) FROM dataset_stages WHERE id = $1 AND dataset_id = $2`, string(stage), id).Scan(&n)
	if err == nil && n == 0 {
		return domain.ErrNotFound
	}

	return err
}

func (r *Datasets) StageRows(ctx context.Context, id domain.DatasetID, stage ports.DatasetStageID, rows []ports.NewRow) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.ownStage(ctx, id, stage); err != nil {
			return err
		}

		s := string(stage)

		return r.insert(ctx, id, &s, rows)
	})
}

func (r *Datasets) StageSize(ctx context.Context, id domain.DatasetID, stage ports.DatasetStageID) (int, int64, error) {
	if err := r.ownStage(ctx, id, stage); err != nil {
		return 0, 0, err
	}

	var (
		rows  int
		bytes int64
	)

	err := r.queryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM dataset_rows WHERE stage_id = $1`, string(stage)).Scan(&rows, &bytes)

	return rows, bytes, err
}

func (r *Datasets) Swap(ctx context.Context, id domain.DatasetID, stage ports.DatasetStageID, at time.Time) error {
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.ownStage(ctx, id, stage); err != nil {
			return err
		}

		steps := []struct {
			q    string
			args []any
		}{
			{`DELETE FROM dataset_rows WHERE dataset_id = $1 AND stage_id IS NULL`, []any{id}},
			{`UPDATE dataset_rows SET stage_id = NULL WHERE dataset_id = $1 AND stage_id = $2`, []any{id, string(stage)}},
			{`DELETE FROM dataset_stages WHERE id = $1`, []any{string(stage)}},
			{`UPDATE datasets SET updated_at = $2 WHERE id = $1`, []any{id, at.UTC()}},
		}

		for _, st := range steps {
			if _, err := r.db.conn(ctx).ExecContext(ctx, st.q, st.args...); err != nil {
				return err
			}
		}

		return nil
	})
}

func (r *Datasets) Unstage(ctx context.Context, id domain.DatasetID, stage ports.DatasetStageID) error {
	if _, err := uuid.Parse(string(stage)); err != nil {
		return domain.ErrNotFound
	}

	res, err := r.db.conn(ctx).ExecContext(ctx, `DELETE FROM dataset_stages WHERE id = $1 AND dataset_id = $2`, string(stage), id)

	return affectedOne(res, err)
}

func (r *Datasets) Bytes(ctx context.Context, owner domain.UserID) (int64, error) {
	oid, err := parseID(owner)
	if err != nil {
		return 0, nil
	}

	var n int64

	err = r.queryRow(ctx, `
		SELECT COALESCE(SUM(r.size), 0) FROM dataset_rows r JOIN datasets d ON d.id = r.dataset_id
		WHERE d.owner_id = $1`, oid).Scan(&n)

	return n, err
}

func (r *Datasets) BytesByOwner(ctx context.Context) (map[domain.UserID]int64, error) {
	rows, err := r.db.conn(ctx).QueryContext(ctx, `
		SELECT d.owner_id, COALESCE(SUM(r.size), 0) FROM datasets d LEFT JOIN dataset_rows r ON r.dataset_id = d.id
		GROUP BY d.owner_id`)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	out := map[domain.UserID]int64{}

	for rows.Next() {
		var (
			owner uuid.UUID
			n     int64
		)

		if err := rows.Scan(&owner, &n); err != nil {
			return nil, err
		}

		out[domain.UserID(owner.String())] = n
	}

	return out, rows.Err()
}

func (r *Datasets) Sweep(ctx context.Context, now, staleStage time.Time) (int64, error) {
	res, err := r.db.conn(ctx).ExecContext(ctx, `
		DELETE FROM dataset_rows r USING datasets d
		WHERE r.dataset_id = d.id AND r.stage_id IS NULL AND d.keep_days > 0
		  AND r.at < $1::timestamptz - make_interval(days => d.keep_days)`, now.UTC())
	if err != nil {
		return 0, err
	}

	dropped, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	_, err = r.db.conn(ctx).ExecContext(ctx, `DELETE FROM dataset_stages WHERE created_at < $1`, staleStage.UTC())

	return dropped, err
}
