package workflows

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// An admin's oversight of every user's workflows (D435): each read as its owner has it,
// its personal settings hidden and never its runs' items; switched off, held off until
// an admin releases it, and released.

// AdminAsked is what an admin's agent's held work on a user's workflow carries out.
type AdminAsked struct {
	Owner   domain.UserID `json:"owner"`
	Reason  string        `json:"reason,omitempty"`
	Hold    bool          `json:"hold,omitempty"`
	Release bool          `json:"release,omitempty"`
}

// reasonMost is the most runes an admin's reason keeps.
const reasonMost = domain.SwitchReasonMost

// AdminGet is one of owner's workflows for an admin, its personal settings hidden.
func (s *Service) AdminGet(ctx context.Context, p auth.Principal, owner domain.UserID, id domain.WorkflowID) (domain.Workflow, error) {
	if !p.User.IsAdmin() {
		return domain.Workflow{}, domain.ErrForbidden
	}

	w, err := s.repo.Get(ctx, owner, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	return s.hidePersonal(w), nil
}

// AdminExecutions are its runs, newest first, as the list has them: no items.
func (s *Service) AdminExecutions(ctx context.Context, p auth.Principal, owner domain.UserID, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.Execution], error) {
	if _, err := s.AdminGet(ctx, p, owner, id); err != nil {
		return ports.Page[domain.Execution]{}, err
	}

	return s.executions.List(ctx, id, f)
}

// AdminSwitchOff switches off one of owner's workflows for an admin, saying why, and holds
// it off with hold until an admin releases it. Its owner is told. One off already is only
// held, or left as it is.
func (s *Service) AdminSwitchOff(ctx context.Context, p auth.Principal, owner domain.UserID, id domain.WorkflowID, reason string, hold bool) (domain.Workflow, error) {
	if !p.User.IsAdmin() {
		return domain.Workflow{}, domain.ErrForbidden
	}

	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return domain.Workflow{}, fmt.Errorf("%w: say why, for its owner", domain.ErrInvalidInput)
	case utf8.RuneCountInString(reason) > reasonMost:
		return domain.Workflow{}, fmt.Errorf("%w: a reason is at most %d characters", domain.ErrInvalidInput, reasonMost)
	}

	w, err := s.repo.Get(ctx, owner, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	if !w.Active && (!hold || w.Hold != nil) {
		return s.hidePersonal(w), nil
	}

	what := fmt.Sprintf("Switch off another user's workflow %q", w.Name)
	if hold {
		what += ", and hold it off until an admin releases it"
	}

	if err := s.holdAdmin(ctx, p, w, what, "Why: "+reason, AdminAsked{Owner: owner, Reason: reason, Hold: hold}); err != nil {
		return domain.Workflow{}, err
	}

	now := s.now()
	wasOn := w.Active

	if wasOn {
		if err := s.repo.SetActive(ctx, owner, id, false, now, domain.Actor{}); err != nil {
			return domain.Workflow{}, err
		}

		s.supervisor.Stop(id)
		w.Active, w.ActedBy = false, domain.Actor{}

		if s.waits != nil {
			if err := s.waits.CancelWorkflow(ctx, owner, id, "cancelled: an admin switched the workflow off"); err != nil {
				s.log.Warn(ctx, "Waiting executions not cancelled", "workflow_id", id, "error", err.Error())
			}
		}
	}

	kind := domain.SwitchOff

	if hold {
		h := &domain.Hold{By: p.User.ID, Reason: reason, At: now}
		if err := s.repo.SetHold(ctx, owner, id, h); err != nil {
			return domain.Workflow{}, err
		}

		w.Hold, kind = h, domain.SwitchHold
	}

	s.switched(ctx, domain.Switch{WorkflowID: id, Kind: kind, Admin: true, Reason: reason})

	if s.pauser != nil {
		if hold {
			s.pauser.WorkflowHeld(owner, w, reason)
		} else {
			s.pauser.WorkflowPaused(owner, w, fmt.Sprintf("an admin switched it off, saying “%s”", reason))
		}
	}

	s.log.Info(ctx, "Workflow switched off by an admin", "workflow_id", id, "owner_id", owner, "admin_id", p.User.ID,
		"was_on", wasOn, "held", hold)

	return s.hidePersonal(w), nil
}

// AdminRelease lets one of owner's workflows that an admin held off be switched on again;
// its owner is told. One not held is left as it is.
func (s *Service) AdminRelease(ctx context.Context, p auth.Principal, owner domain.UserID, id domain.WorkflowID) (domain.Workflow, error) {
	if !p.User.IsAdmin() {
		return domain.Workflow{}, domain.ErrForbidden
	}

	w, err := s.repo.Get(ctx, owner, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	if w.Hold == nil {
		return s.hidePersonal(w), nil
	}

	what := fmt.Sprintf("Release another user's workflow %q, so it can be switched on again", w.Name)
	if err := s.holdAdmin(ctx, p, w, what, "", AdminAsked{Owner: owner, Release: true}); err != nil {
		return domain.Workflow{}, err
	}

	if err := s.repo.SetHold(ctx, owner, id, nil); err != nil {
		return domain.Workflow{}, err
	}

	w.Hold = nil
	s.switched(ctx, domain.Switch{WorkflowID: id, Kind: domain.SwitchRelease, Admin: true})

	if s.pauser != nil {
		s.pauser.WorkflowReleased(owner, w)
	}

	s.log.Info(ctx, "Workflow released by an admin", "workflow_id", id, "owner_id", owner, "admin_id", p.User.ID)

	return s.hidePersonal(w), nil
}

// holdAdmin is nil when an admin's work on a user's workflow goes ahead, and holds it for
// the admin when their agent or token asks, where they have such work wait (D410).
func (s *Service) holdAdmin(ctx context.Context, p auth.Principal, w domain.Workflow, what, detail string, asked AdminAsked) error {
	if s.confirm == nil || p.Itself() {
		return nil
	}

	return s.confirm.Hold(ctx, p, domain.Ask{
		Kind: domain.ConfirmAdminWorkflow, WorkflowID: w.ID, What: what, Detail: detail, Payload: asked,
	})
}
