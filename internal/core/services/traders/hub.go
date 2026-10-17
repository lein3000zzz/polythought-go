// Package traders follows traders' trades for every workflow that copies them (D87): one
// activity stream for the whole process, open while anyone follows a trader, filtered by
// wallet here, with the Data API filling what the stream missed.
package traders

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.TraderFeed = (*Hub)(nil)

var ErrNoWallets = errors.New("follow at least one wallet")

// Config tunes the hub; zero values take the defaults.
type Config struct {
	Settle   time.Duration // 0 => 1.5s: a trade's fills are gathered this long after its first
	Poll     time.Duration // 0 => 30s: how often the Data API is asked while the stream is up
	PollDown time.Duration // 0 => 5s: and while it is down
	Quiet    time.Duration // 0 => 20s: the stream counts as down after this long without a row
	Linger   time.Duration // 0 => 1m: the stream stays open this long after the last follower
	Remember time.Duration // 0 => 1h: a trade delivered is not delivered again for this long
	LookBack time.Duration // 0 => 1h: the furthest back a catch-up looks
	Buffer   int           // 0 => 64: batches a subscription holds before missing some
}

func (c Config) withDefaults() Config {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}

	def(&c.Settle, 1500*time.Millisecond)
	def(&c.Poll, 30*time.Second)
	def(&c.PollDown, 5*time.Second)
	def(&c.Quiet, 20*time.Second)
	def(&c.Linger, time.Minute)
	def(&c.Remember, time.Hour)
	def(&c.LookBack, time.Hour)

	if c.Buffer <= 0 {
		c.Buffer = 64
	}

	return c
}

// requestsPerSecond is the pace polls keep to, under the Data API's limit of 300 trade
// requests per 10 seconds: N followed wallets are polled no more often than N/20 s.
const requestsPerSecond = 20

// historyPages is the most pages of a wallet's trades one poll reads.
const historyPages = 3

// key names a trade: one wallet's fills in one transaction, outcome and side (D84).
type key struct {
	wallet string // lower case
	tx     string
	token  domain.TokenID
	side   domain.Side
}

type pending struct {
	first time.Time
	fills []domain.Fill
}

// Hub fans one activity stream out to the workflows following its wallets. Everything
// but the subscriptions belongs to Run's goroutine.
type Hub struct {
	stream  ports.ActivityStream
	history ports.TradeReader
	markets ports.MarketReader
	cfg     Config
	log     logging.Logger
	now     func() time.Time
	dropped metrics.Counter
	caught  metrics.Counter

	mu        sync.Mutex
	subs      map[*subscription]struct{}
	following map[string]int // wallets, lower case, by how many subscriptions follow them
	catchUps  chan catchUp   // looks back a new subscription asked for
	stopping  bool

	// Run's own.
	pending  map[key]*pending
	takers   map[string]takerMark // a transaction's taking wallet, lower case
	seen     map[key]time.Time    // delivered, and when
	lastRow  time.Time
	lastPoll time.Time
	polling  bool
	polled   chan []domain.Fill
	streamer *streamer
	idleFrom time.Time // when the last follower left

	tokenMu sync.Mutex
	tokens  map[domain.TokenID]domain.Market // markets of tokens fills left bare
}

type takerMark struct {
	wallet string
	at     time.Time
}

type catchUp struct {
	wallets []domain.WalletAddress
	since   time.Time
}

// streamer is the stream while it runs.
type streamer struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Option func(*Hub)

// WithClock sets the hub's clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(h *Hub) { h.now = now }
}

func NewHub(util *observability.Utility, stream ports.ActivityStream, history ports.TradeReader,
	markets ports.MarketReader, cfg Config, opts ...Option,
) (*Hub, error) {
	counter := func(name, help string) (metrics.Counter, error) {
		return util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
			Definition: metrics.Definition{Namespace: metrics.DefaultNamespace, Subsystem: "traders", Name: name, Help: help},
		})
	}

	dropped, err := counter("batches_dropped_total", "Batches of trades a slow follower missed.")
	if err != nil {
		return nil, fmt.Errorf("traders: register metrics: %w", err)
	}

	caught, err := counter("trades_caught_up_total", "Trades the stream missed and the Data API found.")
	if err != nil {
		return nil, fmt.Errorf("traders: register metrics: %w", err)
	}

	h := &Hub{
		stream: stream, history: history, markets: markets, cfg: cfg.withDefaults(),
		log: util.Logger.With("component", "traders"), now: time.Now, dropped: dropped, caught: caught,
		subs:      make(map[*subscription]struct{}),
		following: make(map[string]int),
		catchUps:  make(chan catchUp, 16),
		pending:   make(map[key]*pending),
		takers:    make(map[string]takerMark),
		seen:      make(map[key]time.Time),
		polled:    make(chan []domain.Fill, 4),
		tokens:    make(map[domain.TokenID]domain.Market),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h, nil
}

// Follow starts delivering the wallets' trades; with since set, it first looks back to it,
// no further than LookBack, for trades not delivered yet.
func (h *Hub) Follow(ctx context.Context, wallets []domain.WalletAddress, since time.Time) (ports.TraderSubscription, error) {
	if len(wallets) == 0 {
		return nil, ErrNoWallets
	}

	s := &subscription{hub: h, trades: make(chan []domain.TraderTrade, h.cfg.Buffer), wallets: map[string]bool{}}
	for _, w := range wallets {
		s.wallets[lower(w)] = true
	}

	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return nil, errors.New("traders: the hub is stopping")
	}

	h.subs[s] = struct{}{}
	for w := range s.wallets {
		h.following[w]++
	}
	h.mu.Unlock()

	if !since.IsZero() {
		select {
		case h.catchUps <- catchUp{wallets: wallets, since: since}:
		default:
			h.log.Warn(ctx, "Too many catch-ups at once: one skipped", "wallets", len(wallets))
		}
	}

	go func() {
		<-ctx.Done()
		s.Close()
	}()

	return s, nil
}

// Run reads the stream and delivers settled trades until ctx is done, then ends every
// subscription.
func (h *Hub) Run(ctx context.Context) error {
	tick := time.NewTicker(h.cfg.Settle / 4)
	defer tick.Stop()

	defer h.stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev := <-h.stream.Events():
			h.onEvent(ctx, ev)

		case fills := <-h.polled:
			h.polling = false
			h.deliverPolled(ctx, fills)

		case c := <-h.catchUps:
			h.poll(ctx, c.wallets, c.since)

		case <-tick.C:
			now := h.now()
			h.settle(ctx, now)
			h.keepStream(ctx, now)
			h.maybePoll(ctx, now)
			h.forget(now)
		}
	}
}

func (h *Hub) onEvent(ctx context.Context, ev ports.ActivityEvent) {
	now := h.now()

	if ev.Connected {
		// Rows from before the stream went down were seen; what came after is looked for.
		if !h.lastRow.IsZero() {
			h.poll(ctx, h.followed(), h.lastRow.Add(-10*time.Second))
		}

		return
	}

	h.lastRow = now

	w := lower(ev.Fill.Wallet)
	if !h.isFollowed(w) {
		return
	}

	if ev.Taker {
		h.takers[ev.Fill.TxHash] = takerMark{wallet: w, at: now}
		return
	}

	k := keyOf(ev.Fill)
	p := h.pending[k]

	if p == nil {
		p = &pending{first: now}
		h.pending[k] = p
	}

	p.fills = append(p.fills, ev.Fill)
}

// settle turns fills gathered long enough into trades, and delivers them.
func (h *Hub) settle(ctx context.Context, now time.Time) {
	var trades []domain.TraderTrade

	for _, k := range slices.Collect(maps.Keys(h.pending)) {
		p := h.pending[k]
		if now.Sub(p.first) < h.cfg.Settle {
			continue
		}

		delete(h.pending, k)

		if _, done := h.seen[k]; done {
			continue
		}

		h.seen[k] = now

		role := domain.RoleMaker
		if t, ok := h.takers[k.tx]; ok && t.wallet == k.wallet {
			role = domain.RoleTaker
		}

		trades = append(trades, tradeOf(p.fills, role))
	}

	h.publish(ctx, trades)
}

// deliverPolled delivers what the Data API found that was not delivered yet. A trade the
// stream is still gathering is left to it, as it says who took.
func (h *Hub) deliverPolled(ctx context.Context, fills []domain.Fill) {
	groups := map[key][]domain.Fill{}
	for _, f := range fills {
		k := keyOf(f)
		groups[k] = append(groups[k], f)
	}

	now := h.now()

	var trades []domain.TraderTrade

	for k, g := range groups {
		_, delivered := h.seen[k]
		_, gathering := h.pending[k]

		if delivered || gathering || !h.isFollowed(k.wallet) {
			continue
		}

		h.seen[k] = now
		trades = append(trades, tradeOf(g, domain.RoleUnknown))
	}

	if len(trades) > 0 {
		if err := h.caught.Add(float64(len(trades)), nil); err != nil {
			h.log.Warn(ctx, "Failed to increment metric", logging.Err(err))
		}

		h.log.Info(ctx, "Trades caught up from the Data API", "trades", len(trades))
	}

	h.publish(ctx, trades)
}

// publish names the markets of trades whose fills left them bare, then hands each
// subscription the trades of its wallets. It does not hold up Run.
func (h *Hub) publish(ctx context.Context, trades []domain.TraderTrade) {
	if len(trades) == 0 {
		return
	}

	slices.SortFunc(trades, func(a, b domain.TraderTrade) int { return a.At.Compare(b.At) })

	go func() {
		h.name(ctx, trades)

		h.mu.Lock()
		defer h.mu.Unlock()

		for s := range h.subs {
			var mine []domain.TraderTrade

			for _, t := range trades {
				if s.wallets[lower(t.Wallet)] {
					mine = append(mine, t)
				}
			}

			if len(mine) == 0 {
				continue
			}

			select {
			case s.trades <- mine:
			default:
				if err := h.dropped.Inc(nil); err != nil {
					h.log.Warn(ctx, "Failed to increment metric", logging.Err(err))
				}
			}
		}
	}()
}

// name fills in the market of trades that came without one, from their token.
func (h *Hub) name(ctx context.Context, trades []domain.TraderTrade) {
	var missing []domain.TokenID

	h.tokenMu.Lock()
	for _, t := range trades {
		if _, known := h.tokens[t.TokenID]; t.MarketID == "" && !known && !slices.Contains(missing, t.TokenID) {
			missing = append(missing, t.TokenID)
		}
	}
	h.tokenMu.Unlock()

	if len(missing) > 0 {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		page, err := h.markets.ListMarkets(ctx, ports.MarketFilter{TokenIDs: missing, Limit: len(missing)})
		if err != nil {
			h.log.Warn(ctx, "Markets of traded tokens not found", "tokens", len(missing), logging.Err(err))
		}

		h.tokenMu.Lock()
		for _, m := range page.Items {
			for _, o := range m.Outcomes {
				h.tokens[o.TokenID] = m
			}
		}
		h.tokenMu.Unlock()
	}

	h.tokenMu.Lock()
	defer h.tokenMu.Unlock()

	for i, t := range trades {
		m, ok := h.tokens[t.TokenID]
		if t.MarketID != "" || !ok {
			continue
		}

		trades[i].MarketID, trades[i].Title, trades[i].Slug = m.ID, m.Question, m.Slug
		if o, ok := m.Outcome(t.TokenID); ok {
			trades[i].Outcome = o.Label
		}
	}
}

// keepStream opens the stream while anyone follows a trader, and closes it a while after
// the last one leaves: a workflow saved again follows on without a reconnect.
func (h *Hub) keepStream(ctx context.Context, now time.Time) {
	followed := len(h.followed()) > 0

	if h.streamer != nil {
		select {
		case <-h.streamer.done: // it stopped by itself: opened again below if still wanted
			h.streamer = nil
		default:
		}
	}

	switch {
	case followed && h.streamer == nil:
		sctx, cancel := context.WithCancel(ctx)
		s := &streamer{cancel: cancel, done: make(chan struct{})}
		h.streamer = s
		h.idleFrom = time.Time{}

		go func() {
			defer close(s.done)

			if err := h.stream.Run(sctx); err != nil {
				h.log.Error(sctx, "Activity stream stopped", logging.Err(err))
			}
		}()

		h.log.Info(ctx, "Following traders: activity stream opened")

	case followed:
		h.idleFrom = time.Time{}

	case h.streamer != nil && h.idleFrom.IsZero():
		h.idleFrom = now

	case h.streamer != nil && now.Sub(h.idleFrom) >= h.cfg.Linger:
		h.closeStream()
		h.lastRow = time.Time{}
		h.log.Info(ctx, "No traders followed: activity stream closed")
	}
}

func (h *Hub) closeStream() {
	if h.streamer == nil {
		return
	}

	h.streamer.cancel()

	// Keep reading so the stream is never stuck handing over a row while it stops.
	for {
		select {
		case <-h.streamer.done:
			h.streamer = nil
			return
		case <-h.stream.Events():
		}
	}
}

// maybePoll asks the Data API for the followed wallets' recent trades, more often while
// the stream is down, never faster than its limit allows.
func (h *Hub) maybePoll(ctx context.Context, now time.Time) {
	wallets := h.followed()
	if len(wallets) == 0 || h.polling {
		return
	}

	every := h.cfg.Poll
	if h.lastRow.IsZero() || now.Sub(h.lastRow) > h.cfg.Quiet {
		every = h.cfg.PollDown
	}

	every = max(every, time.Duration(len(wallets))*time.Second/requestsPerSecond)

	if now.Sub(h.lastPoll) < every {
		return
	}

	// Long enough back to cover the time since the last poll, and the Data API's lag.
	h.poll(ctx, wallets, now.Add(-every-30*time.Second))
}

// poll reads the wallets' trades since then in the background, for Run to deliver.
func (h *Hub) poll(ctx context.Context, wallets []domain.WalletAddress, since time.Time) {
	now := h.now()
	since = maxTime(since, now.Add(-h.cfg.LookBack))
	h.lastPoll = now
	h.polling = true

	go func() {
		var fills []domain.Fill

		for _, w := range wallets {
			found, err := h.walletFills(ctx, w, since)
			if err != nil {
				if ctx.Err() == nil {
					h.log.Warn(ctx, "Trader's trades not read", "wallet", w, logging.Err(err))
				}

				continue
			}

			fills = append(fills, found...)
		}

		select {
		case h.polled <- fills:
		case <-ctx.Done():
		}
	}()
}

func (h *Hub) walletFills(ctx context.Context, w domain.WalletAddress, since time.Time) ([]domain.Fill, error) {
	var (
		fills  []domain.Fill
		cursor string
	)

	for range historyPages {
		page, err := h.history.Trades(ctx, ports.TradeFilter{Wallet: w, Since: since, Makers: true, Limit: 500, Cursor: cursor})
		if err != nil {
			return fills, err
		}

		for _, t := range page.Items {
			wallet, err := domain.ParseAddress(string(t.Wallet))
			if err != nil {
				continue
			}

			fills = append(fills, domain.Fill{
				Wallet: wallet, TxHash: t.TxHash, TokenID: t.TokenID, MarketID: t.MarketID, Outcome: t.Outcome,
				Title: t.Title, Slug: t.Slug, EventSlug: t.EventSlug, Side: t.Side, Price: t.Price, Size: t.Size,
				At: t.At, Name: t.Name,
			})
		}

		if page.NextCursor == "" {
			break
		}

		cursor = page.NextCursor
	}

	return fills, nil
}

// forget drops what is too old to matter: delivered trades, takers, gatherings that
// never settled.
func (h *Hub) forget(now time.Time) {
	maps.DeleteFunc(h.seen, func(_ key, at time.Time) bool { return now.Sub(at) > h.cfg.Remember })
	maps.DeleteFunc(h.takers, func(_ string, t takerMark) bool { return now.Sub(t.at) > time.Minute })
}

func (h *Hub) stop() {
	h.closeStream()

	h.mu.Lock()
	h.stopping = true
	all := slices.Collect(maps.Keys(h.subs))
	h.mu.Unlock()

	for _, s := range all {
		s.Close()
	}
}

func (h *Hub) followed() []domain.WalletAddress {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]domain.WalletAddress, 0, len(h.following))
	for w := range h.following {
		out = append(out, domain.WalletAddress(w))
	}

	slices.Sort(out)

	return out
}

func (h *Hub) isFollowed(wallet string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.following[wallet] > 0
}

func (h *Hub) unfollow(s *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.subs[s]; !ok {
		return
	}

	delete(h.subs, s)

	for w := range s.wallets {
		if h.following[w]--; h.following[w] <= 0 {
			delete(h.following, w)
		}
	}

	close(s.trades)
}

type subscription struct {
	hub     *Hub
	wallets map[string]bool
	trades  chan []domain.TraderTrade
	once    sync.Once
}

func (s *subscription) Trades() <-chan []domain.TraderTrade {
	return s.trades
}

func (s *subscription) Close() {
	s.once.Do(func() { s.hub.unfollow(s) })
}

// tradeOf sums one trade's fills (D84).
func tradeOf(fills []domain.Fill, role domain.TradeRole) domain.TraderTrade {
	first := fills[0]
	t := domain.TraderTrade{
		Wallet: first.Wallet, TxHash: first.TxHash, TokenID: first.TokenID, MarketID: first.MarketID,
		Outcome: first.Outcome, Title: first.Title, Slug: first.Slug, EventSlug: first.EventSlug,
		Side: first.Side, Name: first.Name, At: first.At, Fills: len(fills), Role: role,
	}

	for _, f := range fills {
		t.Shares = t.Shares.Add(f.Size)
		t.Dollars = t.Dollars.Add(f.Size.Mul(f.Price))

		if f.At.Before(t.At) {
			t.At = f.At
		}

		if t.MarketID == "" && f.MarketID != "" {
			t.MarketID, t.Outcome, t.Title, t.Slug, t.EventSlug = f.MarketID, f.Outcome, f.Title, f.Slug, f.EventSlug
		}
	}

	if t.Shares.IsPositive() {
		t.Price = t.Dollars.DivRound(t.Shares, 6)
	}

	t.Dollars = t.Dollars.Round(6)

	return t
}

func keyOf(f domain.Fill) key {
	return key{wallet: lower(f.Wallet), tx: strings.ToLower(f.TxHash), token: f.TokenID, side: f.Side}
}

func lower(w domain.WalletAddress) string {
	return strings.ToLower(string(w))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}

	return b
}
