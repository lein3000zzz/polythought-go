package datasets_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/datasets"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const alice = domain.UserID("u-1")

var now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

type gate bool

func (g gate) DataAllowed(context.Context, domain.UserID) (bool, error) { return bool(g), nil }

func limits() datasets.Limits {
	return datasets.Limits{Datasets: 2, Rows: 3, RowBytes: 64, Bytes: 200}
}

func service(t *testing.T, allowed bool) (*datasets.Service, *portsmocks.MockDatasetRepository) {
	t.Helper()

	repo := portsmocks.NewMockDatasetRepository(t)

	return datasets.New(datasets.Deps{Repo: repo, Gate: gate(allowed), Limits: limits, Now: func() time.Time { return now }}), repo
}

func rows(js ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(js))
	for i, j := range js {
		out[i] = json.RawMessage(j)
	}

	return out
}

var forecasts = domain.Dataset{ID: "d-1", OwnerID: alice, Name: "forecasts", KeyField: "city", Rows: 1, Bytes: 30}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("a dataset, its name checked, within the user's number", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().List(mock.Anything, alice).Return(nil, nil).Once()
		repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(d domain.Dataset) bool {
			return d.Name == "forecasts" && d.KeyField == "city" && d.OwnerID == alice && d.KeepRows == 2 && d.CreatedAt.Equal(now)
		})).Return(nil).Once()

		d, err := svc.Create(ctx, alice, datasets.Spec{Name: " forecasts ", KeyField: "city", KeepRows: 2})
		require.NoError(t, err)
		assert.Equal(t, "forecasts", d.Name)
	})

	t.Run("what it refuses", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			spec datasets.Spec
			want error
		}{
			{name: "a name with capitals", spec: datasets.Spec{Name: "Forecasts"}, want: domain.ErrInvalidInput},
			{name: "a name with spaces", spec: datasets.Spec{Name: "my data"}, want: domain.ErrInvalidInput},
			{name: "keeping fewer than no days", spec: datasets.Spec{Name: "a", KeepDays: -1}, want: domain.ErrInvalidInput},
			{name: "keeping more rows than may be", spec: datasets.Spec{Name: "a", KeepRows: 4}, want: domain.ErrInvalidInput},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, _ := service(t, true)
				_, err := svc.Create(ctx, alice, tc.spec)
				require.ErrorIs(t, err, tc.want)
			})
		}

		svc, repo := service(t, true)
		repo.EXPECT().List(mock.Anything, alice).Return([]domain.Dataset{{}, {}}, nil).Once()
		_, err := svc.Create(ctx, alice, datasets.Spec{Name: "third"})
		require.ErrorIs(t, err, domain.ErrDatasetFull, "two at most")
	})

	t.Run("not while an admin keeps datasets from the user", func(t *testing.T) {
		svc, _ := service(t, false)

		_, err := svc.Create(ctx, alice, datasets.Spec{Name: "a"})
		require.ErrorIs(t, err, domain.ErrDataOff)

		_, err = svc.List(ctx, alice)
		require.ErrorIs(t, err, domain.ErrDataOff)
	})
}

func TestAppend(t *testing.T) {
	ctx := context.Background()

	t.Run("rows keyed by the key field, compacted", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()
		repo.EXPECT().Bytes(mock.Anything, alice).Return(30, nil).Once()
		repo.EXPECT().Append(mock.Anything, domain.DatasetID("d-1"), []ports.NewRow{
			{Key: "NYC", At: now, Row: []byte(`{"city":"NYC","high":51}`)},
			{Key: "12345678901234567890", At: now, Row: []byte(`{"city":12345678901234567890}`)},
		}, 0, now).Return(nil).Once()
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()

		_, err := svc.Append(ctx, alice, "forecasts", rows(`{ "city": "NYC", "high": 51 }`, `{"city": 12345678901234567890}`))
		require.NoError(t, err)
	})

	t.Run("rows it refuses", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			rows []json.RawMessage
			want string
		}{
			{name: "none", rows: nil, want: "no rows"},
			{name: "not JSON", rows: rows(`{`), want: "row 0 is not JSON"},
			{name: "not an object", rows: rows(`[1]`), want: "row 0 is not an object"},
			{name: "null", rows: rows(`null`), want: "row 0 is not an object"},
			{name: "without the key", rows: rows(`{"high": 51}`), want: "row 0 has no city"},
			{name: "over the row size", rows: rows(`{"city": "NYC", "note": "` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `"}`), want: "more than the 64 a row may be"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, repo := service(t, true)
				repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()

				_, err := svc.Append(ctx, alice, "forecasts", tc.rows)
				require.ErrorIs(t, err, domain.ErrInvalidInput)
				assert.ErrorContains(t, err, tc.want)
			})
		}
	})

	t.Run("past the rows a dataset may hold: refused, or the oldest dropped where it keeps a number", func(t *testing.T) {
		full := forecasts
		full.Rows = 3

		svc, repo := service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(full, nil).Once()

		_, err := svc.Append(ctx, alice, "forecasts", rows(`{"city": "NYC"}`))
		require.ErrorIs(t, err, domain.ErrDatasetFull)
		assert.ErrorContains(t, err, "set Keep rows")

		rolling := full
		rolling.KeepRows = 3

		svc, repo = service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(rolling, nil).Twice()
		repo.EXPECT().Bytes(mock.Anything, alice).Return(30, nil).Once()
		repo.EXPECT().Append(mock.Anything, domain.DatasetID("d-1"), mock.Anything, 3, now).Return(nil).Once()

		_, err = svc.Append(ctx, alice, "forecasts", rows(`{"city": "NYC"}`))
		require.NoError(t, err)
	})

	t.Run("past the user's storage", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()
		repo.EXPECT().Bytes(mock.Anything, alice).Return(190, nil).Once()

		_, err := svc.Append(ctx, alice, "forecasts", rows(`{"city": "NYC"}`))
		require.ErrorIs(t, err, domain.ErrDatasetFull)
		assert.ErrorContains(t, err, "your datasets take 190 bytes of the 200 bytes")
	})
}

func TestReplace(t *testing.T) {
	ctx := context.Background()

	t.Run("staged, counted, then swapped in at once", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil)
		repo.EXPECT().Stage(mock.Anything, domain.DatasetID("d-1"), now).Return("s-1", nil).Once()
		repo.EXPECT().StageSize(mock.Anything, domain.DatasetID("d-1"), ports.DatasetStageID("s-1")).Return(1, 20, nil).Once()
		repo.EXPECT().Bytes(mock.Anything, alice).Return(50, nil).Once()
		repo.EXPECT().StageRows(mock.Anything, domain.DatasetID("d-1"), ports.DatasetStageID("s-1"), mock.Anything).Return(nil).Once()
		repo.EXPECT().Swap(mock.Anything, domain.DatasetID("d-1"), ports.DatasetStageID("s-1"), now).Return(nil).Once()

		stage, err := svc.StartReplace(ctx, alice, "forecasts")
		require.NoError(t, err)

		n, err := svc.StageRows(ctx, alice, "forecasts", stage, rows(`{"city": "NYC"}`, `{"city": "LDN"}`))
		require.NoError(t, err)
		assert.Equal(t, 3, n, "the one staged before, and these two")

		_, err = svc.FinishReplace(ctx, alice, "forecasts", stage)
		require.NoError(t, err)
	})

	t.Run("no more rows than a dataset may hold", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()
		repo.EXPECT().StageSize(mock.Anything, domain.DatasetID("d-1"), ports.DatasetStageID("s-1")).Return(2, 20, nil).Once()

		_, err := svc.StageRows(ctx, alice, "forecasts", "s-1", rows(`{"city": "NYC"}`, `{"city": "LDN"}`))
		require.ErrorIs(t, err, domain.ErrDatasetFull)
	})

	t.Run("a stage that is gone says so", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()
		repo.EXPECT().Swap(mock.Anything, domain.DatasetID("d-1"), ports.DatasetStageID("old"), now).Return(domain.ErrNotFound).Once()

		_, err := svc.FinishReplace(ctx, alice, "forecasts", "old")
		require.ErrorIs(t, err, domain.ErrNotFound)
		assert.ErrorContains(t, err, "no such replace in progress")
	})
}

func TestLookup(t *testing.T) {
	ctx := context.Background()

	svc, repo := service(t, true)
	repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Twice()
	repo.EXPECT().Latest(mock.Anything, domain.DatasetID("d-1"), "NYC").Return(domain.DatasetRow{Key: "NYC"}, nil).Once()
	repo.EXPECT().Latest(mock.Anything, domain.DatasetID("d-1"), "Mars").Return(domain.DatasetRow{}, domain.ErrNotFound).Once()

	row, err := svc.Lookup(ctx, alice, "forecasts", "NYC")
	require.NoError(t, err)
	assert.Equal(t, "NYC", row.Key)

	_, err = svc.Lookup(ctx, alice, "forecasts", "Mars")
	require.ErrorIs(t, err, domain.ErrNotFound)
	assert.ErrorContains(t, err, `no row with city "Mars"`)

	unkeyed := forecasts
	unkeyed.KeyField = ""

	svc, repo = service(t, true)
	repo.EXPECT().Get(mock.Anything, alice, "plain").Return(unkeyed, nil).Once()

	_, err = svc.Lookup(ctx, alice, "plain", "NYC")
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	svc, repo = service(t, true)
	repo.EXPECT().Get(mock.Anything, alice, "nope").Return(domain.Dataset{}, domain.ErrNotFound).Once()

	_, err = svc.Get(ctx, alice, "nope")
	assert.ErrorContains(t, err, `you have no dataset "nope"`)
}

func TestRecord(t *testing.T) {
	ctx := context.Background()

	t.Run("each row checked alone: those refused say why, the rest go in together", func(t *testing.T) {
		svc, repo := service(t, true)
		repo.EXPECT().Bytes(mock.Anything, alice).Return(30, nil).Once()
		repo.EXPECT().Append(mock.Anything, domain.DatasetID("d-1"), []ports.NewRow{
			{Key: "NYC", At: now, Row: []byte(`{"city":"NYC"}`)},
		}, 0, now).Return(nil).Once()

		refused, err := svc.Record(ctx, forecasts, rows(`{"city": "NYC"}`, `{"high": 51}`, `null`))
		require.NoError(t, err)
		require.Len(t, refused, 2)
		assert.ErrorContains(t, refused[1], "the row has no city, the dataset's key field")
		assert.ErrorContains(t, refused[2], "the row is not an object")
	})

	t.Run("all refused, nothing goes in", func(t *testing.T) {
		svc, _ := service(t, true)

		refused, err := svc.Record(ctx, forecasts, rows(`{"high": 51}`))
		require.NoError(t, err)
		assert.Len(t, refused, 1)
	})

	t.Run("full, all refused at once", func(t *testing.T) {
		svc, _ := service(t, true)
		full := forecasts
		full.Rows = 3

		_, err := svc.Record(ctx, full, rows(`{"city": "NYC"}`))
		require.ErrorIs(t, err, domain.ErrDatasetFull)
	})
}

func TestNewest(t *testing.T) {
	ctx := context.Background()

	svc, repo := service(t, true)
	repo.EXPECT().Newest(mock.Anything, domain.DatasetID("d-1"), 2).Return(nil, nil).Once()
	repo.EXPECT().Newest(mock.Anything, domain.DatasetID("d-1"), datasets.MaxPage).Return(nil, nil).Twice()

	for _, n := range []int{2, 0, datasets.MaxPage + 1} {
		_, err := svc.Newest(ctx, forecasts, n)
		require.NoError(t, err)
	}
}

// flows are users' workflows, those switched off kept with why.
type flows struct {
	all []domain.Workflow
	off map[domain.WorkflowID]string
}

func (f *flows) Of(_ context.Context, owner domain.UserID) ([]domain.Workflow, error) {
	var out []domain.Workflow

	for _, w := range f.all {
		if w.OwnerID == owner {
			out = append(out, w)
		}
	}

	return out, nil
}

func (f *flows) Pause(_ context.Context, why func(w domain.Workflow) string) (int, error) {
	n := 0

	for _, w := range f.all {
		if reason := why(w); w.Active && reason != "" {
			f.off[w.ID] = reason
			n++
		}
	}

	return n, nil
}

// datasetParams names Record's and Read dataset's dataset settings.
func datasetParams(typ string) []string {
	if typ == "data.record" || typ == "data.read" {
		return []string{"dataset"}
	}

	return nil
}

func naming(id domain.WorkflowID, owner domain.UserID, active bool, names ...string) domain.Workflow {
	w := domain.Workflow{ID: id, OwnerID: owner, Name: string(id), Active: active, Nodes: []domain.NodeSpec{{ID: "manual", Type: "trigger.manual"}}}
	for i, name := range names {
		w.Nodes = append(w.Nodes, domain.NodeSpec{ID: domain.NodeID(fmt.Sprintf("n%d", i)), Type: "data.record", Params: map[string]any{"dataset": name}})
	}

	return w
}

// perUser lets the users it names use datasets.
type perUser map[domain.UserID]bool

func (g perUser) DataAllowed(_ context.Context, u domain.UserID) (bool, error) { return g[u], nil }

func TestWorkflowsNamingDatasets(t *testing.T) {
	ctx := context.Background()
	const bob = domain.UserID("u-2")

	setup := func(t *testing.T, g datasets.Gate) (*datasets.Service, *portsmocks.MockDatasetRepository, *flows) {
		t.Helper()

		repo := portsmocks.NewMockDatasetRepository(t)
		svc := datasets.New(datasets.Deps{Repo: repo, Gate: g, Limits: limits, Now: func() time.Time { return now }})
		f := &flows{off: map[domain.WorkflowID]string{}, all: []domain.Workflow{
			naming("bets", alice, true, "forecasts", "forecasts"),
			naming("log", alice, false, "forecasts", "notes"),
			naming("plain", alice, true),
			naming("bobs", bob, true, "forecasts"),
		}}
		svc.UseWorkflows(f, datasetParams)

		return svc, repo, f
	}

	t.Run("the owner's workflows naming each, once each", func(t *testing.T) {
		svc, _, _ := setup(t, gate(true))

		used, err := svc.UsedBy(ctx, alice)
		require.NoError(t, err)
		assert.Len(t, used["forecasts"], 2)
		assert.Equal(t, domain.WorkflowID("log"), used["notes"][0].ID)
	})

	t.Run("switched on only while its datasets are there and its owner may use them", func(t *testing.T) {
		svc, repo, f := setup(t, gate(true))
		repo.EXPECT().List(mock.Anything, alice).Return([]domain.Dataset{forecasts}, nil).Twice()

		require.NoError(t, svc.CheckWorkflow(ctx, f.all[0]))
		require.NoError(t, svc.CheckWorkflow(ctx, f.all[2]), "naming none, nothing to look up")

		err := svc.CheckWorkflow(ctx, f.all[1])
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.ErrorContains(t, err, `node "n1" names the dataset "notes", which you do not have`)

		svc, _, f = setup(t, gate(false))
		require.ErrorIs(t, svc.CheckWorkflow(ctx, f.all[0]), domain.ErrDataOff)
	})

	t.Run("deleting one switches off the owner's workflows on naming it, saying why", func(t *testing.T) {
		svc, repo, f := setup(t, gate(true))
		repo.EXPECT().Get(mock.Anything, alice, "forecasts").Return(forecasts, nil).Once()
		repo.EXPECT().Delete(mock.Anything, domain.DatasetID("d-1")).Return(nil).Once()

		require.NoError(t, svc.Delete(ctx, alice, "forecasts"))
		assert.Equal(t, map[domain.WorkflowID]string{"bets": "the dataset forecasts is deleted"}, f.off, "not bob's, nor those off")
	})

	t.Run("a user kept from datasets has their workflows naming one switched off", func(t *testing.T) {
		svc, _, f := setup(t, perUser{bob: true})

		svc.SwitchOffLost(ctx)
		assert.Equal(t, map[domain.WorkflowID]string{"bets": "you may no longer use datasets"}, f.off)
	})
}
