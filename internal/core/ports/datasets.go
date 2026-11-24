package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// DatasetStageID is a replace in progress: rows staged, to be swapped in at once.
type DatasetStageID string

// NewRow is a row to store: its key, when the dataset has a key field, and its JSON.
type NewRow struct {
	Key string
	At  time.Time
	Row []byte
}

// DatasetRepository keeps users' datasets and their rows.
type DatasetRepository interface {
	Create(ctx context.Context, d domain.Dataset) error
	// List is the owner's datasets, by name, with their rows and bytes counted.
	List(ctx context.Context, owner domain.UserID) ([]domain.Dataset, error)
	// Get is the owner's dataset by name, counted; domain.ErrNotFound for none.
	Get(ctx context.Context, owner domain.UserID, name string) (domain.Dataset, error)
	SetKeep(ctx context.Context, id domain.DatasetID, days, rows int, at time.Time) error
	Delete(ctx context.Context, id domain.DatasetID) error

	// Rows are a page of rows after seq, ascending; Latest, the row with the key last
	// appended, domain.ErrNotFound for none.
	Rows(ctx context.Context, id domain.DatasetID, after int64, limit int) ([]domain.DatasetRow, error)
	Latest(ctx context.Context, id domain.DatasetID, key string) (domain.DatasetRow, error)

	// Append adds rows, then drops the oldest past keep, when keep is above zero.
	Append(ctx context.Context, id domain.DatasetID, rows []NewRow, keep int, at time.Time) error

	// Stage starts a replace; StageRows adds to it; StageSize counts it; Swap makes its rows
	// the dataset's at once, the old ones gone; Unstage drops it.
	Stage(ctx context.Context, id domain.DatasetID, at time.Time) (DatasetStageID, error)
	StageRows(ctx context.Context, id domain.DatasetID, stage DatasetStageID, rows []NewRow) error
	StageSize(ctx context.Context, id domain.DatasetID, stage DatasetStageID) (rows int, bytes int64, err error)
	Swap(ctx context.Context, id domain.DatasetID, stage DatasetStageID, at time.Time) error
	Unstage(ctx context.Context, id domain.DatasetID, stage DatasetStageID) error

	// Bytes is what the owner's datasets take, stages included.
	Bytes(ctx context.Context, owner domain.UserID) (int64, error)
	// BytesByOwner is what each user's take, for admins.
	BytesByOwner(ctx context.Context) (map[domain.UserID]int64, error)

	// Sweep drops rows older than their dataset keeps and stages started before staleStage,
	// saying how many rows went.
	Sweep(ctx context.Context, now, staleStage time.Time) (int64, error)
}
