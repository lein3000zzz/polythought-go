package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"

	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Handshake is what the host and a plugin check before anything else: a plugin started
// by hand, not by the host, says so and exits. ProtocolVersion is the contract's, v1.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "POLYTHOUGHT_PLUGIN",
	MagicCookieValue: "7c1f4b2e-polythought-plugin-v1",
}

// Name is the one plugin each plugin program serves, as go-plugin names it.
const Name = "polythought"

// Serve runs the plugin until the host stops it. It never returns; a plugin that does not
// implement what its manifest declares exits at once, saying why. Run as `plugin
// manifest`, it prints its manifest.json instead, for its archive.
func Serve(p Plugin) {
	p.Manifest.SDK = SDKVersion

	if err := p.check(); err != nil {
		fmt.Fprintf(os.Stderr, "plugin %s: %v\n", p.Manifest.ID, err)
		os.Exit(1)
	}

	if len(os.Args) == 2 && os.Args[1] == "manifest" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(p.Manifest); err != nil {
			fmt.Fprintf(os.Stderr, "plugin %s: %v\n", p.Manifest.ID, err)
			os.Exit(1)
		}

		os.Exit(0)
	}

	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         goplugin.PluginSet{Name: &grpcPlugin{impl: &p}},
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}

// HostPlugins are what the host dispenses, a *Client, serving host to the plugin. Its
// HostService server is held to the SDK's bounds, then to opts, which may change them; not
// an InTapHandle, which is the SDK's.
func HostPlugins(host pluginv1.HostServiceServer, opts ...grpc.ServerOption) goplugin.PluginSet {
	return goplugin.PluginSet{Name: &grpcPlugin{host: host, hostOpts: opts}}
}

// What the HostService server takes of a plugin (P3): a call no larger than the largest a
// host takes, a fetch's body or a value stored, 64 MiB at most as their settings allow,
// with room for the rest; hostStreams calls at once on a connection, more waiting their
// turn; and hostCalls over all of them, which go-plugin's broker lets a plugin open as
// many of as it likes, more refused.
const (
	hostMessage = 65 << 20
	hostHeaders = 64 << 10
	hostStreams = 64
	hostCalls   = 2 * hostStreams
)

// hostOptions are the HostService server's bounds, and its recovery from a call that
// panics, which answers it failed rather than take the server down (P2).
func hostOptions() []grpc.ServerOption {
	calls := &admission{most: hostCalls}

	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(hostMessage),
		grpc.MaxHeaderListSize(hostHeaders),
		grpc.MaxConcurrentStreams(hostStreams),
		grpc.InTapHandle(calls.admit),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: time.Minute}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: time.Minute, Timeout: 20 * time.Second}),
		grpc.ChainUnaryInterceptor(recoverUnary),
		grpc.ChainStreamInterceptor(recoverStream),
	}
}

// hostServer is the HostService's server, with opts: go-plugin's, its TLS, then the SDK's
// bounds, then the host's own.
func hostServer(host pluginv1.HostServiceServer, opts ...grpc.ServerOption) *grpc.Server {
	s := grpc.NewServer(opts...)
	pluginv1.RegisterHostServiceServer(s, host)

	return s
}

// admission bounds the calls the host serves a plugin at once, over all its connections:
// past most, refused.
type admission struct {
	n    atomic.Int64
	most int64
}

func (a *admission) admit(ctx context.Context, _ *tap.Info) (context.Context, error) {
	if a.n.Add(1) > a.most {
		a.n.Add(-1)
		return nil, status.Errorf(codes.ResourceExhausted, "past the %d calls the host takes from a plugin at once", a.most)
	}

	// The call's context ends with it, however it ends.
	context.AfterFunc(ctx, func() { a.n.Add(-1) })

	return ctx, nil
}

// recovered answers a call that panicked as failed, logging why.
func recovered(method string, err *error) {
	if v := recover(); v != nil {
		slog.Error("Plugin's host call panicked", "method", method, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
		*err = status.Error(codes.Internal, "the host failed on the call")
	}
}

func recoverUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ any, err error) {
	defer recovered(info.FullMethod, &err)

	return handler(ctx, req)
}

func recoverStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer recovered(info.FullMethod, &err)

	return handler(srv, ss)
}

// Client is a plugin as the host holds it: the contract's client, and the broker ID the
// host serves HostService on, which Describe tells the plugin.
type Client struct {
	pluginv1.PluginServiceClient

	HostID uint32
}

// grpcPlugin is go-plugin's glue: the server side for a plugin, the client for the host.
type grpcPlugin struct {
	goplugin.NetRPCUnsupportedPlugin

	impl     *Plugin
	host     pluginv1.HostServiceServer
	hostOpts []grpc.ServerOption
}

func (g *grpcPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	pluginv1.RegisterPluginServiceServer(s, &server{p: g.impl, broker: broker})
	return nil
}

func (g *grpcPlugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, cc *grpc.ClientConn) (any, error) {
	c := &Client{PluginServiceClient: pluginv1.NewPluginServiceClient(cc)}

	if g.host != nil {
		c.HostID = broker.NextId()

		go broker.AcceptAndServe(c.HostID, func(opts []grpc.ServerOption) *grpc.Server {
			return hostServer(g.host, slices.Concat(opts, hostOptions(), g.hostOpts)...)
		})
	}

	return c, nil
}

// server serves the contract from a Plugin.
type server struct {
	pluginv1.UnimplementedPluginServiceServer

	p      *Plugin
	broker *goplugin.GRPCBroker

	mu   sync.Mutex
	host pluginv1.HostServiceClient // dialled at Describe; nil until then, or with none
}

// Describe answers the manifest, and dials the host's HostService first: the broker
// keeps its address only a moment.
func (s *server) Describe(_ context.Context, req *pluginv1.DescribeRequest) (*pluginv1.DescribeResponse, error) {
	if id := req.GetHost(); id != 0 {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.host == nil {
			conn, err := s.broker.Dial(id)
			if err != nil {
				return nil, status.Errorf(codes.Unavailable, "dial the host: %v", err)
			}

			s.host = pluginv1.NewHostServiceClient(conn)
		}
	}

	raw, err := json.Marshal(s.p.Manifest)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "manifest: %v", err)
	}

	return &pluginv1.DescribeResponse{Manifest: raw}, nil
}

func (s *server) RunNode(ctx context.Context, req *pluginv1.RunNodeRequest) (*pluginv1.RunNodeResponse, error) {
	node := s.p.Nodes[req.GetNode()]
	if node == nil {
		return nil, status.Errorf(codes.NotFound, "no node %q", req.GetNode())
	}

	in := Ports{}

	for _, input := range req.GetInputs() {
		for i, it := range input.GetItems() {
			j, err := decodeObject(it.GetJson())
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "an item: %v", err)
			}

			params, err := decodeObject(it.GetParams())
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "an item's settings: %v", err)
			}

			in.Add(input.GetPort(), Item{JSON: j, Params: params, ref: &ItemRef{Port: input.GetPort(), Index: i}})
		}
	}

	run := s.runOf(req.GetRun())

	var (
		out Ports
		err error
	)

	if r := req.GetResume(); r != nil {
		resumer, ok := node.(Resumer)
		if !ok {
			return nil, status.Errorf(codes.FailedPrecondition, "node %q has no Resume to finish its run", req.GetNode())
		}

		out, err = resumer.Resume(ctx, run, in, Answer{TimedOut: r.GetTimedOut(), Option: int(r.GetOption()), Label: r.GetLabel(), state: r.GetState()})
	} else {
		out, err = node.Run(ctx, run, in)
	}

	if wait, ok := errors.AsType[*Wait](err); ok {
		if _, ok := node.(Resumer); !ok {
			return nil, status.Errorf(codes.FailedPrecondition, "node %q asked, but has no Resume to finish its run", req.GetNode())
		}

		return &pluginv1.RunNodeResponse{Wait: &pluginv1.Wait{State: wait.state}}, nil
	}

	if err != nil {
		return nil, status.Error(codes.Aborted, err.Error())
	}

	outputs, err := toOutputs(out)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &pluginv1.RunNodeResponse{Outputs: outputs}, nil
}

func (s *server) RunTrigger(stream grpc.BidiStreamingServer[pluginv1.RunTriggerRequest, pluginv1.RunTriggerResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}

	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "the first message must start the trigger")
	}

	trigger := s.p.Triggers[start.GetTrigger()]
	if trigger == nil {
		return status.Errorf(codes.NotFound, "no trigger %q", start.GetTrigger())
	}

	params, err := decodeObject(start.GetParams())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "the trigger's settings: %v", err)
	}

	// Emits wait for their ack, one at a time, as the host runs each execution through.
	emit := func(ctx context.Context, out Ports) error {
		outputs, err := toOutputs(out)
		if err != nil {
			return err
		}

		if err := stream.Send(&pluginv1.RunTriggerResponse{Outputs: outputs}); err != nil {
			return err
		}

		ack, err := stream.Recv()
		if err != nil {
			return err
		}

		if msg := ack.GetAck().GetError(); msg != "" {
			return errors.New(msg)
		}

		return ctx.Err()
	}

	err = trigger.Start(stream.Context(), s.runOf(start.GetRun()), params, start.GetResumed(), emit)
	if err != nil && stream.Context().Err() == nil && !errors.Is(err, io.EOF) {
		return status.Error(codes.Aborted, err.Error())
	}

	return nil
}

func (s *server) CheckCredential(_ context.Context, req *pluginv1.CheckCredentialRequest) (*pluginv1.CheckCredentialResponse, error) {
	c := req.GetCredential()

	checks, ok := s.p.Credentials[c.GetType()]
	if !ok || checks.Check == nil {
		return &pluginv1.CheckCredentialResponse{}, nil
	}

	if err := checks.Check(c.GetValues()); err != nil {
		return &pluginv1.CheckCredentialResponse{Error: err.Error()}, nil
	}

	return &pluginv1.CheckCredentialResponse{}, nil
}

func (s *server) TestCredential(ctx context.Context, req *pluginv1.TestCredentialRequest) (*pluginv1.TestCredentialResponse, error) {
	c := req.GetCredential()

	checks, ok := s.p.Credentials[c.GetType()]
	if !ok || checks.Test == nil {
		return nil, status.Errorf(codes.NotFound, "credential type %q cannot be tested", c.GetType())
	}

	if err := checks.Test(ctx, c.GetValues()); err != nil {
		return &pluginv1.TestCredentialResponse{Error: err.Error()}, nil
	}

	return &pluginv1.TestCredentialResponse{}, nil
}

// runOf is the run a request names, its Host the host's for its session.
func (s *server) runOf(r *pluginv1.RunContext) Run {
	at, _ := time.Parse(time.RFC3339Nano, r.GetStartedAt())

	s.mu.Lock()
	host := &hostClient{rpc: s.host, session: r.GetSession()}
	s.mu.Unlock()

	settings, err := decodeObject(r.GetSettings())
	if err != nil {
		settings = map[string]any{}
	}

	return Run{
		ExecutionID: r.GetExecutionId(), WorkflowID: r.GetWorkflowId(), OwnerID: r.GetOwnerId(), NodeID: r.GetNodeId(),
		StartedAt: at, Host: host, Log: slog.New(&logHandler{host: host}), Settings: settings,
	}
}

func toOutputs(out Ports) ([]*pluginv1.Output, error) {
	outputs := make([]*pluginv1.Output, 0, len(out))

	for port, items := range out {
		o := &pluginv1.Output{Port: port}

		for _, it := range items {
			j := it.JSON
			if j == nil {
				j = map[string]any{}
			}

			raw, err := json.Marshal(j)
			if err != nil {
				return nil, fmt.Errorf("an item out of %s: %w", port, err)
			}

			oi := &pluginv1.OutputItem{Json: raw, Error: it.Err}
			for _, src := range it.Sources {
				oi.Sources = append(oi.Sources, &pluginv1.ItemRef{Port: src.Port, Index: int32(src.Index)}) //nolint:gosec // a run's items
			}

			o.Items = append(o.Items, oi)
		}

		outputs = append(outputs, o)
	}

	return outputs, nil
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
