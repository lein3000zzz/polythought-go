package polymarket

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.ActivityStream = (*Activity)(nil)

// Activity translates the activity topic into domain events. Like Stream, it adds no
// buffer of its own.
type Activity struct {
	client pm.ActivityStreamClient
	events chan ports.ActivityEvent
}

func NewActivity(client pm.ActivityStreamClient) *Activity {
	return &Activity{client: client, events: make(chan ports.ActivityEvent)}
}

func (a *Activity) Events() <-chan ports.ActivityEvent {
	return a.events
}

// Run drives the client and forwards its rows until ctx is done. Rows left from an
// earlier run are dropped first: whoever runs it again has caught up some other way.
func (a *Activity) Run(ctx context.Context) error {
	for drained := false; !drained; {
		select {
		case <-a.client.Events():
		default:
			drained = true
		}
	}

	errc := make(chan error, 1)
	go func() { errc <- a.client.Run(ctx) }()

	for {
		select {
		case err := <-errc:
			return err
		case ev := <-a.client.Events():
			out, ok := toActivityEvent(ev)
			if !ok {
				continue
			}

			select {
			case a.events <- out:
			case <-ctx.Done():
			}
		}
	}
}

func toActivityEvent(ev pm.ActivityEvent) (ports.ActivityEvent, bool) {
	switch e := ev.(type) {
	case pm.ActivityConnected:
		return ports.ActivityEvent{Connected: true}, true

	case pm.ActivityTrade:
		wallet, err := domain.ParseAddress(e.ProxyWallet)
		if err != nil || e.TransactionHash == "" || e.Asset == "" {
			return ports.ActivityEvent{}, false
		}

		return ports.ActivityEvent{
			Taker: e.Type == pm.ActivityOrdersMatched,
			Fill: domain.Fill{
				Wallet:    wallet,
				TxHash:    e.TransactionHash,
				TokenID:   domain.TokenID(e.Asset),
				MarketID:  domain.MarketID(e.ConditionID),
				Outcome:   e.Outcome,
				Title:     e.Title,
				Slug:      e.Slug,
				EventSlug: e.EventSlug,
				Side:      toSide(e.Side),
				Price:     e.Price,
				Size:      e.Size,
				At:        time.Unix(e.Timestamp, 0).UTC(),
				Name:      traderName(e.Name, e.Pseudonym),
			},
		}, true
	}

	return ports.ActivityEvent{}, false
}
