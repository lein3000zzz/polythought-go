package auth

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// An admin's agent's or token's work on another user waits for the admin where they say so
// (D410): role changes, disabling or enabling, raising caps, letting them write code.

// Confirmer holds an action for its user to confirm: the confirmations service.
type Confirmer interface {
	Hold(ctx context.Context, p Principal, a domain.Ask) error
}

// WithConfirmations has an admin's agents' and tokens' work on users wait for the admin.
func WithConfirmations(c Confirmer) Option {
	return func(s *Service) { s.confirm = c }
}

// WithInstanceCaps tells raising a user's caps from lowering them, against the instance's.
func WithInstanceCaps(f func() (domain.LiveCaps, domain.AgentCaps)) Option {
	return func(s *Service) { s.instanceCaps = f }
}

// UserAsked is what a held change to a user carries out.
type UserAsked struct {
	User   domain.UserID `json:"user"`
	Update UserUpdate    `json:"update"`
}

// holdUserUpdate is nil when the actor's change to u goes ahead, and holds it for the
// admin when a part of it waits for them, the whole change together.
func (s *Service) holdUserUpdate(ctx context.Context, actor Principal, u domain.User, upd UserUpdate) error {
	if s.confirm == nil || actor.Itself() {
		return nil
	}

	type part struct {
		kind domain.ConfirmKind
		says string
	}

	var parts []part

	if upd.Role != nil && *upd.Role != u.Role {
		says := "make them an admin"
		if *upd.Role != domain.RoleAdmin {
			says = "make them no longer an admin"
		}

		parts = append(parts, part{domain.ConfirmRole, says})
	}

	if upd.Disabled != nil && *upd.Disabled != u.Disabled() {
		says := "disable them"
		if !*upd.Disabled {
			says = "enable them again"
		}

		parts = append(parts, part{domain.ConfirmDisable, says})
	}

	if s.raisesCaps(u, upd) {
		parts = append(parts, part{domain.ConfirmCaps, "raise or remove their own caps"})
	}

	if upd.CodeChosen != nil && *upd.CodeChosen && !u.CodeChosen {
		parts = append(parts, part{domain.ConfirmCode, "let them write code"})
	}

	var (
		kind domain.ConfirmKind
		says []string
	)

	for _, p := range parts {
		says = append(says, p.says)

		if kind == "" && actor.User.Unconfirmed.Waits(p.kind) {
			kind = p.kind
		}
	}

	if kind == "" {
		return nil
	}

	// And what else the change does, which alone would not wait, as the admin confirms all
	// of it together (A19).
	if upd.EmailVerified != nil && *upd.EmailVerified && u.EmailVerifiedAt.IsZero() {
		says = append(says, "mark their address verified")
	}

	if upd.CodeChosen != nil && !*upd.CodeChosen && u.CodeChosen {
		says = append(says, "no longer let them write code")
	}

	if !s.raisesCaps(u, upd) && ((upd.LiveCaps != nil && !same(*upd.LiveCaps, u.LiveCaps)) ||
		(upd.AgentCaps != nil && !same(*upd.AgentCaps, u.AgentCaps))) {
		says = append(says, "lower their own caps")
	}

	// The agent named them by ID: their email is the admin's to see (D410).
	return s.confirm.Hold(ctx, actor, domain.Ask{
		Kind: kind, What: "Change the user " + string(u.ID) + ": " + strings.Join(says, ", "), About: u.Email,
		Payload: UserAsked{User: u.ID, Update: upd},
	})
}

// same says two caps are the same, by value: their pointers are not.
func same(a, b any) bool {
	x, xerr := json.Marshal(a)
	y, yerr := json.Marshal(b)

	return xerr == nil && yerr == nil && string(x) == string(y)
}

// raisesCaps says upd lets u spend more than their caps let them now, live or on agents: a
// cap of none, or 0, being no cap at all.
func (s *Service) raisesCaps(u domain.User, upd UserUpdate) bool {
	var (
		live  domain.LiveCaps
		agent domain.AgentCaps
	)

	if s.instanceCaps != nil {
		live, agent = s.instanceCaps()
	}

	if upd.LiveCaps != nil {
		was, now := u.LiveCaps.Over(live), upd.LiveCaps.Over(live)
		if more(was.PerOrder, now.PerOrder) || more(was.PerDay, now.PerDay) {
			return true
		}
	}

	if upd.AgentCaps != nil {
		was, now := u.AgentCaps.Over(agent), upd.AgentCaps.Over(agent)
		if moreDollars(was.Dollars, now.Dollars) || more(was.InputTokens, now.InputTokens) ||
			more(was.OutputTokens, now.OutputTokens) || more(was.TotalTokens, now.TotalTokens) {
			return true
		}
	}

	return false
}

// more says the cap now lets through more than it did: none, or 0, lets through everything.
func more[T int | int64](was, now *T) bool {
	switch unlimited := func(c *T) bool { return c == nil || *c == 0 }; {
	case unlimited(now):
		return !unlimited(was)
	case unlimited(was):
		return false
	}

	return *now > *was
}

func moreDollars(was, now *decimal.Decimal) bool {
	switch unlimited := func(c *decimal.Decimal) bool { return c == nil || c.IsZero() }; {
	case unlimited(now):
		return !unlimited(was)
	case unlimited(was):
		return false
	}

	return now.GreaterThan(*was)
}
