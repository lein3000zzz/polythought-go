package plugin

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// brittleHost is a host whose Log panics, and whose SetValue takes any value.
type brittleHost struct {
	pluginv1.UnimplementedHostServiceServer
}

func (brittleHost) Log(context.Context, *pluginv1.LogRequest) (*pluginv1.LogResponse, error) {
	panic("runtime error: slice bounds out of range [:-11]")
}

func (brittleHost) SetValue(context.Context, *pluginv1.SetValueRequest) (*pluginv1.SetValueResponse, error) {
	return &pluginv1.SetValueResponse{}, nil
}

// served is a HostService client of host, served as the SDK serves the host's, with opts.
func served(t *testing.T, host pluginv1.HostServiceServer, opts ...grpc.ServerOption) pluginv1.HostServiceClient {
	t.Helper()

	ln := bufconn.Listen(1 << 20)
	s := hostServer(host, append(hostOptions(), opts...)...)

	go func() { _ = s.Serve(ln) }()

	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient("passthrough:///host",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(hostMessage+1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return pluginv1.NewHostServiceClient(conn)
}

func TestHostServer(t *testing.T) {
	ctx := context.Background()

	t.Run("a call that panics fails, and the server serves on (P2)", func(t *testing.T) {
		rpc := served(t, brittleHost{})

		_, err := rpc.Log(ctx, &pluginv1.LogRequest{Message: "hi"})
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.EqualError(t, err, "rpc error: code = Internal desc = the host failed on the call", "nothing of why")

		_, err = rpc.SetValue(ctx, &pluginv1.SetValueRequest{Key: "k", Value: []byte("1")})
		assert.NoError(t, err)
	})

	t.Run("calls up to its bound, not gRPC's 4 MiB, and the host's bounds over it (P3)", func(t *testing.T) {
		rpc := served(t, brittleHost{})

		_, err := rpc.SetValue(ctx, &pluginv1.SetValueRequest{Key: "k", Value: []byte(strings.Repeat("x", 5<<20))})
		require.NoError(t, err, "a fetch's body or a value as large as the settings allow")

		tight := served(t, brittleHost{}, grpc.MaxRecvMsgSize(1<<10))

		_, err = tight.SetValue(ctx, &pluginv1.SetValueRequest{Key: "k", Value: []byte(strings.Repeat("x", 2<<10))})
		assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	})
}

func TestAdmission(t *testing.T) {
	a := &admission{most: 2}

	first, done := context.WithCancel(context.Background())

	_, err := a.admit(first, nil)
	require.NoError(t, err)

	_, err = a.admit(context.Background(), nil)
	require.NoError(t, err)

	_, err = a.admit(context.Background(), nil)
	require.Error(t, err, "past most, over every connection")
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))

	done()

	assert.Eventually(t, func() bool {
		_, err := a.admit(context.Background(), nil)
		return err == nil
	}, time.Second, 10*time.Millisecond, "a call ended frees its place")
}

func TestRecoverStream(t *testing.T) {
	err := recoverStream(nil, nil, &grpc.StreamServerInfo{FullMethod: "/x/Y"}, func(any, grpc.ServerStream) error {
		panic("boom")
	})

	assert.Equal(t, codes.Internal, status.Code(err))
}
