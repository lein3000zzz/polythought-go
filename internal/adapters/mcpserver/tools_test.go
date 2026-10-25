package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type counter struct{ keys []string }

func (c *counter) Allow(key string) bool {
	c.keys = append(c.keys, key)
	return len(c.keys) <= 2
}

// A call is held to the caller's limit, then its scope, then the size of its answer.
func TestRun(t *testing.T) {
	ctx := context.Background()
	c := &counter{}
	s := &Server{limiter: c, log: logging.Noop()}
	agent := auth.Principal{User: domain.User{ID: "u-1"}, GrantID: "g-1", Scopes: []domain.Scope{domain.ScopeRead}}
	small := func(context.Context) (any, error) { return map[string]string{"ok": "yes"}, nil }

	out, err := s.run(ctx, agent, domain.ScopeRead, small)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ok": "yes"}, out)

	_, err = s.run(ctx, agent, domain.ScopeBuild, small)
	require.ErrorIs(t, err, domain.ErrInsufficientScope)

	_, err = s.run(ctx, agent, "", small)
	require.ErrorIs(t, err, domain.ErrRateLimited, "the third call in the counter's minute")
	assert.Equal(t, []string{"mcp:grant:g-1", "mcp:grant:g-1", "mcp:grant:g-1"}, c.keys, "counted by the grant")

	s.limiter = &counter{}
	big := func(context.Context) (any, error) { return strings.Repeat("x", maxResult), nil }

	_, err = s.run(ctx, agent, "", big)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
	assert.Contains(t, err.Error(), "ask for less")
}

// What the caller can fix is said as it is; anything else is the server's, and logged.
func TestToolError(t *testing.T) {
	s := &Server{log: logging.Noop()}

	err := s.toolError(context.Background(), "t", domain.ErrNotFound)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	err = s.toolError(context.Background(), "t", errors.New("pq: connection refused at 10.0.0.5"))
	assert.EqualError(t, err, "internal error")
}

func TestCallerKey(t *testing.T) {
	assert.Equal(t, "grant:g-1", callerKey(auth.Principal{GrantID: "g-1"}))
	assert.Equal(t, "token:t-1", callerKey(auth.Principal{TokenID: "t-1"}))
}

// An error longer than an answer may be is cut, saying how much (A25).
func TestLongErrorsCut(t *testing.T) {
	err := capped(errors.New(strings.Repeat("problème ", 10_000)))
	assert.Less(t, len(err.Error()), maxResult+100)
	assert.Contains(t, err.Error(), "KiB more, cut: fix what is above first")

	short := errors.New("short")
	assert.Equal(t, short, capped(short))
}
