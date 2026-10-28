package stats

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// The admin's view (D431): how every user's workflows are doing, and one user's as their
// dashboard has it.

// Users are the instance's users: the user repository.
type Users interface {
	List(ctx context.Context, f ports.PageFilter) (ports.Page[domain.User], error)
	ByID(ctx context.Context, id domain.UserID) (domain.User, error)
}

// UserTotals is how one user's workflows are doing together.
type UserTotals struct {
	User      domain.User
	Workflows int // all they have
	Active    int
	Paper     Totals
	Live      Totals
	Day, Week domain.RunCounts
}

// Instance is how every user's workflows are doing.
type Instance struct {
	Users []UserTotals // those with workflows, as the users are listed
	Paper Totals
	Live  Totals
	// Day and Week are all the runs, started in the last day and the last week.
	Day, Week domain.RunCounts
	At        time.Time
}

// summed is how many users are summed at once.
const summed = 4

// Instance is how every user's workflows are doing, for an admin.
func (s *Service) Instance(ctx context.Context, p auth.Principal) (Instance, error) {
	if !p.User.IsAdmin() {
		return Instance{}, domain.ErrForbidden
	}

	if s.d.Users == nil {
		return Instance{}, domain.ErrNotFound
	}

	var users []domain.User

	for f := (ports.PageFilter{Limit: 100}); ; {
		page, err := s.d.Users.List(ctx, f)
		if err != nil {
			return Instance{}, err
		}

		users = append(users, page.Items...)

		if page.NextCursor == "" {
			break
		}

		f.Cursor = page.NextCursor
	}

	each := make([]UserTotals, len(users))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(summed)

	for i, u := range users {
		g.Go(func() error {
			o, err := s.overview(gctx, u.ID)
			if err != nil {
				return err
			}

			each[i] = totalsOf(u, o)

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return Instance{}, err
	}

	out := Instance{Users: []UserTotals{}, At: s.d.Now()}

	for _, t := range each {
		if t.Workflows == 0 {
			continue
		}

		out.Users = append(out.Users, t)
		sum(&out.Paper, t.Paper)
		sum(&out.Live, t.Live)
		out.Day.Runs, out.Day.Failed = out.Day.Runs+t.Day.Runs, out.Day.Failed+t.Day.Failed
		out.Week.Runs, out.Week.Failed = out.Week.Runs+t.Week.Runs, out.Week.Failed+t.Week.Failed
	}

	return out, nil
}

// OverviewOf is how one user's workflows are doing, as their dashboard has it, for an
// admin.
func (s *Service) OverviewOf(ctx context.Context, p auth.Principal, id domain.UserID) (domain.User, Overview, error) {
	if !p.User.IsAdmin() {
		return domain.User{}, Overview{}, domain.ErrForbidden
	}

	if s.d.Users == nil {
		return domain.User{}, Overview{}, domain.ErrNotFound
	}

	u, err := s.d.Users.ByID(ctx, id)
	if err != nil {
		return domain.User{}, Overview{}, err
	}

	o, err := s.overview(ctx, u.ID)
	if err != nil {
		return domain.User{}, Overview{}, err
	}

	return u, o, nil
}

func totalsOf(u domain.User, o Overview) UserTotals {
	t := UserTotals{User: u, Workflows: len(o.Workflows), Paper: o.Paper, Live: o.Live, Day: o.Day, Week: o.Week}

	for _, w := range o.Workflows {
		if w.Active {
			t.Active++
		}
	}

	return t
}

func sum(t *Totals, more Totals) {
	t.Workflows += more.Workflows
	t.Profit, t.Realized, t.Unrealized = t.Profit.Add(more.Profit), t.Realized.Add(more.Realized), t.Unrealized.Add(more.Unrealized)
	t.Volume = t.Volume.Add(more.Volume)
	t.Positions += more.Positions
	t.Unpriced += more.Unpriced
	t.Orders += more.Orders
	t.Wins += more.Wins
	t.Closed += more.Closed
}
