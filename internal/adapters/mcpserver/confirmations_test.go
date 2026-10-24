package mcpserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

var held = domain.Confirmation{
	ID: "c-1", OwnerID: "u-1", Kind: domain.ConfirmRun, WorkflowID: "wf-1", GrantID: "g-1", Agent: "Claude Code",
	What: `Run "Spread watch" by hand, which trades live`, Status: domain.ConfirmPending, CreatedAt: at, ExpiresAt: at.Add(time.Hour),
}

// liveEditing is editing, its runs waiting for the user, as a live workflow's do (D406).
type liveEditing struct {
	*editing
}

func (liveEditing) Run(context.Context, auth.Principal, domain.WorkflowID, domain.NodeID) (domain.Execution, error) {
	return domain.Execution{}, &domain.AwaitingError{Confirmation: held}
}

type confirms struct{}

func (confirms) Get(_ context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	if id != held.ID || p.User.ID != held.OwnerID {
		return domain.Confirmation{}, domain.ErrNotFound
	}

	done := held
	done.Status, done.Result, done.DecidedAt = domain.ConfirmDone, "execution ex-1, succeeded", at.Add(time.Minute)

	return done, nil
}

// A live action that waits says so, and how to follow it (D406).
func TestLiveActionWaits(t *testing.T) {
	e := liveEditing{&editing{}}

	d := deps(alice, &trades{}, []domain.Scope{domain.ScopeBuild, domain.ScopeLive})
	d.Edit, d.Code, d.Confirmations = e, e.editing, confirms{}
	agent := connect(t, d)

	_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
	assert.Equal(t, "it waits for the user to confirm it, as it trades live or lets live trading start again: the user "+
		"confirms it in Polythought, until 2026-10-23T13:00:00Z; confirmation c-1", err, "said as it is, not an internal error")

	got, _ := call(t, agent, "get_confirmation", map[string]any{"id": "c-1"})
	assert.Equal(t, map[string]any{
		"id": "c-1", "kind": "run", "workflow": "wf-1", "what": `Run "Spread watch" by hand, which trades live`,
		"status": "done", "result": "execution ex-1, succeeded", "expiresAt": "2026-10-23T13:00:00Z", "decidedAt": "2026-10-23T12:01:00Z",
	}, got)

	_, err = call(t, agent, "get_confirmation", map[string]any{"id": "c-9"})
	assert.Contains(t, err, "not found")
}
