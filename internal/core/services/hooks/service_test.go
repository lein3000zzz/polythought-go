package hooks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/services/hooks"
)

const (
	owner   = domain.UserID("u-1")
	flow    = domain.WorkflowID("wf-1")
	trigger = domain.NodeID("hook")
)

// repo keeps hooks in memory, by trigger and by hash.
type repo struct {
	mu     sync.Mutex
	hooks  map[domain.NodeID]domain.Hook
	hashes map[string]domain.NodeID
}

func newRepo() *repo {
	return &repo{hooks: map[domain.NodeID]domain.Hook{}, hashes: map[string]domain.NodeID{}}
}

func (r *repo) Hook(_ context.Context, w domain.WorkflowID, n domain.NodeID) (domain.Hook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	h, ok := r.hooks[n]
	if !ok || w != flow {
		return domain.Hook{}, domain.ErrNotFound
	}

	return h, nil
}

func (r *repo) ByHash(_ context.Context, hash []byte) (domain.Hook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.hashes[string(hash)]
	if !ok {
		return domain.Hook{}, domain.ErrNotFound
	}

	h := r.hooks[n]
	h.OwnerID = owner

	return h, nil
}

func (r *repo) Put(_ context.Context, h domain.Hook, hash []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for k, n := range r.hashes {
		if n == h.NodeID {
			delete(r.hashes, k)
		}
	}

	r.hooks[h.NodeID] = h
	r.hashes[string(hash)] = h.NodeID

	return nil
}

// box seals by putting its AAD before the text, so opening with another fails.
type box struct{}

func (box) Seal(_ context.Context, plain, aad []byte) ([]byte, error) {
	return append(append(append([]byte{}, aad...), '|'), plain...), nil
}

func (box) Open(_ context.Context, sealed, aad []byte) ([]byte, error) {
	prefix := append(append([]byte{}, aad...), '|')
	if !bytes.HasPrefix(sealed, prefix) {
		return nil, errors.New("not sealed to this")
	}

	return sealed[len(prefix):], nil
}

type workflows struct{}

func (workflows) Get(_ context.Context, o domain.UserID, id domain.WorkflowID) (domain.Workflow, error) {
	if o != owner || id != flow {
		return domain.Workflow{}, domain.ErrNotFound
	}

	return domain.Workflow{ID: flow, OwnerID: owner, Nodes: []domain.NodeSpec{
		{ID: trigger, Type: triggers.WebhookType},
		{ID: "log", Type: "output.log"},
	}}, nil
}

// limiter allows n calls a key.
type limiter struct {
	mu   sync.Mutex
	n    int
	seen map[string]int
}

func allow(n int) *limiter { return &limiter{n: n, seen: map[string]int{}} }

func (l *limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seen[key]++

	return l.seen[key] <= l.n
}

type fixture struct {
	svc     *hooks.Service
	enabled bool
	now     time.Time
}

func newFixture(t *testing.T, perHook, misses int) *fixture {
	t.Helper()

	f := &fixture{enabled: true, now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}
	f.svc = hooks.New(hooks.Deps{
		Repo: newRepo(), Workflows: workflows{}, Box: box{},
		Enabled: func() bool { return f.enabled },
		PerHook: allow(perHook), Misses: allow(misses),
		PublicURL: "https://pt.example/",
		Now:       func() time.Time { return f.now },
	})

	return f
}

func secretOf(t *testing.T, url string) string {
	t.Helper()

	secret, ok := strings.CutPrefix(url, "https://pt.example/hooks/")
	require.True(t, ok, url)
	require.Len(t, secret, 43, "256 bits, URL-safe")

	return secret
}

func TestAddress(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 100, 100)

	a, err := f.svc.Address(ctx, owner, flow, trigger)
	require.NoError(t, err)
	secret := secretOf(t, a.URL)
	assert.Equal(t, f.now, a.CreatedAt)
	assert.True(t, a.Enabled)
	assert.False(t, a.Listening, "the workflow is not on")

	again, err := f.svc.Address(ctx, owner, flow, trigger)
	require.NoError(t, err)
	assert.Equal(t, a.URL, again.URL, "the same address, opened again")

	_, cancel := f.svc.Listen(flow, trigger)
	defer cancel()

	again, err = f.svc.Address(ctx, owner, flow, trigger)
	require.NoError(t, err)
	assert.True(t, again.Listening)

	renewed, err := f.svc.Renew(ctx, owner, flow, trigger)
	require.NoError(t, err)
	assert.NotEqual(t, secret, secretOf(t, renewed.URL))

	_, err = f.svc.Call(ctx, secret, "1.2.3.4", nil, nil)
	require.ErrorIs(t, err, domain.ErrNotFound, "the old address answers no more")

	_, err = f.svc.Call(ctx, secretOf(t, renewed.URL), "1.2.3.4", nil, nil)
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		owner domain.UserID
		node  domain.NodeID
	}{
		{name: "another user's workflow", owner: "u-2", node: trigger},
		{name: "a node not an Inbound webhook", owner: owner, node: "log"},
		{name: "a node not saved", owner: owner, node: "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Address(ctx, tc.owner, flow, tc.node)
			require.ErrorIs(t, err, domain.ErrNotFound)

			_, err = f.svc.Renew(ctx, tc.owner, flow, tc.node)
			require.ErrorIs(t, err, domain.ErrNotFound)
		})
	}
}

func TestCall(t *testing.T) {
	ctx := context.Background()

	t.Run("a call is handed to the trigger, body and query read, the run's ID given", func(t *testing.T) {
		f := newFixture(t, 100, 100)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)

		calls, cancel := f.svc.Listen(flow, trigger)
		defer cancel()

		id, err := f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", []byte(`{"price": 0.53, "id": 12345678901234567890}`),
			map[string][]string{"city": {"nyc"}, "tag": {"a", "b"}})
		require.NoError(t, err)
		require.NotEmpty(t, id)

		c := <-calls
		assert.Equal(t, id, c.Execution)
		assert.Equal(t, map[string]any{"price": json.Number("0.53"), "id": json.Number("12345678901234567890")}, c.Body)
		assert.Equal(t, map[string]any{"city": "nyc", "tag": []any{"a", "b"}}, c.Query)
		assert.Equal(t, f.now, c.At)
	})

	t.Run("bodies", func(t *testing.T) {
		f := newFixture(t, 100, 100)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)

		calls, cancel := f.svc.Listen(flow, trigger)
		defer cancel()

		for _, tc := range []struct {
			name string
			body []byte
			want any
		}{
			{name: "none", body: nil, want: nil},
			{name: "spaces", body: []byte(" \n"), want: nil},
			{name: "a list", body: []byte(`[1, "two"]`), want: []any{json.Number("1"), "two"}},
			{name: "text, as an alert sends", body: []byte("BTC crossed 100k"), want: "BTC crossed 100k"},
			{name: "two JSON values, text", body: []byte(`{} {}`), want: `{} {}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", tc.body, nil)
				require.NoError(t, err)
				assert.Equal(t, tc.want, (<-calls).Body)
			})
		}

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", []byte{0xff, 0xfe}, nil)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("refused while an admin keeps them off", func(t *testing.T) {
		f := newFixture(t, 100, 100)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)
		assert.True(t, a.Enabled)

		f.enabled = false

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", nil, nil)
		require.ErrorIs(t, err, domain.ErrHooksOff)

		a, err = f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)
		assert.False(t, a.Enabled, "its owner is told")
	})

	t.Run("refused while its workflow is off, and once too many calls wait", func(t *testing.T) {
		f := newFixture(t, 1000, 100)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)
		secret := secretOf(t, a.URL)

		_, err = f.svc.Call(ctx, secret, "1.2.3.4", nil, nil)
		require.ErrorIs(t, err, domain.ErrHookIdle)

		_, cancel := f.svc.Listen(flow, trigger)

		for range 50 {
			_, err = f.svc.Call(ctx, secret, "1.2.3.4", nil, nil)
			require.NoError(t, err)
		}

		_, err = f.svc.Call(ctx, secret, "1.2.3.4", nil, nil)
		require.ErrorIs(t, err, domain.ErrRateLimited)
		assert.ErrorContains(t, err, "50 calls wait")

		cancel()

		_, err = f.svc.Call(ctx, secret, "1.2.3.4", nil, nil)
		require.ErrorIs(t, err, domain.ErrHookIdle, "switched off: no one listens")
	})

	t.Run("a trigger started again takes the calls; the old one's cancel leaves it be", func(t *testing.T) {
		f := newFixture(t, 100, 100)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)

		_, cancelOld := f.svc.Listen(flow, trigger)
		calls, cancel := f.svc.Listen(flow, trigger)
		defer cancel()

		cancelOld()

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", nil, nil)
		require.NoError(t, err)
		assert.Len(t, calls, 1)
	})

	t.Run("each hook's calls limited", func(t *testing.T) {
		f := newFixture(t, 2, 100)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)

		_, cancel := f.svc.Listen(flow, trigger)
		defer cancel()

		for range 2 {
			_, err = f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", nil, nil)
			require.NoError(t, err)
		}

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "5.6.7.8", nil, nil)
		require.ErrorIs(t, err, domain.ErrRateLimited, "whoever calls")
	})

	t.Run("an address guessing wrong too often is refused a minute, even a right secret", func(t *testing.T) {
		f := newFixture(t, 100, 3)
		a, err := f.svc.Address(ctx, owner, flow, trigger)
		require.NoError(t, err)

		_, cancel := f.svc.Listen(flow, trigger)
		defer cancel()

		for range 3 {
			_, err = f.svc.Call(ctx, "wrong", "6.6.6.6", nil, nil)
			require.ErrorIs(t, err, domain.ErrNotFound)
		}

		_, err = f.svc.Call(ctx, "wrong", "6.6.6.6", nil, nil)
		require.ErrorIs(t, err, domain.ErrNotFound, "the fourth miss blocks it")

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "6.6.6.6", nil, nil)
		require.ErrorIs(t, err, domain.ErrRateLimited)

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "1.2.3.4", nil, nil)
		require.NoError(t, err, "another address is not")

		f.now = f.now.Add(time.Minute)

		_, err = f.svc.Call(ctx, secretOf(t, a.URL), "6.6.6.6", nil, nil)
		require.NoError(t, err, "a minute later")
	})
}

// recorder keeps the executions that finish.
type recorder struct {
	done chan domain.Execution
}

func (r recorder) Save(_ context.Context, e domain.Execution) error {
	r.done <- e
	return nil
}

// TestRunsTakeTheirIDs runs a workflow from its hook, as the server does: each call a run,
// under the ID its caller was given.
func TestRunsTakeTheirIDs(t *testing.T) {
	f := newFixture(t, 100, 100)
	a, err := f.svc.Address(context.Background(), owner, flow, trigger)
	require.NoError(t, err)

	registry := engine.NewRegistry()
	require.NoError(t, registry.RegisterTrigger(triggers.NewWebhook(f.svc)))

	rec := recorder{done: make(chan domain.Execution, 2)}
	executor, err := engine.NewExecutor(observability.NoopUtility(), registry, engine.WithRecorder(rec))
	require.NoError(t, err)

	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)

	w := domain.Workflow{ID: flow, OwnerID: owner, Active: true, Nodes: []domain.NodeSpec{{ID: trigger, Type: triggers.WebhookType}}}

	go func() {
		stopped <- engine.NewRunner(observability.NoopUtility(), registry, executor).Run(ctx, w, false)
	}()

	// Called once the trigger listens.
	var ids []domain.ExecutionID

	require.Eventually(t, func() bool {
		id, err := f.svc.Call(context.Background(), secretOf(t, a.URL), "1.2.3.4", []byte(`{"n": 1}`), nil)
		if err == nil {
			ids = append(ids, id)
		}

		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	id, err := f.svc.Call(context.Background(), secretOf(t, a.URL), "1.2.3.4", nil, nil)
	require.NoError(t, err)

	ids = append(ids, id)

	for _, want := range ids {
		select {
		case e := <-rec.done:
			assert.Equal(t, want, e.ID)
			assert.Equal(t, domain.ExecutionSucceeded, e.Status, e.Error)
		case <-time.After(5 * time.Second):
			t.Fatal("no run")
		}
	}

	stop()
	require.NoError(t, <-stopped)
}
