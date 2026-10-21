// Package spend counts what agent nodes spend with their owners' own keys (D268 to
// D277): every call's tokens, and the dollars a provider reports when it does (D270), by
// user and UTC month; and holds them to caps, the month's and a node run's, with notices
// at 80% and at a cap.
package spend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.AgentMeter = (*Service)(nil)

const (
	// recordFor bounds recording a call: past the run's cancelling, but not for ever, as
	// the executor's own writes are; a stopped workflow waits for its nodes.
	recordFor = 10 * time.Second
	// defaultOutput is what a call may write when it does not say: the adapters' own.
	defaultOutput = 16000
	// bytesPerToken estimates a call's input from its size: few texts take fewer bytes a
	// token, so the estimate is high, which errs towards holding too much.
	bytesPerToken = 3
)

// Users are where a user's caps and choices are.
type Users interface {
	ByID(ctx context.Context, id domain.UserID) (domain.User, error)
	SetKillStopsAgents(ctx context.Context, id domain.UserID, on bool) error
}

type Deps struct {
	Usage   ports.AgentUsageRepository
	Users   Users
	Caps    func() domain.AgentCaps // the instance's (D271)
	Notices ports.SpendNotifier     // optional
	Logger  logging.Logger
	Now     func() time.Time // optional
}

type Service struct {
	usage   ports.AgentUsageRepository
	users   Users
	caps    func() domain.AgentCaps
	notices ports.SpendNotifier
	log     logging.Logger
	now     func() time.Time

	mu     sync.Mutex
	owners map[domain.UserID]*holding
}

// holding is what an owner's calls in flight hold, all their runs together, under its
// lock (D269). In memory: one server, as the live day cap.
type holding struct {
	mu            sync.Mutex
	input, output int64
}

func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}

	caps := d.Caps
	if caps == nil {
		caps = func() domain.AgentCaps { return domain.AgentCaps{} }
	}

	return &Service{
		usage: d.Usage, users: d.Users, caps: caps, notices: d.Notices,
		log: d.Logger.With("component", "spend"), now: now, owners: map[domain.UserID]*holding{},
	}
}

func (s *Service) holdingOf(owner domain.UserID) *holding {
	s.mu.Lock()
	defer s.mu.Unlock()

	h := s.owners[owner]
	if h == nil {
		h = &holding{}
		s.owners[owner] = h
	}

	return h
}

func (s *Service) Run(run ports.SpendRun) ports.SpendBudget {
	return &budget{s: s, run: run}
}

// Spend is a user's spend this month, and the caps on it.
type Spend struct {
	Usage  domain.AgentUsage
	Caps   domain.AgentCaps // those that apply
	Own    domain.AgentCaps // an admin's for them; nil where the instance's apply
	Resets time.Time        // when the month ends
	// KillStopsAgents is their choice that their kill switch stops their agents too.
	KillStopsAgents bool
}

// Spend is owner's spend this month and the caps that apply (D277).
func (s *Service) Spend(ctx context.Context, owner domain.UserID) (Spend, error) {
	u, err := s.users.ByID(ctx, owner)
	if err != nil {
		return Spend{}, err
	}

	month := domain.MonthOf(s.now())

	usage, err := s.usage.Month(ctx, owner, month)
	if err != nil {
		return Spend{}, err
	}

	return Spend{
		Usage: usage, Caps: u.AgentCaps.Over(s.caps()), Own: u.AgentCaps, Resets: month.AddDate(0, 1, 0),
		KillStopsAgents: u.KillStopsAgents,
	}, nil
}

// SetKillStopsAgents records owner's choice that their kill switch stops their agents
// too (D276).
func (s *Service) SetKillStopsAgents(ctx context.Context, owner domain.UserID, on bool) error {
	return s.users.SetKillStopsAgents(ctx, owner, on)
}

// tally is spend: tokens in and out, and dollars.
type tally struct {
	input, output int64
	dollars       decimal.Decimal
}

func (t tally) plus(o tally) tally {
	return tally{input: t.input + o.input, output: t.output + o.output, dollars: t.dollars.Add(o.dollars)}
}

// budget is one node run's: what its calls recorded and hold, under its lock, its calls
// a few at once.
type budget struct {
	s   *Service
	run ports.SpendRun

	mu   sync.Mutex
	used tally
	held tally
}

// Admit lets a call go if it fits every cap, holding its estimate until it is recorded:
// the run's own first, then the month's (D269, D271, D272). A token cap counts what is
// recorded, held and this call's estimate; a dollar cap what is recorded, as a call's
// dollars are known only once it ends (D270).
func (b *budget) Admit(ctx context.Context, req ports.LLMRequest) (ports.SpendHold, string, error) {
	s, owner := b.s, b.run.OwnerID

	user, err := s.users.ByID(ctx, owner)
	if err != nil {
		return nil, "", err
	}

	if user.KillStopsAgents && user.LiveStopped() {
		return nil, "your kill switch is on, and you chose that it stops your agents too", nil
	}

	call := estimate(req)

	b.mu.Lock()
	defer b.mu.Unlock()

	if r := check(b.run.Caps, b.used.plus(b.held), call, perRun); r.why != "" {
		return nil, r.why, nil
	}

	h := s.holdingOf(owner)

	h.mu.Lock()
	defer h.mu.Unlock()

	month := domain.MonthOf(s.now())

	stored, err := s.usage.Month(ctx, owner, month)
	if err != nil {
		return nil, "", err
	}

	caps := user.AgentCaps.Over(s.caps())
	so := tally{input: stored.InputTokens + h.input, output: stored.OutputTokens + h.output, dollars: stored.Dollars}

	if r := check(caps, so, call, perMonth); r.why != "" {
		s.notice(ctx, owner, month, domain.NoticeCapped, stored.Capped, caps, r)
		return nil, r.why, nil
	}

	h.input, h.output = h.input+call.input, h.output+call.output
	b.held = b.held.plus(call)

	return &hold{b: b, h: h, month: month, call: call, caps: caps}, "", nil
}

// estimate is the most a call may take: its input from its size, and its Max tokens.
func estimate(req ports.LLMRequest) tally {
	n := len(req.System) + len(req.Schema)

	for _, m := range req.Messages {
		n += len(m.Text)

		for _, c := range m.ToolCalls {
			n += len(c.Name) + len(c.Input)
		}

		for _, r := range m.ToolResults {
			n += len(r.Content)
		}
	}

	for _, t := range req.Tools {
		n += len(t.Name) + len(t.Description) + len(t.Schema)
	}

	out := req.MaxTokens
	if out <= 0 {
		out = defaultOutput
	}

	return tally{input: int64(n)/bytesPerToken + 1, output: out}
}

// scope is where a cap applies, in words.
type scope struct {
	tokens  string // "a month your agents may use"
	dollars string
}

var (
	perMonth = scope{tokens: "a month your agents may use", dollars: "your agents' cap this month"}
	perRun   = scope{tokens: "this node may use in one run", dollars: "this node's cap for one run"}
)

// refusal is why a call may not go, and which cap says so; empty when it may.
type refusal struct {
	why, what, cap, used string
}

// check says whether a call fits caps, beside what is spent or held so far.
func check(caps domain.AgentCaps, so, call tally, sc scope) refusal {
	if d := caps.Dollars; d != nil && d.IsPositive() && so.dollars.GreaterThanOrEqual(*d) {
		used, cap := "$"+so.dollars.StringFixed(2), "$"+d.StringFixed(2)
		return refusal{why: fmt.Sprintf("%s spent, %s", used, sc.dollars), what: "dollars", cap: cap, used: used}
	}

	for _, c := range []struct {
		cap        *int64
		what       string
		so, adding int64
	}{
		{caps.TotalTokens, "tokens", so.input + so.output, call.input + call.output},
		{caps.InputTokens, "input tokens", so.input, call.input},
		{caps.OutputTokens, "output tokens", so.output, call.output},
	} {
		if c.cap == nil || *c.cap <= 0 || c.so+c.adding <= *c.cap {
			continue
		}

		return refusal{
			why: fmt.Sprintf("this call could take up to %s %s, past the %s %s: %s used or held", thousands(c.adding), c.what,
				thousands(*c.cap), sc.tokens, thousands(c.so)),
			what: c.what, cap: thousands(*c.cap), used: thousands(c.so),
		}
	}

	return refusal{}
}

// hold is a call's, its month the one it was let go in: one let go at 23:59 on the last
// day counts in that month.
type hold struct {
	b     *budget
	h     *holding
	month time.Time
	call  tally
	caps  domain.AgentCaps // the month's, as it was let go
	once  sync.Once
}

func (h *hold) Record(ctx context.Context, usage ports.LLMUsage) {
	h.once.Do(func() {
		s, run := h.b.s, h.b.run

		delta := domain.AgentUsage{
			Month: h.month, InputTokens: max(usage.InputTokens, 0), OutputTokens: max(usage.OutputTokens, 0),
			Dollars: decimal.Zero, Calls: 1,
		}

		// A free model's 0 is a cost reported too.
		if usage.Cost != nil && !usage.Cost.IsNegative() {
			delta.Dollars, delta.CostedCalls = *usage.Cost, 1
		}

		// The call is made, and billed: a run cancelled since still counts it. The books
		// first, then the hold goes, so the call is never counted twice nor missed.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordFor)
		defer cancel()

		totals, err := s.usage.Add(ctx, run.OwnerID, delta, s.now())
		h.release(tally{input: delta.InputTokens, output: delta.OutputTokens, dollars: delta.Dollars})

		if err != nil {
			s.log.Error(ctx, "Agent spend not recorded", "user_id", run.OwnerID, "workflow_id", run.WorkflowID,
				"node_id", run.NodeID, "input_tokens", delta.InputTokens, "output_tokens", delta.OutputTokens, logging.Err(err))

			return
		}

		s.warn(ctx, run.OwnerID, totals, h.caps)
	})
}

func (h *hold) Release() {
	h.once.Do(func() { h.release(tally{dollars: decimal.Zero}) })
}

// release lets the hold go, used being what the call took.
func (h *hold) release(used tally) {
	b := h.b

	b.mu.Lock()
	defer b.mu.Unlock()

	b.held = tally{input: b.held.input - h.call.input, output: b.held.output - h.call.output, dollars: b.held.dollars}
	b.used = b.used.plus(used)

	h.h.mu.Lock()
	defer h.h.mu.Unlock()

	h.h.input, h.h.output = h.h.input-h.call.input, h.h.output-h.call.output
}

// warn tells the owner once a month their agents passed 80% of a cap, the first that
// did, and again only for a cap raised since (D274).
func (s *Service) warn(ctx context.Context, owner domain.UserID, totals domain.AgentUsage, caps domain.AgentCaps) {
	so := tally{input: totals.InputTokens, output: totals.OutputTokens, dollars: totals.Dollars}
	eighty := decimal.NewFromFloat(0.8)

	var r refusal

	if d := caps.Dollars; d != nil && d.IsPositive() && so.dollars.GreaterThanOrEqual(d.Mul(eighty)) {
		r = refusal{what: "dollars", cap: "$" + d.StringFixed(2), used: "$" + so.dollars.StringFixed(2)}
	}

	for _, c := range []struct {
		cap  *int64
		what string
		so   int64
	}{
		{caps.TotalTokens, "tokens", so.input + so.output},
		{caps.InputTokens, "input tokens", so.input},
		{caps.OutputTokens, "output tokens", so.output},
	} {
		if r.what == "" && c.cap != nil && *c.cap > 0 && c.so*5 >= *c.cap*4 {
			r = refusal{what: c.what, cap: thousands(*c.cap), used: thousands(c.so)}
		}
	}

	if r.what != "" {
		s.notice(ctx, owner, totals.Month, domain.NoticeWarned, totals.Warned, caps, r)
	}
}

// notice claims the month's notice, unless it went at caps no lower, and tells the owner.
func (s *Service) notice(ctx context.Context, owner domain.UserID, month time.Time, n domain.SpendNotice, was string,
	caps domain.AgentCaps, r refusal,
) {
	sig := signature(caps)
	if was != "" && !raised(was, sig) {
		return
	}

	claimed, err := s.usage.ClaimNotice(ctx, owner, month, n, was, sig, s.now())
	if err != nil {
		s.log.Warn(ctx, "Agent spend notice not claimed", "user_id", owner, "notice", n, logging.Err(err))
		return
	}

	if claimed && s.notices != nil {
		s.notices.AgentSpendNoticed(owner, domain.SpendAlert{Notice: n, What: r.what, Cap: r.cap, Used: r.used, Resets: month.AddDate(0, 1, 0)})
	}
}

// signature is caps as a notice keeps them: each in turn, 0 for none.
func signature(c domain.AgentCaps) string {
	n := func(p *int64) int64 {
		if p == nil {
			return 0
		}

		return max(*p, 0)
	}

	d := decimal.Zero
	if c.Dollars != nil && c.Dollars.IsPositive() {
		d = *c.Dollars
	}

	return fmt.Sprintf("dollars=%s input=%d output=%d total=%d", d.StringFixed(2), n(c.InputTokens), n(c.OutputTokens), n(c.TotalTokens))
}

// raised reports whether any cap of now is higher than it was in was, none being the
// highest: a notice sent at was may go again.
func raised(was, now string) bool {
	before, after := parseSignature(was), parseSignature(now)

	for k, a := range after {
		b, ok := before[k]
		if !ok || (a.IsZero() && !b.IsZero()) || (!b.IsZero() && a.GreaterThan(b)) {
			return true
		}
	}

	return false
}

func parseSignature(sig string) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{}

	for part := range strings.FieldsSeq(sig) {
		k, v, ok := strings.Cut(part, "=")
		if d, err := decimal.NewFromString(v); ok && err == nil {
			out[k] = d
		}
	}

	return out
}

// thousands writes n for people: 20,000,000.
func thousands(n int64) string {
	if n < 0 {
		return "-" + thousands(-n)
	}

	s := fmt.Sprintf("%d", n)

	var b strings.Builder

	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}

		b.WriteRune(c)
	}

	return b.String()
}
