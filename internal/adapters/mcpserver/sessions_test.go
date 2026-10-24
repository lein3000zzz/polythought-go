package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

	d := deps(alice, &trades{}, []domain.Scope{domain.ScopeRead})
	d.Sessions = l

	srv, err := mcpserver.New(mcpserver.Config{PublicURL: publicURL}, d)
	require.NoError(t, err)

	h := httptest.NewServer(srv)
	t.Cleanup(h.Close)

	return srv, func() *mcp.ClientSession {
		session, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "2.1.300"}, nil).
			Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: h.URL, HTTPClient: &http.Client{Transport: bearer("poa_agent")}}, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() }) // before the server closes, which waits for its stream

		return session
	}
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
