package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/datasets"
)

// DatasetService is users' datasets.
type DatasetService interface {
	List(ctx context.Context, owner domain.UserID) ([]domain.Dataset, error)
	Get(ctx context.Context, owner domain.UserID, name string) (domain.Dataset, error)
	Create(ctx context.Context, owner domain.UserID, spec datasets.Spec) (domain.Dataset, error)
	SetKeep(ctx context.Context, owner domain.UserID, name string, days, rows int) (domain.Dataset, error)
	Delete(ctx context.Context, owner domain.UserID, name string) error
	Rows(ctx context.Context, owner domain.UserID, name string, after int64, limit int) ([]domain.DatasetRow, error)
	Lookup(ctx context.Context, owner domain.UserID, name, key string) (domain.DatasetRow, error)
	Append(ctx context.Context, owner domain.UserID, name string, rows []json.RawMessage) (domain.Dataset, error)
	StartReplace(ctx context.Context, owner domain.UserID, name string) (ports.DatasetStageID, error)
	StageRows(ctx context.Context, owner domain.UserID, name string, stage ports.DatasetStageID, rows []json.RawMessage) (int, error)
	FinishReplace(ctx context.Context, owner domain.UserID, name string, stage ports.DatasetStageID) (domain.Dataset, error)
	DropReplace(ctx context.Context, owner domain.UserID, name string, stage ports.DatasetStageID) error
	// UsedBy are the owner's workflows naming each of their datasets, by its name.
	UsedBy(ctx context.Context, owner domain.UserID) (map[string][]domain.Workflow, error)
	// Usage is what each user's datasets take, for admins; Limits, the instance's.
	Usage(ctx context.Context) (map[domain.UserID]int64, error)
	Limits() datasets.Limits
}

type datasetDTO struct {
	Name string `json:"name"`
	// KeyField is the field rows are looked up by; absent for none.
	KeyField string `json:"keyField,omitempty" validate:"optional"`
	// KeepDays drops rows older than so many days, 0 for none; KeepRows keeps no more than
	// so many, the oldest dropped first, 0 for the instance's most.
	KeepDays  int       `json:"keepDays"`
	KeepRows  int       `json:"keepRows"`
	Rows      int       `json:"rows"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"createdAt" format:"date-time"`
	UpdatedAt time.Time `json:"updatedAt" format:"date-time"`
	// Workflows are yours whose nodes name it: deleting it switches those on off. Listing
	// datasets, or getting one, says; other answers leave it out.
	Workflows []usingWorkflowDTO `json:"workflows,omitempty" validate:"optional"`
} // @name Dataset

// usingWorkflowDTO is one of your workflows using a dataset or a model.
type usingWorkflowDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
} // @name UsingWorkflow

type datasetListDTO struct {
	Datasets []datasetDTO     `json:"datasets"`
	Limits   datasetLimitsDTO `json:"limits"`
} // @name DatasetList

// datasetLimitsDTO are the instance's limits, for a form to offer.
type datasetLimitsDTO struct {
	Datasets int   `json:"datasets"` // a user's
	Rows     int   `json:"rows"`     // a dataset's
	RowBytes int   `json:"rowBytes"` // a row's JSON
	Bytes    int64 `json:"bytes"`    // a user's storage
	// KeepDays is what a dataset made for recording keeps, in days, to begin with.
	KeepDays int `json:"keepDays"`
} // @name DatasetLimits

type createDatasetRequest struct {
	// Name is lower-case letters, digits, - and _, up to 64; unique among yours, and fixed.
	Name     string `json:"name"`
	KeyField string `json:"keyField" validate:"optional"`
	KeepDays int    `json:"keepDays" validate:"optional" minimum:"0"`
	KeepRows int    `json:"keepRows" validate:"optional" minimum:"0"`
} // @name CreateDatasetRequest

type datasetKeepRequest struct {
	KeepDays int `json:"keepDays" minimum:"0"`
	KeepRows int `json:"keepRows" minimum:"0"`
} // @name DatasetKeepRequest

type datasetRowDTO struct {
	// Seq is its place, ascending as rows were added: the cursor of the next page.
	Seq int64     `json:"seq"`
	Key string    `json:"key,omitempty" validate:"optional"`
	At  time.Time `json:"at" format:"date-time"`
	// Row is the row itself, a JSON object.
	Row map[string]any `json:"row"`
} // @name DatasetRow

type datasetRowsDTO struct {
	Rows []datasetRowDTO `json:"rows"`
	// Next is the after of the next page; absent on the last.
	Next *int64 `json:"next,omitempty" validate:"optional"`
} // @name DatasetRows

//nolint:unused // the body as documented; rowsBody reads it
type datasetRowsRequest struct {
	// Rows are JSON objects, each at most the size a row may be.
	Rows []map[string]any `json:"rows"`
} // @name DatasetRowsRequest

// rowsBody is datasetRowsRequest as read: each row's JSON as sent, its numbers exact.
type rowsBody struct {
	Rows []json.RawMessage `json:"rows"`
}

type stageDTO struct {
	// Stage is the replace in progress: send rows to it, then finish it.
	Stage string `json:"stage"`
	// Staged is how many rows it holds so far.
	Staged int `json:"staged"`
} // @name DatasetStage

func toDatasetDTO(d domain.Dataset) datasetDTO {
	return datasetDTO{
		Name: d.Name, KeyField: d.KeyField, KeepDays: d.KeepDays, KeepRows: d.KeepRows, Rows: d.Rows, Bytes: d.Bytes,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// usingWorkflows are the workflows naming a dataset or a model.
func usingWorkflows(used []domain.Workflow) []usingWorkflowDTO {
	out := make([]usingWorkflowDTO, len(used))
	for i, w := range used {
		out[i] = usingWorkflowDTO{ID: string(w.ID), Name: w.Name, Active: w.Active}
	}

	return out
}

// withWorkflows is d with the workflows naming it.
func withWorkflows(d datasetDTO, used []domain.Workflow) datasetDTO {
	d.Workflows = usingWorkflows(used)
	return d
}

func toDatasetRowDTO(r domain.DatasetRow) datasetRowDTO {
	var row map[string]any
	_ = json.Unmarshal(r.Row, &row)

	return datasetRowDTO{Seq: r.Seq, Key: r.Key, At: r.At, Row: row}
}

// listDatasets lists your datasets, by name.
//
//	@Summary	List datasets
//	@ID			listDatasets
//	@Tags		datasets
//	@Success	200	{object}	datasetListDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"data_off: an admin keeps datasets from you"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets [get]
func (s *Server) listDatasets(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.datasets.List(ctx, p.User.ID)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	used, err := s.datasets.UsedBy(ctx, p.User.ID)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	l := s.datasets.Limits()
	out := datasetListDTO{
		Datasets: make([]datasetDTO, len(list)),
		Limits:   datasetLimitsDTO{Datasets: l.Datasets, Rows: l.Rows, RowBytes: l.RowBytes, Bytes: l.Bytes, KeepDays: l.KeepDays},
	}

	for i, d := range list {
		out.Datasets[i] = withWorkflows(toDatasetDTO(d), used[d.Name])
	}

	c.JSON(http.StatusOK, out)
}

// createDataset makes a dataset.
//
//	@Summary	Make a dataset
//	@ID			createDataset
//	@Tags		datasets
//	@Param		body	body		createDatasetRequest	true	"The dataset"
//	@Success	201		{object}	datasetDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	409		{object}	errorBody	"dataset_exists; dataset_full: as many as you may have"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets [post]
func (s *Server) createDataset(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req createDatasetRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	d, err := s.datasets.Create(ctx, p.User.ID, datasets.Spec{Name: req.Name, KeyField: req.KeyField, KeepDays: req.KeepDays, KeepRows: req.KeepRows})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toDatasetDTO(d))
}

// getDataset is one of your datasets.
//
//	@Summary	Get a dataset
//	@ID			getDataset
//	@Tags		datasets
//	@Param		name	path		string	true	"Its name"
//	@Success	200		{object}	datasetDTO
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name} [get]
func (s *Server) getDataset(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	d, err := s.datasets.Get(ctx, p.User.ID, c.Param("name"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	used, err := s.datasets.UsedBy(ctx, p.User.ID)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, withWorkflows(toDatasetDTO(d), used[d.Name]))
}

// setDatasetKeep changes how long and how many rows a dataset keeps.
//
//	@Summary	Set what a dataset keeps
//	@ID			setDatasetKeep
//	@Tags		datasets
//	@Param		name	path		string				true	"Its name"
//	@Param		body	body		datasetKeepRequest	true	"Days and rows: 0 for no limit"
//	@Success	200		{object}	datasetDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/keep [put]
func (s *Server) setDatasetKeep(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req datasetKeepRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	d, err := s.datasets.SetKeep(ctx, p.User.ID, c.Param("name"), req.KeepDays, req.KeepRows)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toDatasetDTO(d))
}

// deleteDataset deletes a dataset and its rows, and switches off your workflows naming it.
//
//	@Summary	Delete a dataset
//	@ID			deleteDataset
//	@Tags		datasets
//	@Param		name	path	string	true	"Its name"
//	@Success	204
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name} [delete]
func (s *Server) deleteDataset(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.datasets.Delete(ctx, p.User.ID, c.Param("name")); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// listDatasetRows reads a dataset's rows a page at a time, oldest first.
//
//	@Summary	A dataset's rows
//	@ID			listDatasetRows
//	@Tags		datasets
//	@Param		name	path		string	true	"Its name"
//	@Param		after	query		int		false	"The seq to start after: the page before's next"
//	@Param		limit	query		int		false	"At most this many: 1,000 unless said, and at most"
//	@Success	200		{object}	datasetRowsDTO
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/rows [get]
func (s *Server) listDatasetRows(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	after, err := intArg(c, "after")
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	limit, err := intArg(c, "limit")
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	if limit <= 0 || limit > datasets.MaxPage {
		limit = datasets.MaxPage
	}

	rows, err := s.datasets.Rows(ctx, p.User.ID, c.Param("name"), after, int(limit))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := datasetRowsDTO{Rows: make([]datasetRowDTO, len(rows))}
	for i, r := range rows {
		out.Rows[i] = toDatasetRowDTO(r)
	}

	if len(rows) == int(limit) {
		next := rows[len(rows)-1].Seq
		out.Next = &next
	}

	c.JSON(http.StatusOK, out)
}

func intArg(c *app.RequestContext, name string) (int64, error) {
	raw := string(c.Query(name))
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, invalidInput(name + ": a whole number, 0 or more")
	}

	return n, nil
}

// getDatasetRow is the row last added with a key, in a dataset with a key field.
//
//	@Summary	A dataset's row by its key
//	@ID			getDatasetRow
//	@Tags		datasets
//	@Param		name	path		string	true	"Its name"
//	@Param		key		path		string	true	"The key field's value"
//	@Success	200		{object}	datasetRowDTO
//	@Failure	400		{object}	errorBody	"invalid_input: it has no key field"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/rows/{key} [get]
func (s *Server) getDatasetRow(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	row, err := s.datasets.Lookup(ctx, p.User.ID, c.Param("name"), c.Param("key"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toDatasetRowDTO(row))
}

// appendDatasetRows adds rows to a dataset: the oldest go past what it keeps.
//
//	@Summary	Add rows to a dataset
//	@ID			appendDatasetRows
//	@Tags		datasets
//	@Param		name	path		string				true	"Its name"
//	@Param		body	body		datasetRowsRequest	true	"The rows: within the request's 1 MiB"
//	@Success	200		{object}	datasetDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"dataset_full"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/rows [post]
func (s *Server) appendDatasetRows(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req rowsBody
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	d, err := s.datasets.Append(ctx, p.User.ID, c.Param("name"), req.Rows)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toDatasetDTO(d))
}

// startDatasetReplace starts replacing a dataset's rows: rows sent to the stage go in, at
// once, when it is finished; until then the dataset is as it was.
//
//	@Summary	Start replacing a dataset's rows
//	@ID			startDatasetReplace
//	@Tags		datasets
//	@Param		name	path		string	true	"Its name"
//	@Success	201		{object}	stageDTO
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/replace [post]
func (s *Server) startDatasetReplace(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	stage, err := s.datasets.StartReplace(ctx, p.User.ID, c.Param("name"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, stageDTO{Stage: string(stage)})
}

// stageDatasetRows sends rows to a replace in progress.
//
//	@Summary	Send rows to a replace
//	@ID			stageDatasetRows
//	@Tags		datasets
//	@Param		name	path		string				true	"Its name"
//	@Param		stage	path		string				true	"The replace"
//	@Param		body	body		datasetRowsRequest	true	"The rows: within the request's 1 MiB"
//	@Success	200		{object}	stageDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"dataset_full"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/replace/{stage}/rows [post]
func (s *Server) stageDatasetRows(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req rowsBody
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	stage := ports.DatasetStageID(c.Param("stage"))

	n, err := s.datasets.StageRows(ctx, p.User.ID, c.Param("name"), stage, req.Rows)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, stageDTO{Stage: string(stage), Staged: n})
}

// finishDatasetReplace swaps the staged rows in, at once, the old ones gone.
//
//	@Summary	Finish a replace
//	@ID			finishDatasetReplace
//	@Tags		datasets
//	@Param		name	path		string	true	"Its name"
//	@Param		stage	path		string	true	"The replace"
//	@Success	200		{object}	datasetDTO
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/replace/{stage}/finish [post]
func (s *Server) finishDatasetReplace(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	d, err := s.datasets.FinishReplace(ctx, p.User.ID, c.Param("name"), ports.DatasetStageID(c.Param("stage")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toDatasetDTO(d))
}

// dropDatasetReplace drops a replace in progress and its rows; the dataset stays as it was.
//
//	@Summary	Drop a replace
//	@ID			dropDatasetReplace
//	@Tags		datasets
//	@Param		name	path	string	true	"Its name"
//	@Param		stage	path	string	true	"The replace"
//	@Success	204
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/replace/{stage} [delete]
func (s *Server) dropDatasetReplace(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.datasets.DropReplace(ctx, p.User.ID, c.Param("name"), ports.DatasetStageID(c.Param("stage"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// exportDataset downloads a dataset as CSV: a column a field, in the order they first
// appear; a value that is an object or list, its JSON.
//
//	@Summary	A dataset as CSV
//	@ID			exportDataset
//	@Tags		datasets
//	@Produce	text/csv
//	@Param		name	path	string	true	"Its name"
//	@Success	200		{string}	string	"CSV"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/datasets/{name}/csv [get]
func (s *Server) exportDataset(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	name := c.Param("name")

	var (
		all   []map[string]any
		after int64
	)

	for {
		rows, err := s.datasets.Rows(ctx, p.User.ID, name, after, datasets.MaxPage)
		if err != nil {
			s.fail(ctx, c, err)
			return
		}

		for _, r := range rows {
			var row map[string]any
			_ = json.Unmarshal(r.Row, &row)
			all = append(all, row)
		}

		if len(rows) < datasets.MaxPage {
			break
		}

		after = rows[len(rows)-1].Seq
	}

	var columns []string

	for _, row := range all {
		var fresh []string

		for k := range row {
			if !slices.Contains(columns, k) {
				fresh = append(fresh, k)
			}
		}

		slices.Sort(fresh) // a row's own fields in a fixed order
		columns = append(columns, fresh...)
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name+".csv"))
	c.Status(http.StatusOK)

	w := csv.NewWriter(c)
	_ = w.Write(columns)

	for _, row := range all {
		line := make([]string, len(columns))
		for i, col := range columns {
			line[i] = cellOf(row[col])
		}

		_ = w.Write(line)
	}

	w.Flush()
}

// cellOf is a value as a CSV cell: text as it is, anything else its JSON.
func cellOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// hasDatasets answers 404 where the server keeps no datasets.
func (s *Server) hasDatasets(ctx context.Context, c *app.RequestContext) {
	if s.datasets == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}

// dataAccessNow is who may use datasets and models now: everyone where the server says
// nothing.
func (s *Server) dataAccessNow() domain.DataAccess {
	if s.dataAccess == nil {
		return domain.DataEveryone
	}

	return s.dataAccess()
}

type datasetUsageDTO struct {
	// Bytes is what each user's datasets take, by user ID; a user with none is absent.
	Bytes map[string]int64 `json:"bytes"`
	// Limit is what each may take.
	Limit int64 `json:"limit"`
} // @name DatasetUsage

// datasetUsage is what each user's datasets take, against what each may: an admin's.
//
//	@Summary	Datasets' storage, by user
//	@ID			datasetUsage
//	@Tags		admin
//	@Success	200	{object}	datasetUsageDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/datasets/usage [get]
func (s *Server) datasetUsage(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	if !p.User.IsAdmin() {
		s.fail(ctx, c, domain.ErrForbidden)
		return
	}

	usage, err := s.datasets.Usage(ctx)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := datasetUsageDTO{Bytes: make(map[string]int64, len(usage)), Limit: s.datasets.Limits().Bytes}
	for id, n := range usage {
		out.Bytes[string(id)] = n
	}

	c.JSON(http.StatusOK, out)
}
