package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// patience is how long a test waits for what should happen: it goes on the moment it does,
// so this is room for a busy machine.
const patience = 30 * time.Second

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

// logBook is a logging core keeping the lines it is given, their attributes by key.
type logBook struct {
	mu    sync.Mutex
	lines []logged
}

type logged struct {
	msg   string
	attrs map[string]any
}

func (b *logBook) last() logged {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.lines) == 0 {
		return logged{}
	}

	return b.lines[len(b.lines)-1]
}

type bookCore struct {
	book *logBook
	with []logging.Attr
}

func (c bookCore) Enabled(context.Context, logging.Level) bool { return true }

func (c bookCore) Log(_ context.Context, _ logging.Level, msg string, attrs ...logging.Attr) {
	line := logged{msg: msg, attrs: map[string]any{}}
	for _, a := range append(slices.Clone(c.with), attrs...) {
		line.attrs[a.Key] = a.Value
	}

	c.book.mu.Lock()
	c.book.lines = append(c.book.lines, line)
	c.book.mu.Unlock()
}

func (c bookCore) With(attrs ...logging.Attr) logging.Core {
	return bookCore{book: c.book, with: append(slices.Clone(c.with), attrs...)}
}

func (c bookCore) WithGroup(string) logging.Core { return c }

// opened is an api logging to a book, and a session of it open in ctx, closed with the
// test.
func opened(t *testing.T, ctx context.Context) (*api, *logBook, *session, string, func()) {
	t.Helper()

	a, err := newAPI(Deps{})
	require.NoError(t, err)

	book := &logBook{}
	a.id, a.name, a.log = "echo", "Echo", logging.New(bookCore{book: book})

	s := &session{ctx: ctx, rc: engine.RunContext{ExecutionID: "ex-1", WorkflowID: "wf-1", OwnerID: "alice", NodeID: "n-1"}}

	token, closed, err := a.open(s)
	require.NoError(t, err)
	t.Cleanup(closed)

	return a, book, s, token, closed
}

func TestMarkedNoRoom(t *testing.T) {
	for _, name := range []string{"Movers" + strings.Repeat("\u200b", 4090), strings.Repeat("<", 1100)} {
		a := &api{name: name}

		require.NotPanics(t, func() {
			_, err := a.marked("hello", false, maxMessage)
			assert.EqualError(t, err, "rpc error: code = FailedPrecondition desc = the plugin's name leaves no room for a message")

			_, err = a.marked("hello", false, maxMessage-closingRoom)
			assert.Error(t, err, "nor for a question")
		}, "a name past Check's, as one approved before it bounded names (P2)")
	}
}

func TestCallsInFlight(t *testing.T) {
	a, _, _, token, closed := opened(t, context.Background())

	var (
		going, most atomic.Int32
		wg          sync.WaitGroup
	)

	release := make(chan struct{})

	for range maxInFlight + 2 {
		wg.Go(func() {
			_, _ = call(context.Background(), a, token, "test", func(context.Context, *session, trace.Span) (int, error) {
				n := going.Add(1)
				defer going.Add(-1)

				for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
				}

				<-release

				return 0, nil
			})
		})
	}

	assert.Eventually(t, func() bool { return going.Load() == maxInFlight }, patience, time.Millisecond)
	assert.Never(t, func() bool { return going.Load() > maxInFlight }, 100*time.Millisecond, time.Millisecond,
		"the rest wait their turn (P12)")

	close(release)
	wg.Wait()
	assert.EqualValues(t, maxInFlight, most.Load())

	t.Run("those waiting end with the run's session", func(t *testing.T) {
		var holding atomic.Int32

		hold := make(chan struct{})
		defer close(hold)

		for range maxInFlight {
			go func() {
				_, _ = call(context.Background(), a, token, "test", func(context.Context, *session, trace.Span) (int, error) {
					holding.Add(1)
					<-hold

					return 0, nil
				})
			}()
		}

		require.Eventually(t, func() bool { return holding.Load() == maxInFlight }, patience, time.Millisecond)

		waited := make(chan error, 1)

		go func() {
			_, err := call(context.Background(), a, token, "test", func(context.Context, *session, trace.Span) (int, error) { return 0, nil })
			waited <- err
		}()

		time.Sleep(50 * time.Millisecond)
		closed()

		select {
		case err := <-waited:
			assert.Equal(t, codes.Canceled, status.Code(err))
		case <-time.After(patience):
			t.Fatal("a call waited on after its run's session closed")
		}
	})
}

func TestCallsEndWithTheSession(t *testing.T) {
	a, _, _, token, closed := opened(t, context.Background())

	started := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		_, err := call(context.Background(), a, token, "test", func(ctx context.Context, _ *session, _ trace.Span) (int, error) {
			close(started)
			<-ctx.Done()

			return 0, ended(ctx.Err())
		})
		done <- err
	}()

	<-started
	closed()

	select {
	case err := <-done:
		assert.Equal(t, codes.Canceled, status.Code(err), "the run's ended, its plugin's call with it (P15)")
	case <-time.After(patience):
		t.Fatal("a call went on after its run's session closed")
	}
}

// askers is a Telegram asker that holds each question until let go, and keeps those closed.
type askers struct {
	asked, let chan struct{}

	mu     sync.Mutex
	closed map[int]string
}

func (k *askers) AskUser(context.Context, domain.UserID, ports.Question) (int, error) {
	close(k.asked)
	<-k.let

	return 7, nil
}

func (k *askers) CloseQuestion(_ context.Context, _ domain.UserID, messageID int, html string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.closed[messageID] = html

	return nil
}

func TestQuestionAfterTheRun(t *testing.T) {
	a, _, s, token, _ := opened(t, context.Background())

	k := &askers{asked: make(chan struct{}), let: make(chan struct{}), closed: map[int]string{}}
	a.deps.Telegram.Asker, a.perms.telegram = k, true

	done := make(chan error, 1)

	go func() {
		_, err := a.AskQuestion(context.Background(), &pluginv1.AskQuestionRequest{Session: token, Text: "Rain?", Options: []string{"Yes"}})
		done <- err
	}()

	<-k.asked
	assert.Nil(t, s.settle(), "the plugin answered the run while its question was being sent")
	close(k.let)

	require.NoError(t, <-done)
	assert.Contains(t, k.closed[7], "No longer waiting for an answer", "closed however it ends (D311, P15)")

	_, err := a.AskQuestion(context.Background(), &pluginv1.AskQuestionRequest{Session: token, Text: "Again?", Options: []string{"Yes"}})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestFailures(t *testing.T) {
	ctx := context.Background()
	a, book, s, _, _ := opened(t, ctx)

	inside := errors.New("failed to connect to `host=db.internal user=polythought database=polythought`: password authentication failed")

	err := a.sourceError(ctx, s, "wallets", inside)
	assert.EqualError(t, err, "rpc error: code = Unavailable desc = wallets failed", "nothing of the server's insides (P13)")

	line := book.last()
	assert.Equal(t, "Plugin's call failed", line.msg)
	assert.Equal(t, inside, line.attrs["error"], "logged, with the run's")
	assert.EqualValues(t, "wf-1", line.attrs["workflow_id"])

	err = a.sourceError(ctx, s, "order book", fmt.Errorf("dial host=db.internal: %w", context.DeadlineExceeded))
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.NotContains(t, err.Error(), "db.internal")

	assert.EqualError(t, a.sourceError(ctx, s, "cancel orders", fmt.Errorf("ledger: %w", domain.ErrNotFound)),
		"rpc error: code = NotFound desc = cancel orders: not found")

	_, why := a.failure(ctx, s, "order 2", fmt.Errorf("wallet at https://polygon.example/key-1: %w", domain.ErrNotEnoughCash))
	assert.Equal(t, "order 2: not enough cash for the order", why, "a domain error, in its own words only")

	assert.EqualError(t, a.telegramError(ctx, s, fmt.Errorf("chat 42: %w", domain.ErrTelegramNotLinked)),
		"rpc error: code = FailedPrecondition desc = "+domain.ErrTelegramNotLinked.Error())
	assert.EqualError(t, a.telegramError(ctx, s, errors.New(`Post "https://api.telegram.org/bot<token>/sendMessage": EOF`)),
		"rpc error: code = Unavailable desc = telegram failed")

	assert.Equal(t, codes.Canceled, status.Code(a.storeError(ctx, fmt.Errorf("pq host=db.internal: %w", context.Canceled))))
	assert.NotContains(t, a.storeError(ctx, fmt.Errorf("pq host=db.internal: %w", context.Canceled)).Error(), "db.internal")
}

// keys is a store whose Keys says what it was asked; the rest it has not.
type keys struct {
	ports.PluginStore

	asked []string
}

func (k *keys) Keys(_ context.Context, _ ports.PluginNode, prefix, after string, _ int) ([]string, error) {
	k.asked = append(k.asked, prefix, after)
	return nil, nil
}

func TestListKeysChecked(t *testing.T) {
	ctx := context.Background()
	a, _, s, token, _ := opened(t, ctx)

	st := &keys{}
	a.deps.Store, s.typ.Remembers = st, true

	_, err := a.ListKeys(ctx, &pluginv1.ListKeysRequest{Session: token, Prefix: "seen:", After: "seen:a"})
	require.NoError(t, err)

	for name, req := range map[string]*pluginv1.ListKeysRequest{
		"a prefix too long": {Prefix: strings.Repeat("k", maxKey+1)},
		"after too long":    {After: strings.Repeat("k", 4<<20)},
		"a prefix not text": {Prefix: "\xff"},
		"after with a NUL":  {After: "a\x00"},
	} {
		req.Session = token

		_, err := a.ListKeys(ctx, req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), name)
	}

	assert.Equal(t, []string{"seen:", "seen:a"}, st.asked, "only what a key could start with reaches the store (P24)")
}

func TestSpanBounds(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	ctx, node := tp.Tracer("test").Start(context.Background(), "node")

	defer node.End()

	a, _, _, token, _ := opened(t, ctx)

	big := map[string]string{}
	for i := range maxFields {
		big[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", maxText)
	}

	first, err := a.StartSpan(ctx, &pluginv1.StartSpanRequest{Session: token, Name: "think", Attributes: big})
	require.NoError(t, err)

	_, err = a.EndSpan(ctx, &pluginv1.EndSpanRequest{Session: token, Span: first.GetSpan(), Attributes: map[string]string{"z": strings.Repeat("v", maxText)}})
	require.NoError(t, err)

	require.Len(t, spans.Ended(), 1)

	size := 0
	for _, kv := range spans.Ended()[0].Attributes() {
		if kv.Key != "plugin.id" {
			size += len(kv.Key) + len(kv.Value.AsString())
		}
	}

	assert.LessOrEqual(t, size, maxLine, "its attributes, at start and end together (P11)")
	assert.Positive(t, size)

	for range maxSpans {
		_, err := a.StartSpan(ctx, &pluginv1.StartSpanRequest{Session: token, Name: "left open"})
		require.NoError(t, err)
	}

	_, err = a.StartSpan(ctx, &pluginv1.StartSpanRequest{Session: token, Name: "one more"})
	assert.Equal(t, codes.ResourceExhausted, status.Code(err), "so many going at once, and no more (P11)")

	_, err = a.EndSpan(ctx, &pluginv1.EndSpanRequest{Session: token, Span: first.GetSpan() + 1})
	require.NoError(t, err)

	_, err = a.StartSpan(ctx, &pluginv1.StartSpanRequest{Session: token, Name: "one more"})
	assert.NoError(t, err, "one ended, one may start")
}

func TestLogLineBound(t *testing.T) {
	ctx := context.Background()
	a, book, _, token, _ := opened(t, ctx)

	fields := map[string]string{}
	for i := range maxFields {
		fields[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", maxText)
	}

	_, err := a.Log(ctx, &pluginv1.LogRequest{Session: token, Level: "info", Message: strings.Repeat("m", 1<<20), Fields: fields})
	require.NoError(t, err)

	line := book.last()
	size := len(line.msg)

	group, ok := line.attrs["fields"].([]logging.Attr)
	require.True(t, ok, "what fits of its fields")
	assert.True(t, strings.HasSuffix(group[0].Value.(string), ellipsis), "the first cut to fit")

	for _, f := range group {
		size += len(f.Key) + len(f.Value.(string))
	}

	assert.LessOrEqual(t, size, maxLine, "its message and fields together, not 32 fields of 16 KiB (P4)")

	_, err = a.Log(ctx, &pluginv1.LogRequest{Session: token, Level: "info", Message: "short", Fields: map[string]string{"a": "1", "b": "2"}})
	require.NoError(t, err)
	assert.Equal(t, []logging.Attr{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}, book.last().attrs["fields"], "a line within it, whole")
}
