package mcpserver_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/mcpserver"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/oauth"
)

// window is the confirmations service for the agent's window: what it was told, and an
// answer in Polythought, given to Await.
type window struct {
	mu        sync.Mutex
	confirmed []domain.ConfirmationID
	refused   []domain.ConfirmationID
	finished  []error

	inPolythought chan domain.ConfirmStatus // the user's answer there, while a call awaits it
}

func (w *window) ConfirmInWindow(_ context.Context, _ auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.confirmed = append(w.confirmed, id)
	c := held
	c.Status, c.DecidedVia = domain.ConfirmConfirmed, domain.ConfirmViaAgent

	return c, nil
}

func (w *window) RefuseInWindow(_ context.Context, _ auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.refused = append(w.refused, id)

	return held, nil
}

func (w *window) Await(ctx context.Context, _ auth.Principal, _ domain.ConfirmationID) (domain.Confirmation, bool, error) {
	select {
	case status := <-w.inPolythought:
		c := held
		c.Status = status

		if status == domain.ConfirmRefused {
			return c, false, domain.ErrConfirmRefused
		}

		return c, true, nil
	case <-ctx.Done():
		return domain.Confirmation{}, false, ctx.Err()
	}
}

func (w *window) Finish(_ context.Context, _ domain.Confirmation, _ string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.finished = append(w.finished, err)
}

// windowed is an agent whose user lets it ask in its window, its client answering as
// answer does, by a link where links says it can.
func windowed(t *testing.T, w *window, ask bool, links bool, answer func(*mcp.ElicitRequest) string) (*mcp.ClientSession, *[]*mcp.ElicitParams) {
	t.Helper()

	return windowedAs(t, w, ask, links, answer, nil)
}

// windowedAs is windowed, the principal as as makes it.
func windowedAs(
	t *testing.T, w *window, ask bool, links bool, answer func(*mcp.ElicitRequest) string, as func(*auth.Principal),
) (*mcp.ClientSession, *[]*mcp.ElicitParams) {
	t.Helper()

	u := alice
	u.AskInAgent = ask

	e := liveEditing{&editing{}}
	d := deps(u, &trades{}, []domain.Scope{domain.ScopeBuild, domain.ScopeLive})
	d.Edit, d.Code, d.Confirmations, d.Window = e, e.editing, confirms{}, w
	d.WindowWait = func() time.Duration { return 2 * time.Second }

	if as != nil {
		access := d.Agents.(agents).access["poa_agent"]
		as(&access.Principal)
		d.Agents = agents{access: map[string]oauth.Access{"poa_agent": access}}
	}

	return windowedWith(t, d, links, answer)
}

// changing is the agent's access, which the user changes while a call waits.
type changing struct {
	mu     sync.Mutex
	access oauth.Access
	gone   bool
}

func (c *changing) Enabled() bool { return true }

func (c *changing) Verify(context.Context, string) (oauth.Access, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.gone {
		return oauth.Access{}, domain.ErrUnauthenticated
	}

	return c.access, nil
}

func (c *changing) set(f func(*changing)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	f(c)
}

// windowedChanging is windowed, its agent's access as access has it at each check.
func windowedChanging(
	t *testing.T, w *window, access *changing, answer func(*mcp.ElicitRequest) string,
) (*mcp.ClientSession, *[]*mcp.ElicitParams) {
	t.Helper()

	e := liveEditing{&editing{}}
	d := deps(alice, &trades{}, nil)
	d.Edit, d.Code, d.Confirmations, d.Window = e, e.editing, confirms{}, w
	d.WindowWait = func() time.Duration { return 2 * time.Second }
	d.Agents = access

	return windowedWith(t, d, false, answer)
}

func windowedWith(t *testing.T, d mcpserver.Deps, links bool, answer func(*mcp.ElicitRequest) string) (*mcp.ClientSession, *[]*mcp.ElicitParams) {
	t.Helper()

	var (
		mu    sync.Mutex
		asked []*mcp.ElicitParams
	)

	opts := &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		mu.Lock()
		asked = append(asked, req.Params)
		mu.Unlock()

		return &mcp.ElicitResult{Action: answer(req)}, nil
	}}

	if links {
		opts.Capabilities = &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}
	}

	return connectWith(t, d, opts), &asked
}

// A live action waiting for the user is asked in the agent's window too, where the user
// lets it (D409): a form whose answer counts, or a link to Polythought where the client
// shows links; confirmed in time, the call carries it out and answers as if it had gone
// ahead.
func TestAgentWindow(t *testing.T) {
	t.Run("confirmed in the form", func(t *testing.T) {
		w := &window{}
		agent, asked := windowed(t, w, true, false, func(*mcp.ElicitRequest) string { return "accept" })

		got, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		require.Empty(t, err)
		assert.Equal(t, "ex-1", got["id"], "what it did, as if it had gone ahead")

		require.Len(t, *asked, 1)
		assert.Equal(t, "form", (*asked)[0].Mode)
		assert.Contains(t, (*asked)[0].Message, `Run "Spread watch" by hand, which trades live`)

		w.mu.Lock()
		defer w.mu.Unlock()

		assert.Equal(t, []domain.ConfirmationID{"c-1"}, w.confirmed)
		assert.Equal(t, []error{nil}, w.finished, "its end kept")
	})

	t.Run("declined in the form, refused", func(t *testing.T) {
		w := &window{}
		agent, _ := windowed(t, w, true, false, func(*mcp.ElicitRequest) string { return "decline" })

		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Equal(t, "the user refused it", err)

		w.mu.Lock()
		defer w.mu.Unlock()

		assert.Equal(t, []domain.ConfirmationID{"c-1"}, w.refused)
	})

	t.Run("dismissed, it waits in Polythought", func(t *testing.T) {
		agent, _ := windowed(t, &window{}, true, false, func(*mcp.ElicitRequest) string { return "cancel" })

		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "it waits for the user to confirm it")
	})

	t.Run("never a form for an API token, whose holder would answer it (A6)", func(t *testing.T) {
		w := &window{}
		token, asked := windowedAs(t, w, true, false, func(*mcp.ElicitRequest) string { return "accept" },
			func(p *auth.Principal) { p.GrantID, p.TokenID, p.TokenName = "", "t-1", "ci" })

		_, err := call(t, token, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "it waits for the user to confirm it")
		assert.Empty(t, *asked, "never asked in a form")

		w.mu.Lock()
		defer w.mu.Unlock()

		assert.Empty(t, w.confirmed)
	})

	t.Run("accepted after the agent was revoked meanwhile, nothing carried out (A16)", func(t *testing.T) {
		w := &window{}
		u := alice
		u.AskInAgent = true
		access := &changing{access: oauth.Access{
			Principal: auth.Principal{User: u, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeBuild, domain.ScopeLive}},
			ExpiresAt: time.Now().Add(time.Hour),
		}}

		agent, _ := windowedChanging(t, w, access, func(*mcp.ElicitRequest) string {
			access.set(func(c *changing) { c.gone = true }) // revoked while the form was open
			return "accept"
		})

		// Its answer comes with its token, refused now: the call ends unanswered.
		_, err := agent.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_workflow", Arguments: map[string]any{"id": "wf-1"}})
		require.ErrorContains(t, err, "Unauthorized")

		w.mu.Lock()
		defer w.mu.Unlock()

		assert.Empty(t, w.confirmed, "not confirmed for an agent that is gone")
	})

	t.Run("accepted after the user stopped it asking there, it waits in Polythought (A16)", func(t *testing.T) {
		w := &window{}
		u := alice
		u.AskInAgent = true
		access := &changing{access: oauth.Access{
			Principal: auth.Principal{User: u, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeBuild, domain.ScopeLive}},
			ExpiresAt: time.Now().Add(time.Hour),
		}}

		agent, _ := windowedChanging(t, w, access, func(*mcp.ElicitRequest) string {
			access.set(func(c *changing) { c.access.Principal.User.AskInAgent = false })
			return "accept"
		})

		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "it waits for the user to confirm it")

		w.mu.Lock()
		defer w.mu.Unlock()

		assert.Empty(t, w.confirmed)
	})

	t.Run("confirmed by a link, then carried out only with the scope it still has (A16)", func(t *testing.T) {
		w := &window{inPolythought: make(chan domain.ConfirmStatus, 1)}
		u := alice
		u.AskInAgent = true
		access := &changing{access: oauth.Access{
			Principal: auth.Principal{User: u, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeBuild, domain.ScopeLive}},
			ExpiresAt: time.Now().Add(time.Hour),
		}}

		e := liveEditing{&editing{}}
		d := deps(alice, &trades{}, nil)
		d.Edit, d.Code, d.Confirmations, d.Window = e, e.editing, confirms{}, w
		d.WindowWait = func() time.Duration { return 2 * time.Second }
		d.Agents = access

		agent, _ := windowedWith(t, d, true, func(*mcp.ElicitRequest) string {
			access.set(func(c *changing) { c.access.Principal.Scopes = []domain.Scope{domain.ScopeRead} })
			w.inPolythought <- domain.ConfirmConfirmed // the user confirms in Polythought
			return "accept"
		})

		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "build")

		w.mu.Lock()
		defer w.mu.Unlock()

		require.Len(t, w.finished, 1)
		require.Error(t, w.finished[0], "kept as failed, not carried out")
	})

	t.Run("not where the user has it off", func(t *testing.T) {
		agent, asked := windowed(t, &window{}, false, false, func(*mcp.ElicitRequest) string { return "accept" })

		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "it waits for the user to confirm it")
		assert.Empty(t, *asked, "never asked")
	})

	t.Run("a link to Polythought, confirmed there", func(t *testing.T) {
		w := &window{inPolythought: make(chan domain.ConfirmStatus, 1)}
		agent, asked := windowed(t, w, true, true, func(req *mcp.ElicitRequest) string {
			w.inPolythought <- domain.ConfirmConfirmed // the user opens it and confirms
			return "accept"
		})

		got, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		require.Empty(t, err)
		assert.Equal(t, "ex-1", got["id"])

		require.Len(t, *asked, 1)
		assert.Equal(t, "url", (*asked)[0].Mode)
		assert.Equal(t, publicURL+"/confirmations/c-1", (*asked)[0].URL)
		assert.Equal(t, "c-1", (*asked)[0].ElicitationID)
	})

	t.Run("a link, refused there", func(t *testing.T) {
		w := &window{inPolythought: make(chan domain.ConfirmStatus, 1)}
		agent, _ := windowed(t, w, true, true, func(*mcp.ElicitRequest) string {
			w.inPolythought <- domain.ConfirmRefused
			return "accept"
		})

		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Equal(t, "the user refused it", err)
	})

	t.Run("a link not opened, it waits in Polythought", func(t *testing.T) {
		agent, _ := windowed(t, &window{inPolythought: make(chan domain.ConfirmStatus)}, true, true,
			func(*mcp.ElicitRequest) string { return "decline" })

		start := time.Now()
		_, err := call(t, agent, "run_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "it waits for the user to confirm it")
		assert.Less(t, time.Since(start), time.Second, "no longer waiting once it is not opened")
	})
}
