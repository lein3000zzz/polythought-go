package plugin

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

func TestFromHost(t *testing.T) {
	refused := fromHost(status.Error(codes.PermissionDenied, "fetch from x: its permissions do not name it"))
	assert.ErrorIs(t, refused, ErrNotAllowed)
	assert.EqualError(t, refused, "fetch from x: its permissions do not name it", "the host's reason, as it gave it")

	assert.ErrorIs(t, fromHost(status.Error(codes.ResourceExhausted, "past")), ErrLimit)

	other := fromHost(status.Error(codes.Unavailable, "down"))
	assert.False(t, errors.Is(other, ErrNotAllowed) || errors.Is(other, ErrLimit))
	assert.EqualError(t, other, "down")
}

// logs is a HostServiceClient keeping the log lines it is sent.
type logs struct {
	pluginv1.HostServiceClient

	got []*pluginv1.LogRequest
}

func (l *logs) Log(_ context.Context, in *pluginv1.LogRequest, _ ...grpc.CallOption) (*pluginv1.LogResponse, error) {
	l.got = append(l.got, in)
	return &pluginv1.LogResponse{}, nil
}

func TestLog(t *testing.T) {
	rpc := &logs{}
	log := slog.New(&logHandler{host: &hostClient{rpc: rpc, session: "s-1"}})

	log.With("city", "Paris").WithGroup("odds").Warn("Rain", "yes", 0.42, slog.Group("by", "model", "a"), slog.Group("", "inline", true))
	log.Debug("Quiet")

	if assert.Len(t, rpc.got, 2) {
		assert.Equal(t, "s-1", rpc.got[0].GetSession())
		assert.Equal(t, "warn", rpc.got[0].GetLevel())
		assert.Equal(t, "Rain", rpc.got[0].GetMessage())
		assert.Equal(t, map[string]string{
			"city": "Paris", "odds.yes": "0.42", "odds.by.model": "a", "odds.inline": "true",
		}, rpc.got[0].GetFields(), "groups flattened into their keys")
		assert.Equal(t, "debug", rpc.got[1].GetLevel(), "every level goes: the server's own decides")
	}
}

func TestNoHost(t *testing.T) {
	h := &hostClient{}

	_, err := h.Fetch(context.Background(), Request{URL: "https://example.com"})
	assert.ErrorIs(t, err, errNoHost)

	ctx, end := h.Span(context.Background(), "x", nil)
	end(nil)
	assert.NotNil(t, ctx, "untraced, the work goes on")
}
