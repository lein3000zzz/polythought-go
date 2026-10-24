package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// MCPSession is an agent's or an API token's session on /mcp (D414): who, with what
// client, and when it last called.
type MCPSession struct {
	ID       string
	UserID   domain.UserID
	Holder   string // the grant or the token: "grant:<id>" or "token:<id>"
	Name     string // the agent or token, by its name then
	Client   string // its client, as it said, with its version
	Boot     string // the server's run it opened in
	OpenedAt time.Time
	SeenAt   time.Time
}

// ConnectedSession is a session open on /mcp, or one from before the server restarted
// whose agent has not come back yet (BeforeRestart).
type ConnectedSession struct {
	MCPSession
	BeforeRestart bool
}

// MCPSessionRepository keeps the sessions open on /mcp, so a restart does not forget them.
type MCPSessionRepository interface {
	// Open keeps a session; one kept already is kept as it is.
	Open(ctx context.Context, s MCPSession) error
	Seen(ctx context.Context, id string, at time.Time) error
	Close(ctx context.Context, id string) error
	// Supersede forgets holder's sessions from runs other than boot: its agent opened
	// another since the restart.
	Supersede(ctx context.Context, user domain.UserID, holder, boot string) error
	// Of is the user's sessions seen since since, the newest first.
	Of(ctx context.Context, user domain.UserID, since time.Time) ([]MCPSession, error)
	// Prune forgets every session not seen since before.
	Prune(ctx context.Context, before time.Time) error
}
