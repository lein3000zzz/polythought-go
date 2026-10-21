package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
// implement what its manifest declares exits at once, saying why.
func Serve(p Plugin) {
	p.Manifest.SDK = SDKVersion

	if err := p.check(); err != nil {
		fmt.Fprintf(os.Stderr, "plugin %s: %v\n", p.Manifest.ID, err)
		os.Exit(1)
	}

	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         goplugin.PluginSet{Name: &grpcPlugin{impl: &p}},
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}

// HostPlugins are what the host dispenses: the plugin's client.
func HostPlugins() goplugin.PluginSet {
	return goplugin.PluginSet{Name: &grpcPlugin{}}
}

// grpcPlugin is go-plugin's glue: the server side for a plugin, the client for the host.
type grpcPlugin struct {
	goplugin.NetRPCUnsupportedPlugin

	impl *Plugin
}

func (g *grpcPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	pluginv1.RegisterPluginServiceServer(s, &server{p: g.impl})
	return nil
}

func (g *grpcPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, cc *grpc.ClientConn) (any, error) {
	return pluginv1.NewPluginServiceClient(cc), nil
}

// server serves the contract from a Plugin.
type server struct {
	pluginv1.UnimplementedPluginServiceServer

	p *Plugin
}

func (s *server) Describe(context.Context, *pluginv1.DescribeRequest) (*pluginv1.DescribeResponse, error) {
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
		for _, it := range input.GetItems() {
			j, err := decodeObject(it.GetJson())
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "an item: %v", err)
			}

			params, err := decodeObject(it.GetParams())
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "an item's settings: %v", err)
			}

			in.Add(input.GetPort(), Item{JSON: j, Params: params})
		}
	}

	out, err := node.Run(ctx, runOf(req.GetRun()), in)
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

	err = trigger.Start(stream.Context(), runOf(start.GetRun()), params, start.GetResumed(), emit)
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

func runOf(r *pluginv1.RunContext) Run {
	at, _ := time.Parse(time.RFC3339Nano, r.GetStartedAt())

	return Run{
		ExecutionID: r.GetExecutionId(), WorkflowID: r.GetWorkflowId(), OwnerID: r.GetOwnerId(), NodeID: r.GetNodeId(),
		StartedAt: at,
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
