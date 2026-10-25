package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/semver"
)

const (
	opReleases = "github.Releases"

	// releasesPage is how many of the latest releases are looked at, newest first: the
	// newest version is among them unless an older line had a dozen patches since.
	releasesPage = 20
)

var _ ports.Releases = (*Client)(nil)

type release struct {
	Tag         string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	URL         string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

// Newest is the newest release by version among the latest few, pre-releases only when
// asked. Tags that are no version, as v0.1.2 is, are passed over.
func (c *Client) Newest(ctx context.Context, prereleases bool) (r domain.Release, err error) {
	done := c.kit.Start(ctx, opReleases)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opReleases, func(ctx context.Context, span trace.Span) (domain.Release, error) {
		var list []release
		if err := c.get(ctx, fmt.Sprintf("/releases?per_page=%d", releasesPage), &list); err != nil {
			return domain.Release{}, err
		}

		var (
			newest domain.Release
			best   semver.Version
			found  bool
		)

		for _, rel := range list {
			v, ok := semver.Parse(strings.TrimPrefix(rel.Tag, "v"))
			pre := rel.Prerelease || v.Prerelease()

			if !ok || rel.Draft || (pre && !prereleases) || !strings.HasPrefix(rel.URL, "https://") {
				continue
			}

			if !found || semver.Compare(v, best) > 0 {
				newest = domain.Release{Version: v.String(), Prerelease: pre, URL: rel.URL, PublishedAt: rel.PublishedAt}
				best, found = v, true
			}
		}

		if !found {
			return domain.Release{}, fmt.Errorf("%w: GitHub shows no release yet", domain.ErrNotFound)
		}

		span.SetAttributes(attribute.String("github.release", newest.Version))

		return newest, nil
	})
}
