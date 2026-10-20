package trading

import (
	"context"
	"errors"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Batches (3s, D244 to D246): a workflow's orders placed together, for speed. Each is
// checked on its own, as Place checks one, the caps counting those before it; live, those
// that pass reach Polymarket together. Polymarket's batch is not atomic: each order
// succeeds or fails on its own, and so do they here.

var _ ports.BatchPlacer = (*Service)(nil)

var errOneWorkflow = errors.New("a batch is one workflow's orders")

// PlaceBatch places a workflow's orders together: live, those that pass their checks in
// requests of as many as Polymarket takes; on paper, one after another. How each went,
// in their order; its owner is told of each, as they chose (D229).
func (s *Service) PlaceBatch(ctx context.Context, reqs []domain.OrderRequest) ([]ports.PlaceResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	for _, r := range reqs {
		if r.WorkflowID != reqs[0].WorkflowID {
			return nil, errOneWorkflow
		}
	}

	out, err := s.placeBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	for _, p := range out {
		if p.Err == nil {
			s.tell(placed(p.Order, s.d.Now().UTC())...)
		}
	}

	return out, nil
}

func (s *Service) placeBatch(ctx context.Context, reqs []domain.OrderRequest) ([]ports.PlaceResult, error) {
	unlock := s.lock(reqs[0].WorkflowID)
	defer unlock()

	t, err := s.tradingOf(ctx, reqs[0].WorkflowID)
	if err != nil {
		return nil, err
	}

	out := make([]ports.PlaceResult, len(reqs))

	if t.Mode != domain.ModeLive {
		for i, req := range reqs {
			out[i].Order, out[i].Err = s.placeIn(ctx, req, t)
		}

		return out, nil
	}

	// Each checked in turn: the caps and the shares count the orders before it (D245).
	var (
		ready   []*liveReady
		at      []int // where each ready one is in out
		selling = map[domain.HoldingKey]decimal.Decimal{}
	)

	defer func() {
		for _, r := range ready {
			r.done(s)
		}
	}()

	for i, req := range reqs {
		o, rules, refused, err := s.prepare(ctx, req, t)
		if err != nil || refused {
			out[i] = ports.PlaceResult{Order: o, Err: err}
			continue
		}

		r, refusedLive, err := s.readyLive(ctx, o, rules, t, selling)
		if err != nil || r == nil {
			out[i] = ports.PlaceResult{Order: refusedLive, Err: err}
			continue
		}

		if o.Side == domain.SideSell {
			key := domain.HoldingKey{WorkflowID: o.WorkflowID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}
			selling[key] = selling[key].Add(o.Shares)
		}

		ready = append(ready, r)
		at = append(at, i)
	}

	if len(ready) == 0 {
		return out, nil
	}

	s.sendBatch(ctx, ready, at, out)

	return out, nil
}

// sendBatch sends the ready orders together, and records what each came to: answered at
// once, or waited for together (D204), then recorded one after another, each in its
// holding as the ones before it left it.
func (s *Service) sendBatch(ctx context.Context, ready []*liveReady, at []int, out []ports.PlaceResult) {
	orders := make([]ports.LiveOrder, len(ready))
	for i, r := range ready {
		orders[i] = s.liveOrder(r)
	}

	placements, err := s.d.Exchange.PlaceBatch(ctx, ready[0].k, orders, func(i int, id string) error {
		return s.recordFirst(ctx, ready[i], id)
	})
	if err != nil {
		for i, r := range ready {
			s.notSent(ctx, r, err)
			out[at[i]].Err = err
		}

		return
	}

	var waiting []int

	for i, r := range ready {
		o, done, err := s.answered(ctx, r, placements[i])
		if done || err != nil {
			out[at[i]] = ports.PlaceResult{Order: o, Err: err}
			continue
		}

		waiting = append(waiting, i)
	}

	settled := make([]ports.PlaceResult, len(ready))

	var wg sync.WaitGroup

	for _, i := range waiting {
		wg.Go(func() {
			r := ready[i]
			settled[i].Order, settled[i].Err = s.await(ctx, r.k, r.o, r.rules, placements[i])
		})
	}

	wg.Wait()

	for _, i := range waiting {
		if settled[i].Err != nil {
			out[at[i]].Err = settled[i].Err
			continue
		}

		out[at[i]].Order, out[at[i]].Err = s.finishLive(ctx, ready[i], settled[i].Order)
	}
}
