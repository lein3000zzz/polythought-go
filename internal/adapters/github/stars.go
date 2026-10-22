// Package github asks GitHub's API about Polythought's own repository.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/github"
	opStars    = "github.Stars"

	defaultBaseURL = "https://api.github.com"
	timeout        = 10 * time.Second
	maxResponse    = 1 << 20 // a repository's JSON is a few KiB
)

var _ ports.RepoStars = (*Stars)(nil)

type Config struct {
	// Repo is the repository, as owner/name.
	Repo string
	// BaseURL is GitHub's API; empty for github.com's.
	BaseURL string
	// Transport sends the requests; nil for http.DefaultTransport.
	Transport http.RoundTripper
}

// Stars asks GitHub's API, without a token, for a repository's star count. GitHub allows
// 60 such requests an hour from one address, and Polythought makes about one.
type Stars struct {
	url    string
	client *http.Client
	kit    *redkit.Kit
	scope  tracing.Scope
}

func New(util *observability.Utility, cfg Config) (*Stars, error) {
	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    util.Logger.With("component", "github"),
		Subsystem: "github",
		Buckets:   redkit.MediumLongBuckets,
		Component: "github",
	})
	if err != nil {
		return nil, fmt.Errorf("github: register metrics: %w", err)
	}

	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}

	return &Stars{
		url:    strings.TrimRight(base, "/") + "/repos/" + cfg.Repo,
		client: &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(cfg.Transport)},
		kit:    kit,
		scope:  tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
	}, nil
}

func (s *Stars) Stars(ctx context.Context) (n int, err error) {
	done := s.kit.Start(ctx, opStars)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, s.scope, opStars, func(ctx context.Context, span trace.Span) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
		if err != nil {
			return 0, err
		}

		// GitHub refuses requests without a User-Agent.
		req.Header.Set("User-Agent", "polythought")
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		res, err := s.client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("%w: GitHub: %w", domain.ErrUnavailable, err)
		}
		defer func() { _ = res.Body.Close() }()

		switch res.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			return 0, fmt.Errorf("%w: GitHub shows no such repository", domain.ErrNotFound)
		default:
			return 0, fmt.Errorf("%w: GitHub answered %d", domain.ErrUnavailable, res.StatusCode)
		}

		var repo struct {
			Stars *int `json:"stargazers_count"`
		}

		if err := json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(&repo); err != nil || repo.Stars == nil || *repo.Stars < 0 {
			return 0, fmt.Errorf("%w: GitHub's answer has no star count", domain.ErrUnavailable)
		}

		span.SetAttributes(attribute.Int("github.stars", *repo.Stars))

		return *repo.Stars, nil
	})
}
