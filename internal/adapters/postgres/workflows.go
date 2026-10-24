package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	Notes          string         `json:"notes,omitempty"`
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

// encodeDefinition stores a graph: a workflow's, or a shared template's.
func encodeDefinition(nodes []domain.NodeSpec, connections []domain.Connection) (string, error) {
	doc := definitionDoc{
		Nodes:       make([]nodeDoc, len(nodes)),
		Connections: make([]connectionDoc, len(connections)),
	}

	for i, n := range nodes {
		doc.Nodes[i] = nodeDoc{
			ID: string(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
			Position: positionDoc{X: n.Position.X, Y: n.Position.Y}, Notes: n.Notes,
		}
	}

	for i, c := range connections {
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
			Position: domain.CanvasPosition{X: n.Position.X, Y: n.Position.Y}, Notes: n.Notes,
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

func (r *Workflows) List(ctx context.Context, owner domain.UserID, f ports.WorkflowFilter) (ports.Page[domain.Workflow], error) {
	uid, err := parseID(owner)
	if err != nil {
		return ports.Page[domain.Workflow]{}, err
	}

	t := table.Workflows
	limit := pageLimit(f.Limit)
	where := t.OwnerID.EQ(jet.UUID(uid))

	if q := strings.TrimSpace(f.Query); q != "" {
		like := jet.String("%" + likeEscaper.Replace(strings.ToLower(q)) + "%")
		where = where.AND(jet.LOWER(t.Name).LIKE(like).OR(jet.LOWER(t.Description).LIKE(like)))
	}

	if f.Active != nil {
		where = where.AND(t.Active.EQ(jet.Bool(*f.Active)))
	}

	// The pinned in their order, all of them; the rest the newest first, a page at a time.
	order := t.ID.DESC()

	switch {
	case f.Pinned == nil:
	case *f.Pinned:
		where, order, limit = where.AND(t.Pin.IS_NOT_NULL()), t.Pin.ASC(), maxPageSize
	default:
		where = where.AND(t.Pin.IS_NULL())
	}

	if f.Cursor != "" && (f.Pinned == nil || !*f.Pinned) {
		before, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.Workflow]{}, domain.ErrInvalidInput
		}

		where = where.AND(t.ID.LT(jet.UUID(before)))
	}

	var rows []model.Workflows

	err = jet.SELECT(t.AllColumns).
		FROM(t).
		WHERE(where).
		ORDER_BY(order).
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

	if len(rows) == limit && (f.Pinned == nil || !*f.Pinned) {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

// likeEscaper escapes what LIKE reads as a pattern, so a search finds it as typed.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SetPin pins a workflow after the owner's others, or unpins it, closing the gap it
// leaves. Neither changes its version.
func (r *Workflows) SetPin(ctx context.Context, owner domain.UserID, id domain.WorkflowID, pinned bool) error {
	where, err := ownedWorkflow(owner, id)
	if err != nil {
		return err
	}

	uid, _ := parseID(owner)

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		t := table.Workflows
		owned := t.OwnerID.EQ(jet.UUID(uid))

		// The owner's workflows held while the pins move, so two pins never take one place.
		var all []model.Workflows

		err := jet.SELECT(t.ID, t.Pin).FROM(t).WHERE(owned).FOR(jet.UPDATE()).QueryContext(ctx, r.db.conn(ctx), &all)
		if err != nil {
			return err
		}

		var (
			this  *model.Workflows
			after int32
		)

		for i, m := range all {
			if m.ID.String() == string(id) {
				this = &all[i]
			}

			if m.Pin != nil && *m.Pin > after {
				after = *m.Pin
			}
		}

		if this == nil {
			return domain.ErrNotFound
		}

		switch {
		case pinned && this.Pin == nil:
			_, err = t.UPDATE(t.Pin).SET(jet.Int32(after+1)).WHERE(where).ExecContext(ctx, r.db.conn(ctx))
		case !pinned && this.Pin != nil:
			m := this
			if _, err = t.UPDATE(t.Pin).SET(jet.NULL).WHERE(where).ExecContext(ctx, r.db.conn(ctx)); err == nil {
				_, err = t.UPDATE(t.Pin).SET(t.Pin.SUB(jet.Int(1))).
					WHERE(owned.AND(t.Pin.GT(jet.Int32(*m.Pin)))).ExecContext(ctx, r.db.conn(ctx))
			}
		}

		return err
	})
}

// SetPinOrder puts the owner's pinned workflows in the order of ids, which must be every
// one of them, else domain.ErrConflict: they changed meanwhile.
func (r *Workflows) SetPinOrder(ctx context.Context, owner domain.UserID, ids []domain.WorkflowID) error {
	uid, err := parseID(owner)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		t := table.Workflows

		var rows []model.Workflows

		err := jet.SELECT(t.ID).FROM(t).WHERE(t.OwnerID.EQ(jet.UUID(uid)).AND(t.Pin.IS_NOT_NULL())).
			FOR(jet.UPDATE()).QueryContext(ctx, r.db.conn(ctx), &rows)
		if err != nil {
			return err
		}

		pinned := map[string]bool{}
		for _, m := range rows {
			pinned[m.ID.String()] = true
		}

		if len(ids) != len(pinned) {
			return domain.ErrConflict
		}

		for i, id := range ids {
			if !pinned[string(id)] {
				return domain.ErrConflict
			}

			delete(pinned, string(id)) // each once

			wid, err := uuid.Parse(string(id))
			if err != nil {
				return domain.ErrInvalidInput
			}

			_, err = t.UPDATE(t.Pin).SET(jet.Int32(int32(i+1))).
				WHERE(t.OwnerID.EQ(jet.UUID(uid)).AND(t.ID.EQ(jet.UUID(wid)))).ExecContext(ctx, r.db.conn(ctx))
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// Update is a compare-and-swap on the version column.
func (r *Workflows) Update(ctx context.Context, w domain.Workflow, expectedVersion int) error {
	where, err := ownedWorkflow(w.OwnerID, w.ID)
	if err != nil {
		return err
	}

	def, err := encodeDefinition(w.Nodes, w.Connections)
	if err != nil {
		return err
	}

	t := table.Workflows

	a := w.ActedBy

	res, err := t.UPDATE(t.Name, t.Description, t.Definition, t.Version, t.UpdatedAt, t.ActedHow, t.ActedGrant, t.ActedToken, t.ActedName, t.ActedConfirmed).
		SET(w.Name, w.Description, def, w.Version, w.UpdatedAt, string(a.How), string(a.GrantID), string(a.TokenID), a.Name, a.Confirmed).
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

func (r *Workflows) SetActive(ctx context.Context, owner domain.UserID, id domain.WorkflowID, active bool, at time.Time, by domain.Actor) error {
	where, err := ownedWorkflow(owner, id)
	if err != nil {
		return err
	}

	t := table.Workflows

	return affectedOne(t.
		UPDATE(t.Active, t.UpdatedAt, t.ActedHow, t.ActedGrant, t.ActedToken, t.ActedName, t.ActedConfirmed).
		SET(active, at, string(by.How), string(by.GrantID), string(by.TokenID), by.Name, by.Confirmed).
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

func (r *Workflows) DeactivateAll(ctx context.Context, owner domain.UserID, at time.Time) ([]domain.WorkflowID, error) {
	uid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	var rows []model.Workflows

	t := table.Workflows

	// Off, no one is behind its runs any more (D407).
	err = t.
		UPDATE(t.Active, t.UpdatedAt, t.ActedHow, t.ActedGrant, t.ActedToken, t.ActedName, t.ActedConfirmed).
		SET(false, at, "", "", "", "", false).
		WHERE(table.Workflows.OwnerID.EQ(jet.UUID(uid)).AND(table.Workflows.Active.IS_TRUE())).
		RETURNING(table.Workflows.ID).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	ids := make([]domain.WorkflowID, len(rows))
	for i, m := range rows {
		ids[i] = domain.WorkflowID(m.ID.String())
	}

	return ids, nil
}

// ListActive leaves out workflows of disabled users, should any still be marked active:
// disabling a user deactivates them, but this way a failure there cannot restart them.
func (r *Workflows) ListActive(ctx context.Context) ([]domain.Workflow, error) {
	var rows []model.Workflows

	err := jet.SELECT(table.Workflows.AllColumns).
		FROM(table.Workflows.INNER_JOIN(table.Users, table.Users.ID.EQ(table.Workflows.OwnerID))).
		WHERE(table.Workflows.Active.IS_TRUE().AND(table.Users.DisabledAt.IS_NULL())).
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

	def, err := encodeDefinition(w.Nodes, w.Connections)
	if err != nil {
		return model.Workflows{}, err
	}

	return model.Workflows{
		ID:          id,
		OwnerID:     owner,
		Name:        w.Name,
		Description: w.Description,
		Active:      w.Active,
		Definition:  def,
		Pin:         pinOf(w.Pin),
		Version:     int32(w.Version),
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,

		ActedHow: string(w.ActedBy.How), ActedGrant: string(w.ActedBy.GrantID), ActedToken: string(w.ActedBy.TokenID),
		ActedName: w.ActedBy.Name, ActedConfirmed: w.ActedBy.Confirmed,
	}, nil
}

func toWorkflow(m model.Workflows) (domain.Workflow, error) {
	nodes, conns, err := decodeDefinition(m.Definition)
	if err != nil {
		return domain.Workflow{}, err
	}

	pin := 0
	if m.Pin != nil {
		pin = int(*m.Pin)
	}

	return domain.Workflow{
		ID:          domain.WorkflowID(m.ID.String()),
		OwnerID:     domain.UserID(m.OwnerID.String()),
		Name:        m.Name,
		Description: m.Description,
		Pin:         pin,
		Active:      m.Active,
		Nodes:       nodes,
		Connections: conns,
		Version:     int(m.Version),
		CreatedAt:   m.CreatedAt.UTC(),
		UpdatedAt:   m.UpdatedAt.UTC(),
		ActedBy: domain.Actor{
			GrantID: domain.GrantID(m.ActedGrant), TokenID: domain.APITokenID(m.ActedToken), Name: m.ActedName,
			How: domain.ActorHow(m.ActedHow), Confirmed: m.ActedConfirmed,
		},
	}, nil
}

// pinOf is a pin as kept: none for 0.
func pinOf(pin int) *int32 {
	if pin <= 0 {
		return nil
	}

	p := int32(pin)

	return &p
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

	ex := table.Executions

	// An execution that waits is saved again when it carries on.
	_, err = ex.INSERT(ex.AllColumns).MODEL(model.Executions{
		ID:              id,
		WorkflowID:      wid,
		WorkflowVersion: int32(e.WorkflowVersion),
		TriggeredBy:     string(e.TriggeredBy),
		Status:          string(e.Status),
		StartedAt:       e.StartedAt,
		FinishedAt:      timePtr(e.FinishedAt),
		Error:           e.Error,
		Runs:            string(raw),
		WaitingOn:       string(e.WaitingOn),
		WaitingUntil:    timePtr(e.WaitingUntil),
		ByHow:           string(e.By.How),
		ByGrant:         string(e.By.GrantID),
		ByToken:         string(e.By.TokenID),
		ByName:          e.By.Name,
		ByConfirmed:     e.By.Confirmed,
	}).
		ON_CONFLICT(ex.ID).
		DO_UPDATE(jet.SET(
			ex.Status.SET(ex.EXCLUDED.Status),
			ex.FinishedAt.SET(ex.EXCLUDED.FinishedAt),
			ex.Error.SET(ex.EXCLUDED.Error),
			ex.Runs.SET(ex.EXCLUDED.Runs),
			ex.WaitingOn.SET(ex.EXCLUDED.WaitingOn),
			ex.WaitingUntil.SET(ex.EXCLUDED.WaitingUntil),
		)).
		ExecContext(ctx, r.db.conn(ctx))

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
		WaitingOn:       domain.NodeID(m.WaitingOn),
		WaitingUntil:    timeOf(m.WaitingUntil),
		By: domain.Actor{
			GrantID: domain.GrantID(m.ByGrant), TokenID: domain.APITokenID(m.ByToken), Name: m.ByName,
			How: domain.ActorHow(m.ByHow), Confirmed: m.ByConfirmed,
		},
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
