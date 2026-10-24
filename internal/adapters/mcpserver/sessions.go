package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// The agents connected now (D414): each session open on /mcp, kept in the database so a
// restart does not forget it. The SDK keeps no session across a restart; an agent whose
// session is gone opens another, which replaces those it had from before.

// seenEvery is how often a session's last call is written down: a list of who is
// connected needs no more.
const seenEvery = time.Minute

// newBoot names this run of the server, which its sessions are kept with.
func newBoot() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// track keeps the sessions its requests come in: one first seen is written down, its last
// call now and then, and its end once the SDK closes it.
func (s *Server) track(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)

		if s.sessions != nil {
			s.seen(ctx, req)
		}

		return res, err
	}
}

func (s *Server) seen(ctx context.Context, req mcp.Request) {
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok || ss.ID() == "" {
		return
	}

	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return
	}

	p, ok := extra.TokenInfo.Extra[principalName].(auth.Principal)
	if !ok {
		return
	}

	id, now := ss.ID(), time.Now()

	s.mu.Lock()
	last, known := s.tracked[id]
	if known && now.Sub(last) < seenEvery {
		s.mu.Unlock()
		return
	}
	s.tracked[id] = now
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if known {
		if err := s.sessions.Seen(ctx, id, now); err != nil {
			s.log.Warn(ctx, "MCP session not marked seen", logging.Err(err))
		}

		return
	}

	holder := holderOf(p)
	err := s.sessions.Open(ctx, ports.MCPSession{
		ID: id, UserID: p.User.ID, Holder: holder, Name: nameOf(p), Client: clientOf(ss), Boot: s.boot,
		OpenedAt: now, SeenAt: now,
	})
	if err == nil {
		err = s.sessions.Supersede(ctx, p.User.ID, holder, s.boot)
	}

	if err == nil {
		err = s.sessions.Prune(ctx, now.Add(-s.idle))
	}

	if err != nil {
		s.log.Warn(ctx, "MCP session not kept", logging.Err(err))
	}

	go s.closed(ss)
}

// closed forgets a session once the SDK closes it: its client ended it, or it went idle.
// One the server's shutdown ends stays, for the agent to come back to.
func (s *Server) closed(ss *mcp.ServerSession) {
	_ = ss.Wait()

	s.mu.Lock()
	delete(s.tracked, ss.ID())
	s.mu.Unlock()

	if s.closing.Load() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.sessions.Close(ctx, ss.ID()); err != nil {
		s.log.Warn(ctx, "MCP session not forgotten", logging.Err(err))
	}
}

// Shutdown keeps the sessions open as the server stops: their agents come back after.
func (s *Server) Shutdown(context.Context) error {
	s.closing.Store(true)
	return nil
}

// Connected is the owner's agents' and tokens' sessions: those open, and those from before
// a restart whose agents have not come back yet, the newest first.
func (s *Server) Connected(ctx context.Context, owner domain.UserID) ([]ports.ConnectedSession, error) {
	if s.sessions == nil {
		return []ports.ConnectedSession{}, nil
	}

	list, err := s.sessions.Of(ctx, owner, time.Now().Add(-s.idle))
	if err != nil {
		return nil, err
	}

	out := make([]ports.ConnectedSession, len(list))
	for i, m := range list {
		out[i] = ports.ConnectedSession{MCPSession: m, BeforeRestart: m.Boot != s.boot}
	}

	return out, nil
}

// holderOf is the grant or the token behind a session.
func holderOf(p auth.Principal) string {
	if p.GrantID != "" {
		return "grant:" + string(p.GrantID)
	}

	return "token:" + string(p.TokenID)
}

// nameOf is the agent or the token by its name.
func nameOf(p auth.Principal) string {
	if p.GrantID != "" {
		return p.Agent
	}

	return p.TokenName
}

// clientOf is the session's client as it said, with its version.
func clientOf(ss *mcp.ServerSession) string {
	params := ss.InitializeParams()
	if params == nil || params.ClientInfo == nil {
		return ""
	}

	name := params.ClientInfo.Name
	if params.ClientInfo.Title != "" {
		name = params.ClientInfo.Title
	}

	if params.ClientInfo.Version != "" {
		name += " " + params.ClientInfo.Version
	}

	return name
}
