package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

// DefaultRPCURL is the public Polygon endpoint @polymarket/client uses.
const DefaultRPCURL = "https://polygon.drpc.org"

const (
	defaultRPCRPS = 5
	opRPCCall     = "rpc_call"
	opRPCEstimate = "rpc_estimate_gas"
)

// RPCClient reads Polygon through a JSON-RPC endpoint (D163).
type RPCClient interface {
	// Call runs calls with eth_call on the latest block, in one batch, giving their
	// results in order.
	Call(ctx context.Context, calls []Call) ([][]byte, error)
	// EstimateGas is a call's gas from from.
	EstimateGas(ctx context.Context, from common.Address, call Call) (uint64, error)
}

var _ RPCClient = (*DefaultRPCClient)(nil)

// ErrRPC means the RPC endpoint failed a request, or answered nonsense.
var ErrRPC = errors.New("polymarket: the Polygon RPC failed")

// DefaultRPCClient posts to the URL url gives at each request, which may hold an API
// key: it is never logged, nor put in errors.
type DefaultRPCClient struct {
	url     func() string
	client  *http.Client
	limiter *rate.Limiter
	kit     *redkit.Kit
	scope   tracing.Scope
	log     logging.Logger
}

func NewRPCClient(util *observability.Utility, cfg HTTPConfig, url func() string) (*DefaultRPCClient, error) {
	log := util.Logger.With("component", "polymarket.rpc")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "polymarket_rpc",
		Buckets:   redkit.MediumLongBuckets,
		Component: "polygon rpc",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register rpc metrics: %w", err)
	}

	cfg = cfg.withDefaults("", defaultRPCRPS)

	return &DefaultRPCClient{
		url:     url,
		client:  &http.Client{Timeout: cfg.Timeout, Transport: otelhttp.NewTransport(cfg.Transport)},
		limiter: rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst),
		kit:     kit,
		scope:   tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:     log,
	}, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type rpcCall struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	Data string `json:"data"`
}

func (c *DefaultRPCClient) Call(ctx context.Context, calls []Call) (_ [][]byte, err error) {
	done := c.kit.Start(ctx, opRPCCall)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opRPCCall, func(ctx context.Context, _ trace.Span) ([][]byte, error) {
		reqs := make([]rpcRequest, len(calls))
		for i, call := range calls {
			reqs[i] = rpcRequest{JSONRPC: "2.0", ID: i + 1, Method: "eth_call",
				Params: []any{rpcCall{To: call.To.Hex(), Data: hexutil.Encode(call.Data)}, "latest"}}
		}

		var resps []rpcResponse
		if err := c.post(ctx, reqs, &resps); err != nil {
			return nil, err
		}

		out := make([][]byte, len(calls))
		seen := 0

		for _, r := range resps {
			if r.ID < 1 || r.ID > len(calls) || out[r.ID-1] != nil {
				return nil, fmt.Errorf("%w: an answer to no request", ErrRPC)
			}

			result, err := r.hex("eth_call")
			if err != nil {
				return nil, err
			}

			out[r.ID-1] = result
			seen++
		}

		if seen != len(calls) {
			return nil, fmt.Errorf("%w: %d answers to %d calls", ErrRPC, seen, len(calls))
		}

		return out, nil
	})
}

func (c *DefaultRPCClient) EstimateGas(ctx context.Context, from common.Address, call Call) (_ uint64, err error) {
	done := c.kit.Start(ctx, opRPCEstimate)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opRPCEstimate, func(ctx context.Context, _ trace.Span) (uint64, error) {
		req := rpcRequest{JSONRPC: "2.0", ID: 1, Method: "eth_estimateGas",
			Params: []any{rpcCall{From: from.Hex(), To: call.To.Hex(), Data: hexutil.Encode(call.Data)}}}

		var resp rpcResponse
		if err := c.post(ctx, req, &resp); err != nil {
			return 0, err
		}

		s, err := resp.text("eth_estimateGas")
		if err != nil {
			return 0, err
		}

		gas, err := hexutil.DecodeUint64(s)
		if err != nil {
			return 0, fmt.Errorf("%w: eth_estimateGas gave no number", ErrRPC)
		}

		return gas, nil
	})
}

func (r rpcResponse) text(method string) (string, error) {
	if r.Error != nil {
		return "", fmt.Errorf("%w: %s: %s (%d)", ErrRPC, method, r.Error.Message, r.Error.Code)
	}

	var s string
	if err := json.Unmarshal(r.Result, &s); err != nil {
		return "", fmt.Errorf("%w: %s gave no hex", ErrRPC, method)
	}

	return s, nil
}

func (r rpcResponse) hex(method string) ([]byte, error) {
	s, err := r.text(method)
	if err != nil {
		return nil, err
	}

	b, err := hexutil.Decode(s)
	if err != nil && s != "0x" {
		return nil, fmt.Errorf("%w: %s gave no hex", ErrRPC, method)
	}

	return b, nil
}

// post sends body and decodes the answer. Errors name the RPC, never its URL.
func (c *DefaultRPCClient) post(ctx context.Context, body, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: its URL is not one", ErrRPC)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	start := time.Now()

	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.log.Warn(ctx, "Polygon RPC not reached", "took", time.Since(start))

		return fmt.Errorf("%w: not reached", ErrRPC)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: the answer broke off", ErrRPC)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: it answered %d", ErrRPC, resp.StatusCode)
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: the answer is not JSON-RPC", ErrRPC)
	}

	return nil
}
