package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// A live action an agent asks for may wait for its user to confirm it, in Polythought
// (D406): the tool says so, and get_confirmation how it went.

// Confirmations are the live actions waiting for their users: the confirmations service.
type Confirmations interface {
	Get(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
}

type confirmationIn struct {
	ID string `json:"id" jsonschema:"the confirmation's ID, as the tool that waited gave it"`
}

type confirmationOut struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind" jsonschema:"go_live, switch_on, run, change or kill_off"`
	Workflow  string     `json:"workflow,omitempty"`
	What      string     `json:"what"`
	Status    string     `json:"status" jsonschema:"pending: the user has not answered; confirmed: being carried out; done, failed, refused or expired"`
	Result    string     `json:"result,omitempty" jsonschema:"what came of it: an execution, or why it failed"`
	ExpiresAt time.Time  `json:"expiresAt"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
}

func (s *Server) addConfirmationTools(srv *mcp.Server) {
	add(s, srv, Tool[confirmationIn, confirmationOut]{
		Name: "get_confirmation", Title: "Get a confirmation", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "How a live action that waited for the user stands: going live, switching on or running a workflow " +
			"that trades live, changing one that is on, or turning the kill switch off. The user confirms or refuses it " +
			"in Polythought; confirmed, it is carried out as you asked. Ask the user, rather than calling this again and again.",
		Handle: s.getConfirmation,
	})
}

func (s *Server) getConfirmation(ctx context.Context, p auth.Principal, in confirmationIn) (confirmationOut, error) {
	c, err := s.confirms.Get(ctx, p, domain.ConfirmationID(in.ID))
	if err != nil {
		return confirmationOut{}, err
	}

	return confirmationOut{
		ID: string(c.ID), Kind: string(c.Kind), Workflow: string(c.WorkflowID), What: c.What, Status: string(c.Status),
		Result: c.Result, ExpiresAt: c.ExpiresAt, DecidedAt: optionalTime(c.DecidedAt),
	}, nil
}
