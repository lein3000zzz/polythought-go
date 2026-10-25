package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	clientTimeout = 10 * time.Second
	maxAnswer     = 64 << 10 // a status, its log's end with it
)

var _ ports.Updater = (*Client)(nil)

// ClientConfig is where the server reaches the updater, and the token it takes; no
// token, no updater.
type ClientConfig struct {
	URL   string
	Token string
}

// Client is the server's: it asks the updater for updates. Its config is read for each
// request, as the Config page sets it.
type Client struct {
	config func() ClientConfig
	http   *http.Client
}

// NewClient asks the updater config names; transport sends the requests, nil for
// http.DefaultTransport.
func NewClient(config func() ClientConfig, transport http.RoundTripper) *Client {
	return &Client{config: config, http: &http.Client{Timeout: clientTimeout, Transport: otelhttp.NewTransport(transport)}}
}

func (c *Client) Set() bool { return c.config().Token != "" }

func (c *Client) Update(ctx context.Context, version string) error {
	body, err := json.Marshal(UpdateRequest{Version: version})
	if err != nil {
		return err
	}

	_, err = c.do(ctx, http.MethodPost, PathUpdate, body)

	return err
}

func (c *Client) Status(ctx context.Context) (domain.UpdaterStatus, error) {
	b, err := c.do(ctx, http.MethodGet, PathStatus, nil)
	if err != nil {
		return domain.UpdaterStatus{}, err
	}

	var st Status
	if err := json.Unmarshal(b, &st); err != nil {
		return domain.UpdaterStatus{}, fmt.Errorf("%w: the updater's answer is not a status", domain.ErrUnavailable)
	}

	return domain.UpdaterStatus{
		State: domain.UpdaterState(st.State), Version: st.Version, Error: st.Error, Log: st.Log,
		StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	cfg := c.config()
	if cfg.Token == "" {
		return nil, domain.ErrNoUpdater
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: the updater's address: %w", domain.ErrUnavailable, err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("%w: the updater did not answer at %s: is it running?", domain.ErrUnavailable, cfg.URL)
	}
	defer func() { _ = res.Body.Close() }()

	b, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return nil, fmt.Errorf("%w: the updater's answer: %w", domain.ErrUnavailable, err)
	}

	if res.StatusCode < 300 {
		return b, nil
	}

	var e errorBody
	if json.Unmarshal(b, &e) != nil || e.Error == "" {
		e.Error = fmt.Sprintf("it answered %d", res.StatusCode)
	}

	switch res.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: the updater refused the server's token: POLYTHOUGHT_UPDATER_TOKEN must be the same for both",
			domain.ErrUnavailable)
	case http.StatusConflict:
		return nil, domain.ErrUpdating
	case http.StatusBadRequest:
		return nil, fmt.Errorf("%w: the updater: %s", domain.ErrInvalidInput, e.Error)
	}

	return nil, fmt.Errorf("%w: the updater: %s", domain.ErrUnavailable, e.Error)
}
