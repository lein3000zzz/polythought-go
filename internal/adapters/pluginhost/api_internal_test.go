package pluginhost

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

func TestCount(t *testing.T) {
	t.Run("a node's run, past its calls, is over", func(t *testing.T) {
		s := &session{limits: Limits{HostCalls: 2}}

		require.NoError(t, s.count())
		require.NoError(t, s.count())
		assert.False(t, s.overLimit())

		require.EqualError(t, s.count(), "rpc error: code = ResourceExhausted desc = past the 2 calls a run may make")
		assert.True(t, s.overLimit())
	})

	t.Run("a trigger's, by the minute", func(t *testing.T) {
		now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		s := &session{trigger: true, limits: Limits{HostCalls: 2}, now: func() time.Time { return now }}

		require.NoError(t, s.count())
		require.NoError(t, s.count())
		require.Error(t, s.count())
		assert.False(t, s.overLimit(), "refused, not failed: a trigger runs on")

		now = now.Add(59 * time.Second)
		require.Error(t, s.count(), "the same minute")

		now = now.Add(time.Second)
		require.NoError(t, s.count(), "the next")
	})

	t.Run("logs, metrics and spans apart, with a cap of their own", func(t *testing.T) {
		s := &session{limits: Limits{HostCalls: 1}}

		for range maxTelemetry {
			require.NoError(t, s.report())
		}

		require.Error(t, s.report())
		require.NoError(t, s.count(), "the calls untouched")
	})
}

func TestClip(t *testing.T) {
	assert.Equal(t, "short", clip("short"))

	long := clip(string(make([]byte, maxText-1)) + "éé")
	assert.LessOrEqual(t, len(long), maxText+len("…"))
	assert.True(t, len(long) > 0 && long[len(long)-len("…"):] == "…", "cut at a rune's start, marked")
}

func TestHistoryFilter(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	f, err := historyFilter(&pluginv1.GetPriceHistoryRequest{TokenId: " tok ", Since: "2026-10-20T12:00:00Z"}, now)
	require.NoError(t, err)
	assert.Equal(t, domain.TokenID("tok"), f.TokenID)
	assert.Equal(t, now, f.Until, "until now")
	assert.Equal(t, 1000, f.Limit)
	assert.Equal(t, 24*time.Hour/1000, f.Bucket, "the day in as many points as it may have")

	f, err = historyFilter(&pluginv1.GetPriceHistoryRequest{
		TokenId: "tok", Since: "2026-10-20T12:00:00Z", Until: "2026-10-21T00:00:00Z", Bucket: "1h", Limit: 5000,
	}, now)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, f.Bucket)
	assert.Equal(t, 1000, f.Limit, "at most")

	for name, req := range map[string]*pluginv1.GetPriceHistoryRequest{
		"no token":          {Since: "2026-10-20T12:00:00Z"},
		"no since":          {TokenId: "tok"},
		"since after until": {TokenId: "tok", Since: "2026-10-21T13:00:00Z"},
		"a bucket too fine": {TokenId: "tok", Since: "2026-10-20T12:00:00Z", Bucket: "10s"},
		"a bucket unread":   {TokenId: "tok", Since: "2026-10-20T12:00:00Z", Bucket: "hourly"},
	} {
		_, err := historyFilter(req, now)
		assert.Error(t, err, name)
	}
}

func TestPage(t *testing.T) {
	assert.Equal(t, 20, page(0, 20, 100))
	assert.Equal(t, 7, page(7, 20, 100))
	assert.Equal(t, 100, page(500, 20, 100))
	assert.Equal(t, 20, page(-1, 20, 100))
}

func TestMarked(t *testing.T) {
	a := &api{name: "Rain <bot>"}

	got, err := a.marked("a & b", false, 100)
	require.NoError(t, err)
	assert.Equal(t, "🧩 <b>Rain &lt;bot&gt;</b>\na &amp; b", got, "its name escaped too")

	long, err := a.marked(strings.Repeat("x", 200), false, 100)
	require.NoError(t, err)
	assert.Equal(t, 100, utf8.RuneCountInString(long), "cut to fit, the name with it")
	assert.True(t, strings.HasSuffix(long, "…"))

	_, err = a.marked(strings.Repeat("x", 200), true, 100)
	assert.Error(t, err, "HTML is not cut: a tag could be")
}

func TestQuestionChecks(t *testing.T) {
	a := &api{name: "Echo"}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	q, err := a.question(&pluginv1.AskQuestionRequest{Text: "Rain?", Options: []string{"Yes", "No"}}, now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(time.Hour), q.Deadline, "an hour unless it says")
	assert.False(t, q.Remind, "no reminder without a reminder")
	assert.Equal(t, q.Deadline, q.until())
	assert.NotEmpty(t, q.Key)

	for name, req := range map[string]*pluginv1.AskQuestionRequest{
		"no options":      {Text: "?"},
		"eleven options":  {Text: "?", Options: strings.Split("a b c d e f g h i j k", " ")},
		"an option blank": {Text: "?", Options: []string{" "}},
		"an option long":  {Text: "?", Options: []string{strings.Repeat("x", 41)}},
		"an option twice": {Text: "?", Options: []string{"Yes", "Yes"}},
		"a timeout short": {Text: "?", Options: []string{"Yes"}, Timeout: "30s"},
		"no text":         {Options: []string{"Yes"}},
	} {
		_, err := a.question(req, now)
		assert.Error(t, err, name)
	}
}
