// Package data is the nodes of users' datasets: Record, which appends items to one, and
// Read dataset, which reads its rows.
package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const RecordType = "data.record"

// The fields Record adds to each row.
const (
	FieldAt        = "_at"
	FieldWorkflow  = "_workflow"
	FieldExecution = "_execution"
	FieldMode      = "_mode"
)

// Modes is the mode a workflow trades in, paper or live.
type Modes func(ctx context.Context, w domain.WorkflowID) (domain.TradingMode, error)

var _ engine.Node = (*Record)(nil)

// Record appends a row to one of the owner's datasets for each item: the fields chosen, or
// the whole item, with when, which workflow and run, and the mode it trades in. Items pass
// on unchanged.
type Record struct {
	store ports.DatasetStore
	modes Modes // optional: without it, rows have no _mode
	now   func() time.Time
}

func NewRecord(store ports.DatasetStore, modes Modes, now func() time.Time) *Record {
	return &Record{store: store, modes: modes, now: now}
}

// datasetParam is the dataset a node uses: one of its owner's, never shared.
func datasetParam(description string) engine.ParamSpec {
	return engine.ParamSpec{Name: "dataset", Label: "Dataset", Kind: engine.ParamDataset, Required: true, Description: description}
}

func (*Record) Type() engine.NodeType {
	return engine.NodeType{
		Name:  RecordType,
		Label: "Record",
		Description: "Adds a row to one of your datasets for each item: the fields chosen, or the whole item, with when (" +
			FieldAt + "), the workflow (" + FieldWorkflow + "), its run (" + FieldExecution + ") and whether it trades on " +
			"paper or live (" + FieldMode + "). Items pass on unchanged.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   &engine.Items{Passed: true},
		Params: []engine.ParamSpec{
			datasetParam("The dataset the rows go to: one of yours, or a new one."),
			{
				Name: "fields", Label: "Fields", Kind: engine.ParamList, Field: true,
				Description: "The fields recorded, like city or outcomes.0.price, each under its name as written; none for " +
					"the whole item.",
			},
		},
	}
}

func (n *Record) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return out, nil
	}

	// Neither setting takes expressions: the same for every item.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	name, err := p.String("dataset")
	if err != nil {
		return nil, err
	}

	var picked []string

	if p["fields"] != nil {
		if picked, err = p.Strings("fields"); err != nil {
			return nil, err
		}
	}

	// Gone, or kept from its owner: the run fails at once.
	d, err := n.store.Get(ctx, rc.OwnerID, name)
	if err != nil {
		return nil, err
	}

	extra := map[string]any{
		FieldAt:        n.now().UTC().Format(time.RFC3339Nano),
		FieldWorkflow:  string(rc.WorkflowID),
		FieldExecution: string(rc.ExecutionID),
	}

	if n.modes != nil {
		mode, err := n.modes(ctx, rc.WorkflowID)
		if err != nil {
			return nil, err
		}

		extra[FieldMode] = string(mode)
	}

	rows := make([]json.RawMessage, 0, len(items))
	from := make([]int, 0, len(items)) // each row's item

	for i, item := range items {
		raw, err := json.Marshal(rowOf(item.JSON, picked, extra))
		if err != nil {
			out.Fail(item, engine.ItemRef{Port: engine.DefaultPort, Index: i}, fmt.Errorf("not recorded: %w", err))
			continue
		}

		rows = append(rows, raw)
		from = append(from, i)
	}

	refused, err := n.store.Record(ctx, d, rows)

	switch {
	case err != nil && ctx.Err() != nil:
		return nil, ctx.Err()
	case err != nil && !errors.Is(err, domain.ErrDatasetFull):
		return nil, err
	}

	for j, i := range from {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		// Full: each fails saying so, for an error branch to take.
		if why := refused[j]; err != nil || why != nil {
			out.Fail(items[i], src, errors.Join(err, why))
			continue
		}

		out.Add(engine.DefaultPort, engine.Item{JSON: items[i].JSON, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// rowOf is what an item records: the fields picked, each under its path, or the whole
// item, and extra over them.
func rowOf(item map[string]any, picked []string, extra map[string]any) map[string]any {
	var row map[string]any

	if len(picked) == 0 {
		row = maps.Clone(item)
		if row == nil {
			row = map[string]any{}
		}
	} else {
		row = make(map[string]any, len(picked)+len(extra))

		for _, path := range picked {
			if v, ok := fields.Lookup(item, path); ok {
				row[path] = v
			}
		}
	}

	maps.Copy(row, extra)

	return row
}
