package github

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// releases is a page of them as GitHub lists them, newest first: a patch to an older line
// came last.
const releases = `[
	{"tag_name":"v0.1.1","draft":false,"prerelease":false,"html_url":"https://github.com/o/r/releases/tag/v0.1.1","published_at":"2026-10-25T10:00:00Z","body":"Notes"},
	{"tag_name":"v0.3.0-beta.2","draft":false,"prerelease":true,"html_url":"https://github.com/o/r/releases/tag/v0.3.0-beta.2","published_at":"2026-10-24T10:00:00Z"},
	{"tag_name":"v0.2.10","draft":false,"prerelease":false,"html_url":"https://github.com/o/r/releases/tag/v0.2.10","published_at":"2026-10-23T10:00:00Z"},
	{"tag_name":"v0.2.9","draft":false,"prerelease":false,"html_url":"https://github.com/o/r/releases/tag/v0.2.9","published_at":"2026-10-22T10:00:00Z"},
	{"tag_name":"v9.0.0","draft":true,"prerelease":false,"html_url":"https://github.com/o/r/releases/tag/v9.0.0","published_at":null},
	{"tag_name":"nightly","draft":false,"prerelease":false,"html_url":"https://github.com/o/r/releases/tag/nightly","published_at":"2026-10-21T10:00:00Z"},
	{"tag_name":"v8.0.0","draft":false,"prerelease":false,"html_url":"javascript:alert(1)","published_at":"2026-10-21T10:00:00Z"}
]`

func TestNewest(t *testing.T) {
	t.Run("the newest stable release by version", func(t *testing.T) {
		c, req := fakeGitHub(t, http.StatusOK, releases)

		r, err := c.Newest(context.Background(), false)
		require.NoError(t, err)
		assert.Equal(t, domain.Release{
			Version: "0.2.10", URL: "https://github.com/o/r/releases/tag/v0.2.10",
			PublishedAt: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC),
		}, r, "not the latest published, nor a draft, nor a tag that is no version, nor a link that is not to GitHub")

		assert.Equal(t, "/repos/lein3000zzz/polythought-go/releases", req.URL.Path)
		assert.Equal(t, "20", req.URL.Query().Get("per_page"))
		assert.Empty(t, req.Header.Get("Authorization"), "no token")
	})

	t.Run("pre-releases when asked", func(t *testing.T) {
		c, _ := fakeGitHub(t, http.StatusOK, releases)

		r, err := c.Newest(context.Background(), true)
		require.NoError(t, err)
		assert.Equal(t, "0.3.0-beta.2", r.Version)
		assert.True(t, r.Prerelease)
	})

	t.Run("a version with a hyphen is a pre-release, marked so or not", func(t *testing.T) {
		c, _ := fakeGitHub(t, http.StatusOK, `[{"tag_name":"v1.0.0-rc.1","prerelease":false,"html_url":"https://github.com/o/r/releases/tag/v1.0.0-rc.1"}]`)

		_, err := c.Newest(context.Background(), false)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestNewestFailing(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"none yet":        {http.StatusOK, `[]`, domain.ErrNotFound},
		"private or gone": {http.StatusNotFound, `{"message":"Not Found"}`, domain.ErrNotFound},
		"rate limited":    {http.StatusForbidden, `{"message":"API rate limit exceeded"}`, domain.ErrUnavailable},
		"not a list":      {http.StatusOK, `{"tag_name":"v1.0.0"}`, domain.ErrUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := fakeGitHub(t, tc.status, tc.body)

			_, err := c.Newest(context.Background(), true)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}
