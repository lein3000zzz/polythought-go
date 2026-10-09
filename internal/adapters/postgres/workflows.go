package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var (
	_ ports.WorkflowRepository  = (*Workflows)(nil)
	_ ports.ExecutionRepository = (*Executions)(nil)
)

// definitionDoc is how a workflow's graph is stored in the definition column.
type definitionDoc struct {
	Nodes       []nodeDoc       `json:"nodes"`
	Connections []connectionDoc `json:"connections"`
}

type nodeDoc struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Name           string         `json:"name,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	ContinueOnFail bool           `json:"continueOnFail,omitempty"`
	Position       positionDoc    `json:"position,omitzero"`
}

type positionDoc struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type connectionDoc struct {
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
}

func encodeDefinition(w domain.Workflow) (string, error) {
	doc := definitionDoc{
		Nodes:       make([]nodeDoc, len(w.Nodes)),
		Connections: make([]connectionDoc, len(w.Connections)),
	}

	for i, n := range w.Nodes {
		doc.Nodes[i] = nodeDoc{
			ID: string(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
			Position: positionDoc{X: n.Position.X, Y: n.Position.Y},
		}
	}

	for i, c := range w.Connections {
		doc.Connections[i] = connectionDoc{From: string(c.From), FromPort: c.FromPort, To: string(c.To), ToPort: c.ToPort}
	}

	b, err := json.Marshal(doc)

	return string(b), err
}

// decodeDefinition keeps numbers as json.Number, so parameters like 0.02 stay exact.
func decodeDefinition(raw string) ([]domain.NodeSpec, []domain.Connection, error) {
	var doc definitionDoc

	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()

	if err := dec.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("postgres: decode workflow definition: %w", err)
	}

	nodes := make([]domain.NodeSpec, len(doc.Nodes))
	for i, n := range doc.Nodes {
		nodes[i] = domain.NodeSpec{
			ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
			Position: domain.CanvasPosition{X: n.Position.X, Y: n.Position.Y},
		}
	}

	conns := make([]domain.Connection, len(doc.Connections))
	for i, c := range doc.Connections {
		conns[i] = domain.Connection{From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort}
	}

	return nodes, conns, nil
}

type Workflows struct {
	db *DB
}

func NewWorkflows(db *DB) *Workflows {
	return &Workflows{db: db}
}

func (r *Workflows) Create(ctx context.Context, w domain.Workflow) error {
	m, err := toWorkflowModel(w)
	if err != nil {
		return err
	}

	_, err = table.Workflows.INSERT(table.Workflows.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Workflows) Get(ctx context.Context, owner domain.UserID, id domain.WorkflowID) (domain.Workflow, error) {
	where, err := ownedWorkflow(owner, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	var m model.Workflows

	err = jet.SELECT(table.Workflows.AllColumns).FROM(table.Workflows).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.Workflow{}, notFound(err)
	}

	return toWorkflow(m)
}

func (r *Workflows) List(ctx context.Context, owner domain.UserID, f ports.PageFilter) (ports.Page[domain.Workflow], error) {
	uid, err := parseID(owner)
	if err != nil {
		return ports.Page[domain.Workflow]{}, err
	}

	limit := pageLimit(f.Limit)
	where := table.Workflows.OwnerID.EQ(jet.UUID(uid))

	if f.Cursor != "" {
		after, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.Workflow]{}, domain.ErrInvalidInput
		}

		where = where.AND(table.Workflows.ID.GT(jet.UUID(after)))
	}

	var rows []model.Workflows

	err = jet.SELECT(table.Workflows.AllColumns).
		FROM(table.Workflows).
		WHERE(where).
		ORDER_BY(table.Workflows.ID.ASC()).
		LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return ports.Page[domain.Workflow]{}, err
	}

	page := ports.Page[domain.Workflow]{Items: make([]domain.Workflow, len(rows))}

	for i, m := range rows {
		if page.Items[i], err = toWorkflow(m); err != nil {
			return ports.Page[domain.Workflow]{}, err
		}
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

// Update is a compare-and-swap on the version column.
func (r *Workflows) Update(ctx context.Context, w domain.Workflow, expectedVersion int) error {
	where, err := ownedWorkflow(w.OwnerID, w.ID)
	if err != nil {
		return err
	}

	def, err := encodeDefinition(w)
	if err != nil {
		return err
	}

	t := table.Workflows

	res, err := t.UPDATE(t.Name, t.Definition, t.Version, t.UpdatedAt).
		SET(w.Name, def, w.Version, w.UpdatedAt).
		WHERE(where.AND(t.Version.EQ(jet.Int(int64(expectedVersion))))).
		ExecContext(ctx, r.db.conn(ctx))

	if err := affectedOne(res, err); !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	// Nothing matched: either the workflow is gone or someone saved first.
	if _, getErr := r.Get(ctx, w.OwnerID, w.ID); getErr == nil {
		return domain.ErrConflict
	}

	return domain.ErrNotFound
}

func (r *Workflows) SetActive(ctx context.Context, owner domain.UserID, id domain.WorkflowID, active bool, at time.Time) error {
	where, err := ownedWorkflow(owner, id)
	if err != nil {
		return err
	}

	return affectedOne(table.Workflows.
		UPDATE(table.Workflows.Active, table.Workflows.UpdatedAt).
		SET(active, at).
		WHERE(where).
		ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Workflows) Delete(ctx context.Context, owner domain.UserID, id domain.WorkflowID) error {
	where, err := ownedWorkflow(owner, id)
	if err != nil {
		return err
	}

	return affectedOne(table.Workflows.DELETE().WHERE(where).ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Workflows) Count(ctx context.Context, owner domain.UserID) (total, active int, err error) {
	uid, err := parseID(owner)
	if err != nil {
		return 0, 0, err
	}

	var out struct {
		Total  int64
		Active int64
	}

	err = jet.SELECT(
		jet.COUNT(jet.STAR).AS("total"),
		jet.RawInt("COUNT(*) FILTER (WHERE workflows.active)").AS("active"),
	).
		FROM(table.Workflows).
		WHERE(table.Workflows.OwnerID.EQ(jet.UUID(uid))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Total), int(out.Active), err
}

func (r *Workflows) ListActive(ctx context.Context) ([]domain.Workflow, error) {
	var rows []model.Workflows

	err := jet.SELECT(table.Workflows.AllColumns).
		FROM(table.Workflows).
		WHERE(table.Workflows.Active.IS_TRUE()).
		ORDER_BY(table.Workflows.ID.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Workflow, len(rows))

	for i, m := range rows {
		if out[i], err = toWorkflow(m); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func ownedWorkflow(owner domain.UserID, id domain.WorkflowID) (jet.BoolExpression, error) {
	uid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	wid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	return table.Workflows.ID.EQ(jet.UUID(wid)).AND(table.Workflows.OwnerID.EQ(jet.UUID(uid))), nil
}

func toWorkflowModel(w domain.Workflow) (model.Workflows, error) {
	id, err := uuid.Parse(string(w.ID))
	if err != nil {
		return model.Workflows{}, err
	}

	owner, err := uuid.Parse(string(w.OwnerID))
	if err != nil {
		return model.Workflows{}, err
	}

	def, err := encodeDefinition(w)
	if err != nil {
		return model.Workflows{}, err
	}

	return model.Workflows{
		ID:         id,
		OwnerID:    owner,
		Name:       w.Name,
		Active:     w.Active,
		Definition: def,
		Version:    int32(w.Version),
		CreatedAt:  w.CreatedAt,
		UpdatedAt:  w.UpdatedAt,
	}, nil
}

func toWorkflow(m model.Workflows) (domain.Workflow, error) {
	nodes, conns, err := decodeDefinition(m.Definition)
	if err != nil {
		return domain.Workflow{}, err
	}

	return domain.Workflow{
		ID:          domain.WorkflowID(m.ID.String()),
		OwnerID:     domain.UserID(m.OwnerID.String()),
		Name:        m.Name,
		Active:      m.Active,
		Nodes:       nodes,
		Connections: conns,
		Version:     int(m.Version),
		CreatedAt:   m.CreatedAt.UTC(),
		UpdatedAt:   m.UpdatedAt.UTC(),
	}, nil
}

func pageLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}

	return min(limit, maxPageSize)
}

// runDoc is how a node run is stored in the runs column.
type runDoc struct {
	NodeID     string         `json:"nodeId"`
	Status     string         `json:"status"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt time.Time      `json:"finishedAt"`
	Input      map[string]int `json:"input,omitempty"`
	Output     map[string]int `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`

	Samples map[string][]sampleDoc `json:"samples,omitempty"`
}

type sampleDoc struct {
	JSON  json.RawMessage `json:"json,omitempty"`
	Error string          `json:"error,omitempty"`
}

type Executions struct {
	db *DB
}

func NewExecutions(db *DB) *Executions {
	return &Executions{db: db}
}

func (r *Executions) Save(ctx context.Context, e domain.Execution) error {
	id, err := uuid.Parse(string(e.ID))
	if err != nil {
		return err
	}

	wid, err := uuid.Parse(string(e.WorkflowID))
	if err != nil {
		return err
	}

	runs := make([]runDoc, len(e.Runs))
	for i, run := range e.Runs {
		runs[i] = runDoc{
			NodeID: string(run.NodeID), Status: string(run.Status), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
			Input: run.Input, Output: run.Output, Error: run.Error, Samples: toSampleDocs(run.Samples),
		}
	}

	raw, err := json.Marshal(runs)
	if err != nil {
		return err
	}

	_, err = table.Executions.INSERT(table.Executions.AllColumns).MODEL(model.Executions{
		ID:              id,
		WorkflowID:      wid,
		WorkflowVersion: int32(e.WorkflowVersion),
		TriggeredBy:     string(e.TriggeredBy),
		Status:          string(e.Status),
		StartedAt:       e.StartedAt,
		FinishedAt:      timePtr(e.FinishedAt),
		Error:           e.Error,
		Runs:            string(raw),
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

// List returns the newest executions first.
func (r *Executions) List(ctx context.Context, workflowID domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.Execution], error) {
	wid, err := parseID(workflowID)
	if err != nil {
		return ports.Page[domain.Execution]{}, err
	}

	limit := pageLimit(f.Limit)
	where := table.Executions.WorkflowID.EQ(jet.UUID(wid))

	if f.Cursor != "" {
		before, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.Execution]{}, domain.ErrInvalidInput
		}

		where = where.AND(table.Executions.ID.LT(jet.UUID(before)))
	}

	var rows []model.Executions

	err = jet.SELECT(table.Executions.AllColumns).
		FROM(table.Executions).
		WHERE(where).
		ORDER_BY(table.Executions.ID.DESC()).
		LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return ports.Page[domain.Execution]{}, err
	}

	page := ports.Page[domain.Execution]{Items: make([]domain.Execution, len(rows))}

	for i, m := range rows {
		if page.Items[i], err = toExecution(m); err != nil {
			return ports.Page[domain.Execution]{}, err
		}
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

func (r *Executions) Get(ctx context.Context, workflowID domain.WorkflowID, id domain.ExecutionID) (domain.Execution, error) {
	wid, err := parseID(workflowID)
	if err != nil {
		return domain.Execution{}, err
	}

	eid, err := parseID(id)
	if err != nil {
		return domain.Execution{}, err
	}

	var m model.Executions

	err = jet.SELECT(table.Executions.AllColumns).
		FROM(table.Executions).
		WHERE(table.Executions.ID.EQ(jet.UUID(eid)).AND(table.Executions.WorkflowID.EQ(jet.UUID(wid)))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.Execution{}, notFound(err)
	}

	return toExecution(m)
}

func (r *Executions) DeleteFinishedBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := table.Executions.DELETE().
		WHERE(table.Executions.FinishedAt.LT(jet.TimestampzT(before))).
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

func toExecution(m model.Executions) (domain.Execution, error) {
	var runs []runDoc
	if err := json.Unmarshal([]byte(m.Runs), &runs); err != nil {
		return domain.Execution{}, fmt.Errorf("postgres: decode execution runs: %w", err)
	}

	e := domain.Execution{
		ID:              domain.ExecutionID(m.ID.String()),
		WorkflowID:      domain.WorkflowID(m.WorkflowID.String()),
		WorkflowVersion: int(m.WorkflowVersion),
		TriggeredBy:     domain.NodeID(m.TriggeredBy),
		Status:          domain.ExecutionStatus(m.Status),
		StartedAt:       m.StartedAt.UTC(),
		FinishedAt:      timeOf(m.FinishedAt),
		Error:           m.Error,
		Runs:            make([]domain.NodeRun, len(runs)),
	}

	for i, r := range runs {
		e.Runs[i] = domain.NodeRun{
			NodeID: domain.NodeID(r.NodeID), Status: domain.NodeRunStatus(r.Status),
			StartedAt: r.StartedAt.UTC(), FinishedAt: r.FinishedAt.UTC(),
			Input: r.Input, Output: r.Output, Error: r.Error, Samples: fromSampleDocs(r.Samples),
		}
	}

	return e, nil
}

func toSampleDocs(samples map[string][]domain.ItemSample) map[string][]sampleDoc {
	if samples == nil {
		return nil
	}

	out := make(map[string][]sampleDoc, len(samples))
	for port, list := range samples {
		for _, s := range list {
			out[port] = append(out[port], sampleDoc{JSON: s.JSON, Error: s.Error})
		}
	}

	return out
}

// fromSampleDocs compacts the items again: jsonb hands JSON back with spaces added.
func fromSampleDocs(docs map[string][]sampleDoc) map[string][]domain.ItemSample {
	if docs == nil {
		return nil
	}

	out := make(map[string][]domain.ItemSample, len(docs))
	for port, list := range docs {
		for _, d := range list {
			s := domain.ItemSample{Error: d.Error}

			if len(d.JSON) > 0 {
				var buf bytes.Buffer
				if err := json.Compact(&buf, d.JSON); err == nil {
					s.JSON = buf.Bytes()
				} else {
					s.JSON = d.JSON
				}
			}

			out[port] = append(out[port], s)
		}
	}

	return out
}
