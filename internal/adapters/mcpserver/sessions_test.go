package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/mcpserver"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// ledger keeps sessions in memory, as the database does.
type ledger struct {
	mu         sync.Mutex
	rows       map[string]ports.MCPSession
	superseded []string
}

func (l *ledger) Open(_ context.Context, s ports.MCPSession) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.rows[s.ID]; !ok {
		l.rows[s.ID] = s
	}

	return nil
}

func (l *ledger) Seen(_ context.Context, id string, at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if s, ok := l.rows[id]; ok {
		s.SeenAt = at
		l.rows[id] = s
	}

	return nil
}

func (l *ledger) Close(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.rows, id)

	return nil
}

func (l *ledger) Supersede(_ context.Context, _ domain.UserID, holder, boot string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for id, s := range l.rows {
		if s.Holder == holder && s.Boot != boot {
			delete(l.rows, id)
			l.superseded = append(l.superseded, id)
		}
	}

	return nil
}

func (l *ledger) Of(_ context.Context, user domain.UserID, since time.Time) ([]ports.MCPSession, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []ports.MCPSession

	for _, s := range l.rows {
		if s.UserID == user && !s.SeenAt.Before(since) {
			out = append(out, s)
		}
	}

	return out, nil
}

func (*ledger) Prune(context.Context, time.Time) error { return nil }

func (l *ledger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.rows)
}

// sessionServer is a server keeping its sessions in l, and a client of alice's agent.
func sessionServer(t *testing.T, l *ledger) (*mcpserver.Server, func() *mcp.ClientSession) {
	t.Helper()

	srv, open := sessionServerWith(t, l, nil)

	return srv, func() *mcp.ClientSession {
		session, err := open("claude-code")
		require.NoError(t, err)

		return session
	}
}

// sessionServerWith is sessionServer on deps as with makes them, its client named as asked.
func sessionServerWith(
	t *testing.T, l *ledger, with func(*mcpserver.Deps),
) (*mcpserver.Server, func(name string) (*mcp.ClientSession, error)) {
	t.Helper()

	d := deps(alice, &trades{}, []domain.Scope{domain.ScopeRead})
	d.Sessions = l

	if with != nil {
		with(&d)
	}

	srv, err := mcpserver.New(mcpserver.Config{PublicURL: publicURL}, d)
	require.NoError(t, err)

	h := httptest.NewServer(srv)
	t.Cleanup(h.Close)

	return srv, func(name string) (*mcp.ClientSession, error) {
		session, err := mcp.NewClient(&mcp.Implementation{Name: name, Version: "2.1.300"}, nil).
			Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: h.URL, HTTPClient: &http.Client{Transport: bearer("poa_agent")}}, nil)
		if err == nil {
			t.Cleanup(func() { _ = session.Close() }) // before the server closes, which waits for its stream
		}

		return session, err
	}
}

// allowing lets n through, then none.
type allowing struct {
	mu   sync.Mutex
	n    int
	keys []string
}

func (a *allowing) Allow(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.keys = append(a.keys, key)
	a.n--

	return a.n >= 0
}

// A user's agents and tokens keep only so many sessions open, and open only so many a
// minute, each with a small first request (A10): each lasts, and costs, until it goes idle.
func TestSessionLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("past the agent's limit, its oldest closes", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{}}
		_, open := sessionServerWith(t, l, func(d *mcpserver.Deps) {
			d.SessionLimits = func() mcpserver.SessionLimits { return mcpserver.SessionLimits{PerHolder: 2, PerUser: 10} }
		})

		var sessions []*mcp.ClientSession

		for range 3 {
			s, err := open("claude-code")
			require.NoError(t, err)

			_, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
			require.NoError(t, err)

			sessions = append(sessions, s)
		}

		require.Eventually(t, func() bool { return l.count() == 2 }, time.Second, 5*time.Millisecond)

		_, err := sessions[0].CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.Error(t, err, "the oldest is closed")

		for _, s := range sessions[1:] {
			_, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
			require.NoError(t, err, "the newer stay")
		}
	})

	t.Run("past the user's, too", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{}}
		_, open := sessionServerWith(t, l, func(d *mcpserver.Deps) {
			d.SessionLimits = func() mcpserver.SessionLimits { return mcpserver.SessionLimits{PerUser: 1} }
		})

		first, err := open("claude-code")
		require.NoError(t, err)
		second, err := open("codex")
		require.NoError(t, err)

		_, err = second.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)

		require.Eventually(t, func() bool { return l.count() == 1 }, time.Second, 5*time.Millisecond)

		_, err = first.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.Error(t, err)
	})

	t.Run("so many new ones a minute, per user", func(t *testing.T) {
		limiter := &allowing{n: 1}
		_, open := sessionServerWith(t, &ledger{rows: map[string]ports.MCPSession{}}, func(d *mcpserver.Deps) {
			d.NewSessions = limiter
		})

		s, err := open("claude-code")
		require.NoError(t, err)

		_, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err, "calls in a session open are not counted")

		_, err = open("claude-code")
		require.ErrorContains(t, err, "Too Many Requests")
		assert.Equal(t, []string{"mcp-open:" + string(alice.ID), "mcp-open:" + string(alice.ID)}, limiter.keys,
			"each initialize, and nothing more")
	})

	t.Run("a revoked holder's, closed at once (A16)", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{}}
		srv, open := sessionServerWith(t, l, nil)

		s, err := open("claude-code")
		require.NoError(t, err)

		_, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)
		require.Eventually(t, func() bool { return l.count() == 1 }, time.Second, 5*time.Millisecond)

		srv.EndedToken("t-1") // another holder's: nothing closes

		_, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)

		srv.EndedGrant("g-1")

		require.Eventually(t, func() bool { return l.count() == 0 }, time.Second, 5*time.Millisecond)

		_, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.Error(t, err)
	})

	t.Run("a small first request, and the client's name cut", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{}}
		srv, open := sessionServerWith(t, l, nil)

		_, err := open(strings.Repeat("x", 100_000))
		require.Error(t, err, "too big to open a session with")

		s, err := open(strings.Repeat("y", 300))
		require.NoError(t, err)

		_, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)
		require.Eventually(t, func() bool { return l.count() == 1 }, time.Second, 5*time.Millisecond)

		got, err := srv.Connected(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 128, utf8.RuneCountInString(got[0].Client))
		assert.True(t, strings.HasSuffix(got[0].Client, "y…"))
	})
}

// The agents connected now are their sessions open on /mcp, kept so a restart does not
// forget them (D414).
func TestSessionsKept(t *testing.T) {
	ctx := context.Background()

	t.Run("one open, then closed by its client", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{}}
		srv, connectAgent := sessionServer(t, l)
		agent := connectAgent()

		_, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)

		require.Eventually(t, func() bool { return l.count() == 1 }, time.Second, 5*time.Millisecond)

		got, err := srv.Connected(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, agent.ID(), got[0].ID)
		assert.Equal(t, "grant:g-1", got[0].Holder)
		assert.Equal(t, "Claude Code", got[0].Name)
		assert.Equal(t, "claude-code 2.1.300", got[0].Client)
		assert.False(t, got[0].BeforeRestart)

		require.NoError(t, agent.Close())
		require.Eventually(t, func() bool { return l.count() == 0 }, time.Second, 5*time.Millisecond, "ended by its client")
	})

	t.Run("one from before a restart, until its agent comes back", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{
			"old": {ID: "old", UserID: alice.ID, Holder: "grant:g-1", Name: "Claude Code", Boot: "before", OpenedAt: at, SeenAt: time.Now()},
		}}
		srv, connectAgent := sessionServer(t, l)

		got, err := srv.Connected(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.True(t, got[0].BeforeRestart, "not forgotten")

		agent := connectAgent()
		_, err = agent.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			got, _ := srv.Connected(ctx, alice.ID)
			return len(got) == 1 && !got[0].BeforeRestart
		}, time.Second, 5*time.Millisecond, "its new session replaces it")
		assert.Equal(t, []string{"old"}, l.superseded)
	})

	t.Run("kept as the server stops", func(t *testing.T) {
		l := &ledger{rows: map[string]ports.MCPSession{}}
		srv, connectAgent := sessionServer(t, l)
		agent := connectAgent()

		_, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)
		require.Eventually(t, func() bool { return l.count() == 1 }, time.Second, 5*time.Millisecond)

		require.NoError(t, srv.Shutdown(ctx))
		require.NoError(t, agent.Close())

		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, 1, l.count(), "for its agent to come back to")
	})
}
