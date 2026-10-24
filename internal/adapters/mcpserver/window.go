package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// The agent's own window (D409): a tool whose live action waits for the user asks there
// too, where the user lets it and the client can, and waits a little for the answer. A link
// to Polythought's own page where the client shows links, the user deciding there; else,
// for an agent but never an API token, a form, whose answer counts, though the agent's own
// client could give it (A6). Confirmed, the call carries the action out itself, so the agent
// gets what came of it as if it had gone ahead; unanswered, it waits in Polythought.

// Window decides a waiting action from the agent's window: the confirmations service.
type Window interface {
	ConfirmInWindow(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
	RefuseInWindow(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
	Await(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, bool, error)
	Finish(ctx context.Context, c domain.Confirmation, out string, err error)
}

// inWindow asks the user about c in the agent's window and waits for the answer. carry
// says the call is to carry the action out now, then finish c; an error, that it ends so,
// refused; neither, that it waits in Polythought as before.
func (s *Server) inWindow(ctx context.Context, session *mcp.ServerSession, p auth.Principal, c domain.Confirmation) (domain.Confirmation, bool, error) {
	if s.window == nil || !p.User.AskInAgent || session == nil {
		return domain.Confirmation{}, false, nil
	}

	init := session.InitializeParams()
	if init == nil || init.Capabilities == nil || init.Capabilities.Elicitation == nil {
		return domain.Confirmation{}, false, nil
	}

	wait := 2 * time.Minute
	if s.windowWait != nil {
		wait = s.windowWait()
	}

	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	if caps := init.Capabilities.Elicitation; caps.URL != nil {
		return s.byLink(ctx, session, p, c)
	}

	// Whatever holds an API token would answer its own form (A6): a token asks by the link
	// alone.
	if p.GrantID == "" {
		return domain.Confirmation{}, false, nil
	}

	return s.byForm(ctx, session, p, c)
}

// byLink sends the user to Polythought's page for c, and waits for them to decide there.
func (s *Server) byLink(ctx context.Context, session *mcp.ServerSession, p auth.Principal, c domain.Confirmation) (domain.Confirmation, bool, error) {
	type decided struct {
		c     domain.Confirmation
		carry bool
		err   error
	}

	got := make(chan decided, 1)
	awaiting, stop := context.WithCancel(ctx)

	defer stop()

	// Waiting first, so an answer quicker than the client's is not missed.
	go func() {
		c, carry, err := s.window.Await(awaiting, p, c.ID)
		got <- decided{c, carry, err}
	}()

	res, err := session.Elicit(ctx, &mcp.ElicitParams{
		Mode: "url", ElicitationID: string(c.ID), URL: s.publicURL + "/confirmations/" + string(c.ID),
		Message: "Polythought asks you to confirm, or refuse, on its own page: " + c.What + ".",
	})
	if err != nil || res.Action != "accept" {
		// Not opened: it waits in Polythought alone.
		s.windowFailed(ctx, c, err)
		stop()
	}

	d := <-got

	if d.carry || errors.Is(d.err, domain.ErrConfirmRefused) {
		_ = session.NotifyElicitationComplete(context.WithoutCancel(ctx), &mcp.ElicitationCompleteParams{ElicitationID: string(c.ID)})
		return d.c, d.carry, d.err
	}

	return domain.Confirmation{}, false, nil // unanswered while the call waited
}

// byForm asks in the window itself, the answer counting.
func (s *Server) byForm(ctx context.Context, session *mcp.ServerSession, p auth.Principal, c domain.Confirmation) (domain.Confirmation, bool, error) {
	res, err := session.Elicit(ctx, &mcp.ElicitParams{
		Mode: "form", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Message: "Polythought: confirm, or decline to refuse: " + c.What + ". It waits for you in Polythought too.",
	})
	if err != nil {
		s.windowFailed(ctx, c, err)
		return domain.Confirmation{}, false, nil
	}

	switch res.Action {
	case "accept":
		decided, err := s.window.ConfirmInWindow(ctx, p, c.ID)
		if err != nil {
			return domain.Confirmation{}, false, err
		}

		return decided, true, nil
	case "decline":
		if _, err := s.window.RefuseInWindow(ctx, p, c.ID); err != nil {
			return domain.Confirmation{}, false, err
		}

		return domain.Confirmation{}, false, domain.ErrConfirmRefused
	}

	return domain.Confirmation{}, false, nil // dismissed: it waits in Polythought
}

func (s *Server) windowFailed(ctx context.Context, c domain.Confirmation, err error) {
	if err != nil && ctx.Err() == nil {
		s.log.Warn(ctx, "Not asked in the agent's window", "confirmation_id", c.ID, logging.Err(err))
	}
}
