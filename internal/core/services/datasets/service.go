// Package datasets is users' datasets: named tables of JSON rows a notebook publishes,
// replacing them at once or appending, and workflows read or record into; each row at
// most so big, each dataset so many rows, each user so much storage, as an admin sets.
package datasets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	// MaxPage is the most rows one read gives.
	MaxPage = 1000

	// staleStage is how long an unfinished replace is kept.
	staleStage = 24 * time.Hour
)

// Limits are an admin's, read on every use.
type Limits struct {
	Datasets int   // a user's datasets
	Rows     int   // a dataset's rows
	RowBytes int   // a row's JSON
	Bytes    int64 // a user's storage, stages counted
}

// Gate says whether a user may use datasets now.
type Gate interface {
	DataAllowed(ctx context.Context, owner domain.UserID) (bool, error)
}

type Deps struct {
	Repo   ports.DatasetRepository
	Gate   Gate
	Limits func() Limits
	Logger logging.Logger
	Now    func() time.Time
}

type Service struct {
	deps Deps
	log  logging.Logger
}

func New(deps Deps) *Service {
	if deps.Logger == nil {
		deps.Logger = logging.Noop()
	}

	if deps.Now == nil {
		deps.Now = time.Now
	}

	return &Service{deps: deps, log: deps.Logger.With("component", "datasets")}
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalidInput, fmt.Sprintf(format, args...))
}

// allowed refuses a user an admin keeps from datasets.
func (s *Service) allowed(ctx context.Context, owner domain.UserID) error {
	if s.deps.Gate == nil {
		return nil
	}

	ok, err := s.deps.Gate.DataAllowed(ctx, owner)
	if err != nil {
		return err
	}

	if !ok {
		return domain.ErrDataOff
	}

	return nil
}

// Spec is a new dataset.
type Spec struct {
	Name               string
	KeyField           string
	KeepDays, KeepRows int
}

func (s *Service) Create(ctx context.Context, owner domain.UserID, spec Spec) (domain.Dataset, error) {
	if err := s.allowed(ctx, owner); err != nil {
		return domain.Dataset{}, err
	}

	spec.Name, spec.KeyField = strings.TrimSpace(spec.Name), strings.TrimSpace(spec.KeyField)

	if !nameRe.MatchString(spec.Name) {
		return domain.Dataset{}, invalid("name %q: lower-case letters, digits, - and _, up to 64, like temperature-forecasts", spec.Name)
	}

	if err := s.checkKeep(spec.KeepDays, spec.KeepRows); err != nil {
		return domain.Dataset{}, err
	}

	have, err := s.deps.Repo.List(ctx, owner)
	if err != nil {
		return domain.Dataset{}, err
	}

	if limit := s.deps.Limits().Datasets; len(have) >= limit {
		return domain.Dataset{}, fmt.Errorf("%w: you have %d datasets, the most you may", domain.ErrDatasetFull, limit)
	}

	now := s.deps.Now().UTC()
	d := domain.Dataset{
		ID: domain.DatasetID(uuid.Must(uuid.NewV7()).String()), OwnerID: owner, Name: spec.Name, KeyField: spec.KeyField,
		KeepDays: spec.KeepDays, KeepRows: spec.KeepRows, CreatedAt: now, UpdatedAt: now,
	}

	if err := s.deps.Repo.Create(ctx, d); err != nil {
		return domain.Dataset{}, err
	}

	s.log.Info(ctx, "Dataset made", "owner_id", owner, "dataset", d.Name)

	return d, nil
}

func (s *Service) checkKeep(days, rows int) error {
	switch {
	case days < 0:
		return invalid("keepDays: 0 for every day, or more")
	case rows < 0 || rows > s.deps.Limits().Rows:
		return invalid("keepRows: 0 for as many as may be, up to %d", s.deps.Limits().Rows)
	}

	return nil
}

func (s *Service) List(ctx context.Context, owner domain.UserID) ([]domain.Dataset, error) {
	if err := s.allowed(ctx, owner); err != nil {
		return nil, err
	}

	return s.deps.Repo.List(ctx, owner)
}

func (s *Service) Get(ctx context.Context, owner domain.UserID, name string) (domain.Dataset, error) {
	if err := s.allowed(ctx, owner); err != nil {
		return domain.Dataset{}, err
	}

	d, err := s.deps.Repo.Get(ctx, owner, strings.TrimSpace(name))
	if err != nil {
		return domain.Dataset{}, named(err, name)
	}

	return d, nil
}

func named(err error, name string) error {
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: you have no dataset %q", domain.ErrNotFound, name)
	}

	return err
}

// SetKeep changes how long and how many rows a dataset keeps.
func (s *Service) SetKeep(ctx context.Context, owner domain.UserID, name string, days, rows int) (domain.Dataset, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return domain.Dataset{}, err
	}

	if err := s.checkKeep(days, rows); err != nil {
		return domain.Dataset{}, err
	}

	if err := s.deps.Repo.SetKeep(ctx, d.ID, days, rows, s.deps.Now()); err != nil {
		return domain.Dataset{}, err
	}

	return s.Get(ctx, owner, name)
}

func (s *Service) Delete(ctx context.Context, owner domain.UserID, name string) error {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return err
	}

	if err := s.deps.Repo.Delete(ctx, d.ID); err != nil {
		return err
	}

	s.log.Info(ctx, "Dataset deleted", "owner_id", owner, "dataset", d.Name, "rows", d.Rows)

	return nil
}

// Rows are a page of a dataset's rows after seq, oldest first.
func (s *Service) Rows(ctx context.Context, owner domain.UserID, name string, after int64, limit int) ([]domain.DatasetRow, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return nil, err
	}

	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}

	return s.deps.Repo.Rows(ctx, d.ID, after, limit)
}

// Lookup is the row last appended with the key.
func (s *Service) Lookup(ctx context.Context, owner domain.UserID, name, key string) (domain.DatasetRow, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return domain.DatasetRow{}, err
	}

	if d.KeyField == "" {
		return domain.DatasetRow{}, invalid("dataset %q has no key field to look rows up by", d.Name)
	}

	row, err := s.deps.Repo.Latest(ctx, d.ID, key)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DatasetRow{}, fmt.Errorf("%w: no row with %s %q", domain.ErrNotFound, d.KeyField, key)
	}

	return row, err
}

// rowsOf checks rows as d takes them: JSON objects, each at most the size a row may be,
// with the key field where it has one.
func (s *Service) rowsOf(d domain.Dataset, raw []json.RawMessage) ([]ports.NewRow, int64, error) {
	if len(raw) == 0 {
		return nil, 0, invalid("no rows")
	}

	limit := s.deps.Limits().RowBytes
	now := s.deps.Now().UTC()
	out := make([]ports.NewRow, len(raw))

	var total int64

	for i, r := range raw {
		var buf bytes.Buffer
		if err := json.Compact(&buf, r); err != nil {
			return nil, 0, invalid("row %d is not JSON", i)
		}

		var obj map[string]any
		if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
			return nil, 0, invalid("row %d is not an object: a row is {\"field\": value, …}", i)
		}

		if buf.Len() > limit {
			return nil, 0, invalid("row %d is %d bytes, more than the %d a row may be", i, buf.Len(), limit)
		}

		row := ports.NewRow{At: now, Row: buf.Bytes()}

		if d.KeyField != "" {
			v, ok := obj[d.KeyField]
			if !ok || v == nil {
				return nil, 0, invalid("row %d has no %s, the dataset's key field", i, d.KeyField)
			}

			row.Key = engine.Format(v)
		}

		out[i] = row
		total += int64(buf.Len())
	}

	return out, total, nil
}

// room refuses rows that would take the owner past their storage.
func (s *Service) room(ctx context.Context, owner domain.UserID, more int64) error {
	used, err := s.deps.Repo.Bytes(ctx, owner)
	if err != nil {
		return err
	}

	if limit := s.deps.Limits().Bytes; used+more > limit {
		return fmt.Errorf("%w: your datasets take %s of the %s you may have", domain.ErrDatasetFull, size(used), size(limit))
	}

	return nil
}

// Append adds rows: the oldest go past the dataset's Keep rows; without it, rows past the
// instance's most are refused.
func (s *Service) Append(ctx context.Context, owner domain.UserID, name string, raw []json.RawMessage) (domain.Dataset, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return domain.Dataset{}, err
	}

	return s.AppendTo(ctx, d, raw)
}

// AppendTo adds rows to a dataset read already, for a node that has it.
func (s *Service) AppendTo(ctx context.Context, d domain.Dataset, raw []json.RawMessage) (domain.Dataset, error) {
	rows, total, err := s.rowsOf(d, raw)
	if err != nil {
		return domain.Dataset{}, err
	}

	maxRows := s.deps.Limits().Rows

	keep := d.KeepRows
	if keep == 0 && d.Rows+len(rows) > maxRows {
		return domain.Dataset{}, fmt.Errorf("%w: %q would have %d rows, more than the %d a dataset may: set Keep rows to drop the oldest",
			domain.ErrDatasetFull, d.Name, d.Rows+len(rows), maxRows)
	}

	if err := s.room(ctx, d.OwnerID, total); err != nil {
		return domain.Dataset{}, err
	}

	if err := s.deps.Repo.Append(ctx, d.ID, rows, keep, s.deps.Now()); err != nil {
		return domain.Dataset{}, err
	}

	return s.deps.Repo.Get(ctx, d.OwnerID, d.Name)
}

// StartReplace stages a replace: rows staged go in at once when it is finished.
func (s *Service) StartReplace(ctx context.Context, owner domain.UserID, name string) (ports.DatasetStageID, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return "", err
	}

	return s.deps.Repo.Stage(ctx, d.ID, s.deps.Now())
}

// StageRows adds rows to a replace.
func (s *Service) StageRows(ctx context.Context, owner domain.UserID, name string, stage ports.DatasetStageID, raw []json.RawMessage) (int, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return 0, err
	}

	rows, total, err := s.rowsOf(d, raw)
	if err != nil {
		return 0, err
	}

	staged, _, err := s.deps.Repo.StageSize(ctx, d.ID, stage)
	if err != nil {
		return 0, stageErr(err)
	}

	if limit := s.deps.Limits().Rows; staged+len(rows) > limit {
		return 0, fmt.Errorf("%w: the replace would have %d rows, more than the %d a dataset may", domain.ErrDatasetFull, staged+len(rows), limit)
	}

	if err := s.room(ctx, owner, total); err != nil {
		return 0, err
	}

	if err := s.deps.Repo.StageRows(ctx, d.ID, stage, rows); err != nil {
		return 0, stageErr(err)
	}

	return staged + len(rows), nil
}

func stageErr(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: no such replace in progress: it was finished, dropped, or is older than a day", domain.ErrNotFound)
	}

	return err
}

// FinishReplace swaps the staged rows in, at once, the old ones gone.
func (s *Service) FinishReplace(ctx context.Context, owner domain.UserID, name string, stage ports.DatasetStageID) (domain.Dataset, error) {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return domain.Dataset{}, err
	}

	if err := s.deps.Repo.Swap(ctx, d.ID, stage, s.deps.Now()); err != nil {
		return domain.Dataset{}, stageErr(err)
	}

	s.log.Info(ctx, "Dataset replaced", "owner_id", owner, "dataset", d.Name)

	return s.deps.Repo.Get(ctx, owner, d.Name)
}

// DropReplace drops a replace and its rows.
func (s *Service) DropReplace(ctx context.Context, owner domain.UserID, name string, stage ports.DatasetStageID) error {
	d, err := s.Get(ctx, owner, name)
	if err != nil {
		return err
	}

	return stageErr(s.deps.Repo.Unstage(ctx, d.ID, stage))
}

// Limit is what each user's datasets may take.
func (s *Service) Limit() int64 { return s.deps.Limits().Bytes }

// Usage is what each user's datasets take, for admins.
func (s *Service) Usage(ctx context.Context) (map[domain.UserID]int64, error) {
	return s.deps.Repo.BytesByOwner(ctx)
}

// Sweep drops rows past their dataset's days, and replaces left unfinished a day.
func (s *Service) Sweep(ctx context.Context) {
	now := s.deps.Now()

	dropped, err := s.deps.Repo.Sweep(ctx, now, now.Add(-staleStage))
	if err != nil {
		s.log.Warn(ctx, "Datasets not swept", logging.Err(err))
		return
	}

	if dropped > 0 {
		s.log.Info(ctx, "Datasets swept", "rows", dropped)
	}
}

// RunSweeper sweeps every interval until ctx is done.
func (s *Service) RunSweeper(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		s.Sweep(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// size is bytes as people read them.
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
