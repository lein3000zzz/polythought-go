package mlmodels_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/mlmodels"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const alice = domain.UserID("u-1")

var (
	temps    = domain.Model{ID: "m-1", OwnerID: alice, Name: "temps", Kind: domain.ModelWorker}
	hosted   = domain.Model{ID: "m-2", OwnerID: alice, Name: "hosted", Kind: domain.ModelURL, URL: "https://models.example/predict", Credential: "c-1"}
	gpu      = mlmodels.Worker{Name: "gpu-1", Version: "1.2.0"}
	laptop   = mlmodels.Worker{Name: "laptop", Version: "1.1.0"}
	atBrowse = auth.Principal{User: domain.User{ID: alice}, SessionID: "s-1"}
)

type gate bool

func (g gate) DataAllowed(context.Context, domain.UserID) (bool, error) { return bool(g), nil }

func limits() mlmodels.Limits {
	return mlmodels.Limits{
		Models: 2, Workers: 2, Items: 3, RequestBytes: 200, AnswerBytes: 200,
		MaxWait: 5 * time.Second, Poll: 2 * time.Second, Lease: time.Minute,
	}
}

type fixture struct {
	svc  *mlmodels.Service
	repo *portsmocks.MockModelRepository
}

func setup(t *testing.T, change ...func(*mlmodels.Deps)) fixture {
	t.Helper()

	repo := portsmocks.NewMockModelRepository(t)
	deps := mlmodels.Deps{Repo: repo, Gate: gate(true), Limits: limits}

	for _, c := range change {
		c(&deps)
	}

	return fixture{svc: mlmodels.New(deps), repo: repo}
}

// serving has the repository know temps, and keep what it does.
func (f fixture) serving() fixture {
	f.repo.EXPECT().Get(mock.Anything, alice, "temps").Return(temps, nil).Maybe()
	f.repo.EXPECT().Answered(mock.Anything, temps.ID, mock.Anything).Return(nil).Maybe()
	f.repo.EXPECT().Failed(mock.Anything, temps.ID, mock.Anything, mock.Anything).Return(nil).Maybe()

	return f
}

func raws(js ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(js))
	for i, j := range js {
		out[i] = json.RawMessage(j)
	}

	return out
}

// connected waits until temps has n workers connected.
func (f fixture) connected(t *testing.T, n int) {
	t.Helper()

	require.Eventually(t, func() bool {
		v, err := f.svc.Get(context.Background(), alice, "temps")
		return err == nil && len(v.Workers) == n
	}, 2*time.Second, 5*time.Millisecond)
}

// take has w poll for temps' next request in the background.
func (f fixture) take(w mlmodels.Worker) <-chan mlmodels.Request {
	got := make(chan mlmodels.Request, 1)

	go func() {
		r, ok, err := f.svc.Take(context.Background(), temps, w)
		if err == nil && ok {
			got <- r
		}

		close(got)
	}()

	return got
}

type asked struct {
	answer mlmodels.Answer
	err    error
}

// ask asks temps about items in the background.
func (f fixture) ask(items []json.RawMessage, schema string, wait time.Duration) <-chan asked {
	done := make(chan asked, 1)

	go func() {
		a, err := f.svc.Ask(context.Background(), alice, "temps", items, json.RawMessage(schema), wait)
		done <- asked{a, err}
	}()

	return done
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("a worker model, its name checked, within the user's number", func(t *testing.T) {
		f := setup(t)
		f.repo.EXPECT().List(mock.Anything, alice).Return(nil, nil).Once()
		f.repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(m domain.Model) bool {
			return m.Name == "temps" && m.Kind == domain.ModelWorker && m.OwnerID == alice
		})).Return(nil).Once()

		v, err := f.svc.Create(ctx, alice, mlmodels.Spec{Name: " temps ", Kind: domain.ModelWorker})
		require.NoError(t, err)
		assert.Equal(t, "temps", v.Name)
		assert.Empty(t, v.Workers)
	})

	t.Run("what it refuses", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			spec mlmodels.Spec
			want error
		}{
			{name: "a name with capitals", spec: mlmodels.Spec{Name: "Temps", Kind: domain.ModelWorker}, want: domain.ErrInvalidInput},
			{name: "no kind", spec: mlmodels.Spec{Name: "temps"}, want: domain.ErrInvalidInput},
			{name: "a worker model with a URL", spec: mlmodels.Spec{Name: "temps", Kind: domain.ModelWorker, URL: "https://x.example"}, want: domain.ErrInvalidInput},
			{name: "a URL model without one", spec: mlmodels.Spec{Name: "hosted", Kind: domain.ModelURL}, want: domain.ErrInvalidInput},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := setup(t, func(d *mlmodels.Deps) { d.HTTP = &requester{} })
				_, err := f.svc.Create(ctx, alice, tc.spec)
				require.ErrorIs(t, err, tc.want)
			})
		}
	})

	t.Run("no more than the user may have", func(t *testing.T) {
		f := setup(t)
		f.repo.EXPECT().List(mock.Anything, alice).Return([]domain.Model{temps, hosted}, nil).Once()

		_, err := f.svc.Create(ctx, alice, mlmodels.Spec{Name: "third", Kind: domain.ModelWorker})
		require.ErrorIs(t, err, domain.ErrModelLimit)
	})

	t.Run("not while an admin keeps models from the user", func(t *testing.T) {
		f := setup(t, func(d *mlmodels.Deps) { d.Gate = gate(false) })

		_, err := f.svc.Create(ctx, alice, mlmodels.Spec{Name: "temps", Kind: domain.ModelWorker})
		require.ErrorIs(t, err, domain.ErrDataOff)
	})
}

func TestWorkerKeys(t *testing.T) {
	ctx := context.Background()

	t.Run("made at a browser, shown once, kept hashed; the worker serves that model alone", func(t *testing.T) {
		f := setup(t).serving()
		f.repo.EXPECT().Keys(mock.Anything, temps.ID).Return(nil, nil).Once()

		var kept []byte

		f.repo.EXPECT().CreateKey(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, k domain.WorkerKey, h []byte) error {
				kept = h
				assert.Equal(t, temps.ID, k.ModelID)
				assert.Len(t, k.Prefix, 10)

				return nil
			}).Once()

		secret, k, err := f.svc.CreateKey(ctx, atBrowse, "temps", "gpu box")
		require.NoError(t, err)
		assert.True(t, len(secret) > 40 && secret[:3] == mlmodels.KeyPrefix)
		assert.Equal(t, secret[:10], k.Prefix)
		assert.NotContains(t, string(kept), secret, "never the secret")

		f.repo.EXPECT().KeyByHash(mock.Anything, kept).Return(k, temps, nil).Once()
		f.repo.EXPECT().KeyUsed(mock.Anything, k.ID, mock.Anything).Return(nil).Once()

		m, err := f.svc.Worker(ctx, secret, "203.0.113.7")
		require.NoError(t, err)
		assert.Equal(t, temps.ID, m.ID)
	})

	t.Run("not by a token, nor for a URL model", func(t *testing.T) {
		f := setup(t)

		_, _, err := f.svc.CreateKey(ctx, auth.Principal{User: domain.User{ID: alice}, TokenID: "t-1"}, "temps", "x")
		require.ErrorIs(t, err, domain.ErrForbidden)

		f.repo.EXPECT().Get(mock.Anything, alice, "hosted").Return(hosted, nil).Once()

		_, _, err = f.svc.CreateKey(ctx, atBrowse, "hosted", "x")
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("a wrong key is refused, and an address trying many is held off a while", func(t *testing.T) {
		misses := 0
		f := setup(t, func(d *mlmodels.Deps) { d.Misses = limiter(func() bool { misses++; return misses < 2 }) })
		f.repo.EXPECT().KeyByHash(mock.Anything, mock.Anything).Return(domain.WorkerKey{}, domain.Model{}, domain.ErrNotFound).Twice()

		_, err := f.svc.Worker(ctx, "pw_wrong", "203.0.113.7")
		require.ErrorIs(t, err, domain.ErrUnauthenticated)

		_, err = f.svc.Worker(ctx, "pw_wrong-again", "203.0.113.7")
		require.ErrorIs(t, err, domain.ErrUnauthenticated)

		_, err = f.svc.Worker(ctx, "pw_right-maybe", "203.0.113.7")
		require.ErrorIs(t, err, domain.ErrRateLimited, "not even looked up")

		_, err = f.svc.Worker(ctx, "pt_a-token", "198.51.100.1")
		require.ErrorIs(t, err, domain.ErrUnauthenticated, "another address, and no worker key")
	})

	t.Run("a key whose owner may not use models", func(t *testing.T) {
		f := setup(t, func(d *mlmodels.Deps) { d.Gate = gate(false) })
		f.repo.EXPECT().KeyByHash(mock.Anything, mock.Anything).Return(domain.WorkerKey{ID: "k-1"}, temps, nil).Once()

		_, err := f.svc.Worker(ctx, "pw_secret", "203.0.113.7")
		require.ErrorIs(t, err, domain.ErrDataOff)
	})
}

type limiter func() bool

func (l limiter) Allow(string) bool { return l() }

func TestAsk(t *testing.T) {
	ctx := context.Background()

	t.Run("none connected fails at once", func(t *testing.T) {
		f := setup(t).serving()

		_, err := f.svc.Ask(ctx, alice, "temps", raws(`{"city":"NYC"}`), nil, time.Second)
		require.ErrorIs(t, err, domain.ErrModelOffline)
	})

	t.Run("a connected worker takes the request and answers each item, saying its version", func(t *testing.T) {
		f := setup(t).serving()
		taken := f.take(gpu)
		f.connected(t, 1)

		done := f.ask(raws(`{"city":"NYC"}`, `{"city":"LDN"}`), "", time.Second)

		r := <-taken
		assert.Equal(t, raws(`{"city":"NYC"}`, `{"city":"LDN"}`), r.Items)

		err := f.svc.Answer(ctx, temps, r.ID, gpu, raws(`{"high":51}`))
		require.ErrorIs(t, err, domain.ErrInvalidInput, "one an item")

		require.NoError(t, f.svc.Answer(ctx, temps, r.ID, gpu, raws(`{"high":51}`, `{"high":9}`)))

		got := <-done
		require.NoError(t, got.err)
		assert.Equal(t, raws(`{"high":51}`, `{"high":9}`), got.answer.Answers)
		assert.Equal(t, "1.2.0", got.answer.Version)
		assert.Equal(t, "gpu-1", got.answer.Worker)

		require.ErrorIs(t, f.svc.Answer(ctx, temps, r.ID, gpu, raws(`{}`, `{}`)), domain.ErrNotFound, "answered once")
	})

	t.Run("a worker's failure fails the request, saying why", func(t *testing.T) {
		f := setup(t).serving()
		taken := f.take(gpu)
		f.connected(t, 1)

		done := f.ask(raws(`{"city":"NYC"}`), "", time.Second)
		r := <-taken

		require.NoError(t, f.svc.Fail(ctx, temps, r.ID, gpu, "out of memory"))

		got := <-done
		require.Error(t, got.err)
		assert.Contains(t, got.err.Error(), "the model's worker gpu-1 failed: out of memory")
	})

	t.Run("no answer in time; a late one finds it gone", func(t *testing.T) {
		f := setup(t).serving()
		taken := f.take(gpu)
		f.connected(t, 1)

		done := f.ask(raws(`{"city":"NYC"}`), "", 50*time.Millisecond)
		r := <-taken

		got := <-done
		require.ErrorIs(t, got.err, domain.ErrModelTimeout)
		require.ErrorIs(t, f.svc.Answer(ctx, temps, r.ID, gpu, raws(`{}`)), domain.ErrNotFound)
	})

	t.Run("one held past its lease goes to another worker", func(t *testing.T) {
		f := setup(t, func(d *mlmodels.Deps) {
			d.Limits = func() mlmodels.Limits { l := limits(); l.Lease = 50 * time.Millisecond; return l }
		}).serving()

		first := f.take(gpu)
		f.connected(t, 1)

		done := f.ask(raws(`{"city":"NYC"}`), "", 2*time.Second)
		r := <-first

		second := f.take(laptop)
		again := <-second
		assert.Equal(t, r.ID, again.ID, "the same request")

		require.NoError(t, f.svc.Answer(ctx, temps, again.ID, laptop, raws(`{"high":50}`)))
		got := <-done
		require.NoError(t, got.err)
		assert.Equal(t, "laptop", got.answer.Worker)
	})

	t.Run("no more workers than a model may have", func(t *testing.T) {
		f := setup(t, func(d *mlmodels.Deps) {
			d.Limits = func() mlmodels.Limits { l := limits(); l.Workers = 1; return l }
		}).serving()
		f.take(gpu)
		f.connected(t, 1)

		_, _, err := f.svc.Take(ctx, temps, laptop)
		require.ErrorIs(t, err, domain.ErrModelLimit)
	})

	t.Run("answers that do not keep to the schema are refused, each alone", func(t *testing.T) {
		f := setup(t).serving()
		taken := f.take(gpu)
		f.connected(t, 1)

		schema := `{"type": "object", "properties": {"high": {"type": "number"}}, "required": ["high"]}`
		done := f.ask(raws(`{"city":"NYC"}`, `{"city":"LDN"}`), schema, time.Second)

		r := <-taken
		assert.JSONEq(t, schema, string(r.Schema), "the worker is told it")
		require.NoError(t, f.svc.Answer(ctx, temps, r.ID, gpu, raws(`{"high":51}`, `{"high":"warm"}`)))

		got := <-done
		require.NoError(t, got.err)
		assert.NoError(t, got.answer.Refused[0])
		assert.ErrorContains(t, got.answer.Refused[1], "does not keep to the schema")
	})

	t.Run("what it refuses to ask", func(t *testing.T) {
		f := setup(t).serving()

		for _, tc := range []struct {
			name   string
			items  []json.RawMessage
			schema string
			wait   time.Duration
		}{
			{name: "nothing", items: nil, wait: time.Second},
			{name: "more items than a request may hold", items: raws(`{}`, `{}`, `{}`, `{}`), wait: time.Second},
			{name: "more bytes", items: raws(`{"note":"` + string(make([]byte, 200)) + `"}`), wait: time.Second},
			{name: "a wait past the most", items: raws(`{}`), wait: time.Minute},
			{name: "no JSON Schema", items: raws(`{}`), schema: `{"type": 7}`, wait: time.Second},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := f.svc.Ask(ctx, alice, "temps", tc.items, json.RawMessage(tc.schema), tc.wait)
				require.ErrorIs(t, err, domain.ErrInvalidInput)
			})
		}
	})
}

// requester answers each call with answer, keeping what it was asked.
type requester struct {
	asked  []ports.HTTPRequest
	answer ports.HTTPResponse
	err    error
}

func (r *requester) Do(_ context.Context, req ports.HTTPRequest) (ports.HTTPResponse, error) {
	r.asked = append(r.asked, req)
	return r.answer, r.err
}

type vault struct{}

func (vault) Values(_ context.Context, owner domain.UserID, id domain.CredentialID, _ []string) (string, map[string]string, error) {
	if owner != alice || id != "c-1" {
		return "", nil, domain.ErrNotFound
	}

	return "http.header", map[string]string{"header": "X-Api-Key", "value": "s3cret"}, nil
}

func TestURLModels(t *testing.T) {
	ctx := context.Background()

	setupURL := func(t *testing.T, r *requester) fixture {
		t.Helper()

		f := setup(t, func(d *mlmodels.Deps) { d.HTTP, d.Credentials = r, vault{} })
		f.repo.EXPECT().Get(mock.Anything, alice, "hosted").Return(hosted, nil).Maybe()
		f.repo.EXPECT().Answered(mock.Anything, hosted.ID, mock.Anything).Return(nil).Maybe()
		f.repo.EXPECT().Failed(mock.Anything, hosted.ID, mock.Anything, mock.Anything).Return(nil).Maybe()

		return f
	}

	t.Run("POSTed the items as webhooks are, with its credential; its answers and version back", func(t *testing.T) {
		r := &requester{answer: ports.HTTPResponse{Status: 200, Body: []byte(`{"answers": [{"high": 51}], "version": "v7"}`)}}
		f := setupURL(t, r)

		a, err := f.svc.Ask(ctx, alice, "hosted", raws(`{"city":"NYC"}`), nil, 3*time.Second)
		require.NoError(t, err)
		assert.Equal(t, raws(`{"high": 51}`), a.Answers)
		assert.Equal(t, "v7", a.Version)

		require.Len(t, r.asked, 1)
		assert.Equal(t, "POST", r.asked[0].Method)
		assert.Equal(t, hosted.URL, r.asked[0].URL)
		assert.Equal(t, "s3cret", r.asked[0].Header["X-Api-Key"])
		assert.Equal(t, 3*time.Second, r.asked[0].Timeout)
		assert.JSONEq(t, `{"model": "hosted", "items": [{"city": "NYC"}]}`, string(r.asked[0].Body))
	})

	t.Run("an answer not 2xx, or the wrong number of answers, fails it, kept as its last error", func(t *testing.T) {
		for _, tc := range []struct {
			resp ports.HTTPResponse
			want string
		}{
			{resp: ports.HTTPResponse{Status: 503, Body: []byte("busy")}, want: "the model's URL answered 503: busy"},
			{resp: ports.HTTPResponse{Status: 200, Body: []byte(`{"answers": []}`)}, want: "gave 0 answers for 1 items"},
			{resp: ports.HTTPResponse{Status: 200, Body: []byte(`nope`)}, want: "answered no JSON"},
		} {
			r := &requester{answer: tc.resp}
			f := setupURL(t, r)

			_, err := f.svc.Ask(ctx, alice, "hosted", raws(`{"city":"NYC"}`), nil, time.Second)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		}
	})

	t.Run("its credential must be the owner's", func(t *testing.T) {
		f := setup(t, func(d *mlmodels.Deps) { d.HTTP, d.Credentials = &requester{}, vault{} })
		f.repo.EXPECT().List(mock.Anything, alice).Return(nil, nil).Maybe()

		_, err := f.svc.Create(ctx, alice, mlmodels.Spec{Name: "hosted", Kind: domain.ModelURL, URL: "https://models.example", Credential: "c-9"})
		require.True(t, errors.Is(err, domain.ErrNotFound))
	})
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

func asking(id domain.WorkflowID, owner domain.UserID, active bool, models ...string) domain.Workflow {
	w := domain.Workflow{ID: id, OwnerID: owner, Active: active, Nodes: []domain.NodeSpec{{ID: "manual", Type: "trigger.manual"}}}
	for i, name := range models {
		w.Nodes = append(w.Nodes, domain.NodeSpec{ID: domain.NodeID("ask" + string(rune('a'+i))), Type: "data.ask", Params: map[string]any{"model": name}})
	}

	return w
}

type perUser map[domain.UserID]bool

func (g perUser) DataAllowed(_ context.Context, u domain.UserID) (bool, error) { return g[u], nil }

func TestWorkflowsNamingModels(t *testing.T) {
	ctx := context.Background()
	const bob = domain.UserID("u-2")

	params := func(typ string) []string {
		if typ == "data.ask" {
			return []string{"model"}
		}

		return nil
	}

	setupFlows := func(t *testing.T, g mlmodels.Gate) (fixture, *flows) {
		t.Helper()

		f := setup(t, func(d *mlmodels.Deps) { d.Gate = g })
		w := &flows{off: map[domain.WorkflowID]string{}, all: []domain.Workflow{
			asking("bets", alice, true, "temps"),
			asking("idle", alice, false, "temps", "hosted"),
			asking("plain", alice, true),
			asking("bobs", bob, true, "temps"),
		}}
		f.svc.UseWorkflows(w, params)

		return f, w
	}

	t.Run("the owner's workflows naming each", func(t *testing.T) {
		f, _ := setupFlows(t, gate(true))

		used, err := f.svc.UsedBy(ctx, alice)
		require.NoError(t, err)
		assert.Len(t, used["temps"], 2)
		assert.Len(t, used["hosted"], 1)
	})

	t.Run("switched on only while its models are there and its owner may use them", func(t *testing.T) {
		f, w := setupFlows(t, gate(true))
		f.repo.EXPECT().List(mock.Anything, alice).Return([]domain.Model{temps}, nil).Twice()

		require.NoError(t, f.svc.CheckWorkflow(ctx, w.all[0]))
		require.NoError(t, f.svc.CheckWorkflow(ctx, w.all[2]), "naming none")

		err := f.svc.CheckWorkflow(ctx, w.all[1])
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.ErrorContains(t, err, `names the model "hosted", which you do not have`)

		f, w = setupFlows(t, gate(false))
		require.ErrorIs(t, f.svc.CheckWorkflow(ctx, w.all[0]), domain.ErrDataOff)
	})

	t.Run("deleting one switches off the owner's workflows on naming it, saying why", func(t *testing.T) {
		f, w := setupFlows(t, gate(true))
		f.repo.EXPECT().Get(mock.Anything, alice, "temps").Return(temps, nil).Once()
		f.repo.EXPECT().Delete(mock.Anything, temps.ID).Return(nil).Once()

		require.NoError(t, f.svc.Delete(ctx, alice, "temps"))
		assert.Equal(t, map[domain.WorkflowID]string{"bets": "the model temps is deleted"}, w.off, "not bob's, nor those off")
	})

	t.Run("a user kept from models has their workflows naming one switched off", func(t *testing.T) {
		f, w := setupFlows(t, perUser{bob: true})

		f.svc.SwitchOffLost(ctx)
		assert.Equal(t, map[domain.WorkflowID]string{"bets": "you may no longer use models"}, w.off)
	})
}
