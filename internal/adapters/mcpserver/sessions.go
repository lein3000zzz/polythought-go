package mcpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"slices"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
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

// SessionLimits bound the sessions a user's agents and tokens keep open (A10): past one, the
// oldest closes, and its client opens another when it calls again. Zero is no bound.
type SessionLimits struct {
	PerHolder int // open at once for one agent or one API token
	PerUser   int // open at once for all of a user's
}

// maxClient is the most of a client's name and version a session keeps (A10).
const maxClient = 128

// opened is a session seen, whose it is, and when it was last written down.
type opened struct {
	session *mcp.ServerSession
	user    domain.UserID
	holder  string
	at      time.Time // when it opened
	seen    time.Time
}

// limitOpening takes a small request to open a session, and refuses a user's initialize
// past their limit a minute (A10): each session lasts, and costs, until it goes idle.
func (s *Server) limitOpening(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Mcp-Session-Id") != "" {
			next.ServeHTTP(w, r)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxOpening+1))
		if err != nil {
			http.Error(w, "the request could not be read", http.StatusBadRequest)
			return
		}

		if len(body) > maxOpening {
			http.Error(w, "too big a request to open a session with", http.StatusRequestEntityTooLarge)
			return
		}

		var msg struct {
			Method string `json:"method"`
		}

		info := sdkauth.TokenInfoFromContext(r.Context())
		if json.Unmarshal(body, &msg) == nil && msg.Method == "initialize" && s.opening != nil && info != nil &&
			!s.opening.Allow("mcp-open:"+info.UserID) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many new sessions in a minute: use the one you have, or wait", http.StatusTooManyRequests)

			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// track keeps the sessions its requests come in: one first seen is written down, its last
// call now and then, and its end once the SDK closes it.
func (s *Server) track(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (res mcp.Result, err error) {
		// Nothing recovers what panics in a method but here: the server would stop (A24).
		defer func() {
			if r := recover(); r != nil {
				s.log.Error(ctx, "MCP method panicked", "method", method, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				res, err = nil, errors.New("internal error")
			}
		}()

		res, err = next(ctx, method, req)

		s.seen(ctx, req)

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

	id, now, holder := ss.ID(), time.Now(), holderOf(p)

	s.mu.Lock()
	o, known := s.tracked[id]
	if known && now.Sub(o.seen) < seenEvery {
		s.mu.Unlock()
		return
	}

	var over []*mcp.ServerSession

	if known {
		o.seen = now
	} else {
		s.tracked[id] = &opened{session: ss, user: p.User.ID, holder: holder, at: now, seen: now}
		over = s.overLimits(p.User.ID, holder)
	}
	s.mu.Unlock()

	for _, old := range over {
		go func() { _ = old.Close() }() // its client opens another when it calls again
	}

	if !known {
		go s.closed(ss)
	}

	if s.sessions == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if known {
		if err := s.sessions.Seen(ctx, id, now); err != nil {
			s.log.Warn(ctx, "MCP session not marked seen", logging.Err(err))
		}

		return
	}

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
}

// EndedGrant closes the sessions of a grant revoked, and EndedToken those of an API token
// (A16): its next request is refused anyway, but a call waiting meanwhile ends now.
func (s *Server) EndedGrant(id domain.GrantID) { s.closeHolder("grant:" + string(id)) }

func (s *Server) EndedToken(id domain.APITokenID) { s.closeHolder("token:" + string(id)) }

func (s *Server) closeHolder(holder string) {
	s.mu.Lock()

	var closing []*mcp.ServerSession

	for id, o := range s.tracked {
		if o.holder == holder {
			closing = append(closing, o.session)
			delete(s.tracked, id)
		}
	}
	s.mu.Unlock()

	for _, ss := range closing {
		go func() { _ = ss.Close() }()
	}
}

// overLimits is the user's sessions to close, the oldest first, so that the holder's and
// the user's open ones stay inside their limits. s.mu is held.
func (s *Server) overLimits(user domain.UserID, holder string) []*mcp.ServerSession {
	if s.limits == nil {
		return nil
	}

	limits := s.limits()

	var holders, users []*opened

	for _, o := range s.tracked {
		if o.user != user {
			continue
		}

		users = append(users, o)
		if o.holder == holder {
			holders = append(holders, o)
		}
	}

	oldest := func(a, b *opened) int { return a.at.Compare(b.at) }
	slices.SortFunc(holders, oldest)
	slices.SortFunc(users, oldest)

	closing := map[*opened]bool{}

	if most := limits.PerHolder; most > 0 && len(holders) > most {
		for _, o := range holders[:len(holders)-most] {
			closing[o] = true
		}
	}

	if most := limits.PerUser; most > 0 {
		left := len(users) - len(closing)
		for _, o := range users {
			if left <= most {
				break
			}

			if !closing[o] {
				closing[o], left = true, left-1
			}
		}
	}

	out := make([]*mcp.ServerSession, 0, len(closing))
	for o := range closing {
		delete(s.tracked, o.session.ID())
		out = append(out, o.session)
	}

	return out
}

// closed forgets a session once the SDK closes it: its client ended it, or it went idle.
// One the server's shutdown ends stays, for the agent to come back to.
func (s *Server) closed(ss *mcp.ServerSession) {
	_ = ss.Wait()

	s.mu.Lock()
	delete(s.tracked, ss.ID())
	s.mu.Unlock()

	if s.sessions == nil || s.closing.Load() {
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

	if r := []rune(name); len(r) > maxClient {
		name = string(r[:maxClient-1]) + "…"
	}

	return name
}
