package data_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/data"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
)

// store keeps datasets in memory, as the datasets service does: rows over maxRow bytes
// refused one by one, all refused when full.
type store struct {
	sets   map[string]*set
	off    bool
	full   bool
	maxRow int
}

type set struct {
	d    domain.Dataset
	rows []domain.DatasetRow
}

func newStore(sets ...domain.Dataset) *store {
	s := &store{sets: map[string]*set{}, maxRow: 1 << 10}
	for _, d := range sets {
		s.sets[d.Name] = &set{d: d}
	}

	return s
}

func (s *store) Get(_ context.Context, owner domain.UserID, name string) (domain.Dataset, error) {
	if s.off {
		return domain.Dataset{}, domain.ErrDataOff
	}

	got, ok := s.sets[name]
	if !ok || got.d.OwnerID != owner {
		return domain.Dataset{}, fmt.Errorf("%w: you have no dataset %q", domain.ErrNotFound, name)
	}

	return got.d, nil
}

func (s *store) Record(_ context.Context, d domain.Dataset, rows []json.RawMessage) (map[int]error, error) {
	if s.full {
		return nil, fmt.Errorf("%w: %q is full", domain.ErrDatasetFull, d.Name)
	}

	refused := map[int]error{}
	got := s.sets[d.Name]

	for i, r := range rows {
		if len(r) > s.maxRow {
			refused[i] = fmt.Errorf("%w: the row is %d bytes", domain.ErrInvalidInput, len(r))
			continue
		}

		key := ""

		if d.KeyField != "" {
			var obj map[string]any
			_ = json.Unmarshal(r, &obj)
			key = engine.Format(obj[d.KeyField])
		}

		got.rows = append(got.rows, domain.DatasetRow{Seq: int64(len(got.rows) + 1), Key: key, Row: r})
	}

	return refused, nil
}

func (s *store) Latest(_ context.Context, d domain.Dataset, key string) (domain.DatasetRow, error) {
	rows := s.sets[d.Name].rows
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Key == key {
			return rows[i], nil
		}
	}

	return domain.DatasetRow{}, domain.ErrNotFound
}

func (s *store) Newest(_ context.Context, d domain.Dataset, n int) ([]domain.DatasetRow, error) {
	rows := s.sets[d.Name].rows
	return rows[max(0, len(rows)-n):], nil
}

// row is a stored row as an object.
func row(t *testing.T, r domain.DatasetRow) map[string]any {
	t.Helper()

	var obj map[string]any
	require.NoError(t, json.Unmarshal(r.Row, &obj))

	return obj
}

var (
	at        = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	forecasts = domain.Dataset{ID: "d-1", OwnerID: "u-1", Name: "forecasts", KeyField: "city"}
	main      = engine.DefaultPort
)

func paper(context.Context, domain.WorkflowID) (domain.TradingMode, error) { return domain.ModePaper, nil }

// execute runs record into forecasts, then read from it by each item's city, on items.
func execute(t *testing.T, s *store, read map[string]any, items ...map[string]any) engine.Result {
	t.Helper()

	registry := engine.NewRegistry()
	require.NoError(t, registry.RegisterTrigger(triggers.Manual{}))
	require.NoError(t, registry.RegisterNode(data.NewRecord(s, paper, func() time.Time { return at })))
	require.NoError(t, registry.RegisterNode(data.NewRead(s)))

	executor, err := engine.NewExecutor(observability.NoopUtility(), registry)
	require.NoError(t, err)

	wf := domain.Workflow{
		ID: "wf-1", OwnerID: "u-1",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: triggers.ManualType},
			{ID: "record", Type: data.RecordType, Params: map[string]any{"dataset": "forecasts"}},
			{ID: "read", Type: data.ReadType, Params: read},
		},
		Connections: []domain.Connection{
			{From: "manual", FromPort: main, To: "record", ToPort: main},
			{From: "record", FromPort: main, To: "read", ToPort: main},
		},
	}

	in := engine.Ports{}
	for _, j := range items {
		in.Add(main, engine.Item{JSON: j})
	}

	res, err := executor.Execute(context.Background(), wf, "manual", in)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionSucceeded, res.Execution.Status, res.Execution.Error)

	return res
}

func TestRecordThenRead(t *testing.T) {
	nyc := map[string]any{"city": "NYC", "high": json.Number("51.25"), "models": map[string]any{"gfs": 50}}
	ldn := map[string]any{"city": "LDN", "high": 9}

	t.Run("each item recorded whole, with when, where and how; then its row read back by its key", func(t *testing.T) {
		s := newStore(forecasts)
		res := execute(t, s, map[string]any{"dataset": "forecasts", "key": "{{ city }}"}, nyc, ldn)

		rows := s.sets["forecasts"].rows
		require.Len(t, rows, 2)
		assert.Equal(t, map[string]any{
			"city": "NYC", "high": 51.25, "models": map[string]any{"gfs": float64(50)},
			"_at": "2026-11-24T12:00:00Z", "_workflow": "wf-1", "_execution": string(res.Execution.ID), "_mode": "paper",
		}, row(t, rows[0]))
		assert.Equal(t, "LDN", rows[1].Key)

		assert.Equal(t, []engine.Item{{JSON: nyc, Source: []engine.ItemRef{{Port: main}}}, {JSON: ldn, Source: []engine.ItemRef{{Port: main, Index: 1}}}},
			res.Outputs["record"][main], "items pass on unchanged")

		read := res.Outputs["read"][main]
		require.Len(t, read, 2)
		assert.Equal(t, "NYC", read[0].JSON["city"])
		assert.Equal(t, json.Number("51.25"), read[0].JSON["row"].(map[string]any)["high"], "a number exact")
		assert.Equal(t, "LDN", read[1].JSON["row"].(map[string]any)["city"])
	})

	t.Run("only the fields picked, each under its path", func(t *testing.T) {
		s := newStore(forecasts)
		node := data.NewRecord(s, nil, func() time.Time { return at })

		_, err := node.Run(context.Background(), engine.RunContext{
			OwnerID: "u-1", WorkflowID: "wf-1", ExecutionID: "x-1",
			Params: engine.LiteralParams(node.Type(), map[string]any{"dataset": "forecasts", "fields": []any{"city", "models.gfs", "absent"}}),
		}, engine.Ports{main: {{JSON: nyc}}})
		require.NoError(t, err)

		assert.Equal(t, map[string]any{
			"city": "NYC", "models.gfs": float64(50), "_at": "2026-11-24T12:00:00Z", "_workflow": "wf-1", "_execution": "x-1",
		}, row(t, s.sets["forecasts"].rows[0]), "without modes, no _mode")
	})

	t.Run("an item whose key has no row goes out of missing", func(t *testing.T) {
		s := newStore(forecasts)
		tky := map[string]any{"city": "LDN", "ask": "TKY"}
		res := execute(t, s, map[string]any{"dataset": "forecasts", "key": "{{ ask }}"}, map[string]any{"city": "NYC", "ask": "NYC"}, tky)

		require.Len(t, res.Outputs["read"][main], 1)
		missing := res.Outputs["read"][data.MissingPort]
		require.Len(t, missing, 1)
		assert.Equal(t, tky, missing[0].JSON, "as it came")
	})

	t.Run("every row, the newest so many, an item each", func(t *testing.T) {
		s := newStore(forecasts)
		tky := map[string]any{"city": "TKY", "high": 14}
		res := execute(t, s, map[string]any{"dataset": "forecasts", "read": data.ReadEvery, "limit": 2, "field": "forecast"}, nyc, ldn, tky)

		read := res.Outputs["read"][main]
		require.Len(t, read, 2)
		assert.Equal(t, "LDN", read[0].JSON["forecast"].(map[string]any)["city"], "oldest first")
		assert.Equal(t, "TKY", read[1].JSON["forecast"].(map[string]any)["city"])
		assert.Len(t, read[0].JSON, 1, "the row alone")
		assert.Len(t, read[0].Source, 3, "from every item it received")
	})
}

func TestRecordRefuses(t *testing.T) {
	run := func(s *store, items ...map[string]any) (engine.Ports, error) {
		node := data.NewRecord(s, paper, func() time.Time { return at })
		in := engine.Ports{}

		for _, j := range items {
			in.Add(main, engine.Item{JSON: j})
		}

		return node.Run(context.Background(), engine.RunContext{
			OwnerID: "u-1", WorkflowID: "wf-1", Params: engine.LiteralParams(node.Type(), map[string]any{"dataset": "forecasts"}),
		}, in)
	}

	t.Run("a row it refuses fails its item alone", func(t *testing.T) {
		s := newStore(forecasts)
		s.maxRow = 200

		out, err := run(s, map[string]any{"city": "NYC"}, map[string]any{"city": "LDN", "note": string(make([]byte, 300))})
		require.NoError(t, err)

		require.Len(t, out[main], 1)
		require.Len(t, out[engine.ErrorPort], 1)
		assert.Equal(t, "LDN", out[engine.ErrorPort][0].JSON["city"])
		assert.ErrorIs(t, out[engine.ErrorPort][0].Err, domain.ErrInvalidInput)
		assert.Len(t, s.sets["forecasts"].rows, 1)
	})

	t.Run("full, every item fails saying so", func(t *testing.T) {
		s := newStore(forecasts)
		s.full = true

		out, err := run(s, map[string]any{"city": "NYC"}, map[string]any{"city": "LDN"})
		require.NoError(t, err)

		assert.Empty(t, out[main])
		require.Len(t, out[engine.ErrorPort], 2)
		assert.ErrorIs(t, out[engine.ErrorPort][1].Err, domain.ErrDatasetFull)
	})

	t.Run("a dataset gone, or datasets kept from its owner, fails the run at once", func(t *testing.T) {
		_, err := run(newStore(), map[string]any{"city": "NYC"})
		require.ErrorIs(t, err, domain.ErrNotFound)

		s := newStore(forecasts)
		s.off = true

		_, err = run(s, map[string]any{"city": "NYC"})
		require.ErrorIs(t, err, domain.ErrDataOff)
	})
}

func TestReadRefuses(t *testing.T) {
	typ := data.NewRead(nil).Type()

	t.Run("by key needs a key", func(t *testing.T) {
		require.Error(t, typ.Check(engine.Params{"dataset": "forecasts", "read": data.ReadByKey}))
		require.NoError(t, typ.Check(engine.Params{"dataset": "forecasts", "read": data.ReadByKey, "key": "{{ city }}"}))
		require.NoError(t, typ.Check(engine.Params{"dataset": "forecasts", "read": data.ReadEvery}))
	})

	t.Run("so many rows, from 1 to 1,000", func(t *testing.T) {
		limit := typ.Params[3]
		require.Equal(t, "limit", limit.Name)
		assert.Error(t, limit.Check(0))
		assert.Error(t, limit.Check(1001))
		assert.NoError(t, limit.Check(1000))
	})

	t.Run("by key, a dataset with no key field", func(t *testing.T) {
		s := newStore(domain.Dataset{ID: "d-2", OwnerID: "u-1", Name: "notes"})
		node := data.NewRead(s)

		_, err := node.Run(context.Background(), engine.RunContext{
			OwnerID: "u-1", Params: engine.LiteralParams(node.Type(), map[string]any{"dataset": "notes", "key": "NYC"}),
		}, engine.Ports{main: {{JSON: map[string]any{}}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read every row instead")
	})

	t.Run("an item without a key fails alone", func(t *testing.T) {
		s := newStore(forecasts)
		node := data.NewRead(s)

		out, err := node.Run(context.Background(), engine.RunContext{
			OwnerID: "u-1", Params: engine.LiteralParams(node.Type(), map[string]any{"dataset": "forecasts", "key": " "}),
		}, engine.Ports{main: {{JSON: map[string]any{}}}})
		require.NoError(t, err)
		require.Len(t, out[engine.ErrorPort], 1)
		assert.True(t, errors.Is(out[engine.ErrorPort][0].Err, engine.ErrInvalidParam))
	})
}
