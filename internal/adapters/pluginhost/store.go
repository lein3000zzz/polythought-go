package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

const (
	maxKey  = 256  // bytes of a store's key
	maxKeys = 1000 // keys a page lists at most; 100 when the plugin says none
)

// storeOf is the store of the session's node, which its manifest must mark remembers
// (D302): its own, in its workflow (D288).
func (a *api) storeOf(s *session) (ports.PluginNode, error) {
	switch {
	case !s.typ.Remembers:
		return ports.PluginNode{}, status.Errorf(codes.PermissionDenied,
			"%s keeps nothing: the manifest marks remembers the nodes that keep data", s.typ.Name)
	case a.deps.Store == nil:
		return ports.PluginNode{}, status.Error(codes.Unavailable, "the store is not available here")
	}

	return ports.PluginNode{Workflow: s.rc.WorkflowID, Node: s.rc.NodeID, Plugin: a.id}, nil
}

// checkKey refuses a key the store cannot keep: empty, too long, or not text.
func checkKey(key string) error {
	switch {
	case key == "" || len(key) > maxKey:
		return status.Errorf(codes.InvalidArgument, "a key: 1 to %d bytes", maxKey)
	case !utf8.ValidString(key) || strings.ContainsRune(key, 0):
		return status.Error(codes.InvalidArgument, "a key: UTF-8 text, no NUL")
	}

	return nil
}

func (a *api) GetValue(ctx context.Context, req *pluginv1.GetValueRequest) (*pluginv1.GetValueResponse, error) {
	return call(ctx, a, req.GetSession(), opGetValue, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.GetValueResponse, error) {
		n, err := a.storeOf(s)
		if err != nil {
			return nil, err
		}

		if err := checkKey(req.GetKey()); err != nil {
			return nil, err
		}

		value, ok, err := a.deps.Store.Get(ctx, n, req.GetKey())
		if err != nil {
			return nil, a.storeError(ctx, err)
		}

		return &pluginv1.GetValueResponse{Found: ok, Value: value}, nil
	})
}

func (a *api) SetValue(ctx context.Context, req *pluginv1.SetValueRequest) (*pluginv1.SetValueResponse, error) {
	return call(ctx, a, req.GetSession(), opSetValue, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.SetValueResponse, error) {
		n, err := a.storeOf(s)
		if err != nil {
			return nil, err
		}

		if err := checkKey(req.GetKey()); err != nil {
			return nil, err
		}

		if !json.Valid(req.GetValue()) {
			return nil, status.Error(codes.InvalidArgument, "a value: JSON")
		}

		if err := a.deps.Store.Set(ctx, n, req.GetKey(), req.GetValue(), s.limits.StoreBytes, time.Now()); err != nil {
			if errors.Is(err, domain.ErrStoreFull) {
				return nil, status.Errorf(codes.ResourceExhausted, "the node's store is full: its keys and values may take %d bytes", s.limits.StoreBytes)
			}

			return nil, a.storeError(ctx, err)
		}

		return &pluginv1.SetValueResponse{}, nil
	})
}

func (a *api) DeleteValue(ctx context.Context, req *pluginv1.DeleteValueRequest) (*pluginv1.DeleteValueResponse, error) {
	return call(ctx, a, req.GetSession(), opDeleteValue, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.DeleteValueResponse, error) {
		n, err := a.storeOf(s)
		if err != nil {
			return nil, err
		}

		if err := checkKey(req.GetKey()); err != nil {
			return nil, err
		}

		if err := a.deps.Store.Delete(ctx, n, req.GetKey()); err != nil {
			return nil, a.storeError(ctx, err)
		}

		return &pluginv1.DeleteValueResponse{}, nil
	})
}

func (a *api) ListKeys(ctx context.Context, req *pluginv1.ListKeysRequest) (*pluginv1.ListKeysResponse, error) {
	return call(ctx, a, req.GetSession(), opListKeys, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.ListKeysResponse, error) {
		n, err := a.storeOf(s)
		if err != nil {
			return nil, err
		}

		keys, err := a.deps.Store.Keys(ctx, n, req.GetPrefix(), req.GetAfter(), page(req.GetLimit(), 100, maxKeys))
		if err != nil {
			return nil, a.storeError(ctx, err)
		}

		return &pluginv1.ListKeysResponse{Keys: keys}, nil
	})
}

// storeError is why the store failed, logged; the plugin hears only that it did.
func (a *api) storeError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}

	a.log.Warn(ctx, "Plugin store failed", logging.Err(err))

	return status.Error(codes.Internal, "the store failed")
}

// page is how many a call gives: asked, its default when 0, never more than most.
func page(asked int32, byDefault, most int) int {
	switch {
	case asked <= 0:
		return byDefault
	case int(asked) > most:
		return most
	default:
		return int(asked)
	}
}
