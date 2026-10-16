package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	librarymocks "github.com/lein3000zzz/polythought-go/internal/mocks/library"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	now   = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "alice", Role: domain.RoleUser}, SessionID: "s-1"}
	bob   = auth.Principal{User: domain.User{ID: "bob", Role: domain.RoleUser}, SessionID: "s-2"}
	root  = auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}, SessionID: "s-3"}
)

// registry has every real node type, so templates are checked against what runs.
func registry(t *testing.T) *engine.Registry {
	t.Helper()

	r := engine.NewRegistry()
	require.NoError(t, nodes.Register(r, nodes.Deps{
		Markets:      portsmocks.NewMockMarketReader(t),
		Events:       portsmocks.NewMockEventReader(t),
		OrderBooks:   portsmocks.NewMockOrderBookReader(t),
		PriceHistory: portsmocks.NewMockPriceHistoryReader(t),
		Trades:       portsmocks.NewMockTradeReader(t),
		Positions:    portsmocks.NewMockPositionReader(t),
		Account:      portsmocks.NewMockAccountReader(t),
		Wallets:      portsmocks.NewMockWalletDirectory(t),
		Webhooks:     portsmocks.NewMockWebhookSender(t),
		Feed:         portsmocks.NewMockMarketFeed(t),
		Logger:       logging.Noop(),
	}))

	return r
}

type deps struct {
	repo      *portsmocks.MockTemplateRepository
	workflows *librarymocks.MockWorkflows
}

func newService(t *testing.T) (*library.Service, deps) {
	t.Helper()

	d := deps{repo: portsmocks.NewMockTemplateRepository(t), workflows: librarymocks.NewMockWorkflows(t)}

	s, err := library.New(d.repo, d.workflows, registry(t), logging.Noop(), library.WithClock(func() time.Time { return now }))
	require.NoError(t, err)

	return s, d
}

// placeholder is a value of the param's kind, to fill a template's inputs in tests.
func placeholder(p engine.ParamSpec) any {
	switch p.Kind {
	case engine.ParamWallet:
		return "w-1"
	case engine.ParamTokens, engine.ParamList:
		return []any{"x"}
	case engine.ParamDuration:
		return "1h"
	case engine.ParamNumber, engine.ParamDecimal:
		return 1
	case engine.ParamBool:
		return true
	default:
		return "https://chat.example/hook"
	}
}

func TestBuiltinScenarios(t *testing.T) {
	builtins, err := library.Builtins()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(builtins), 5)

	s, _ := newService(t)
	reg := registry(t)
	ids := map[domain.TemplateID]bool{}

	for _, tpl := range builtins {
		t.Run(string(tpl.ID), func(t *testing.T) {
			assert.False(t, ids[tpl.ID], "IDs are unique")
			ids[tpl.ID] = true

			assert.NotEmpty(t, tpl.Name)
			assert.NotEmpty(t, tpl.Summary)
			assert.NotEmpty(t, tpl.Description)

			for _, n := range tpl.Nodes {
				assert.NotEmpty(t, n.Notes, "node %s explains itself", n.ID)
			}

			entry, err := s.Get(context.Background(), alice, tpl.ID)
			require.NoError(t, err)

			// Filled in as the "use" dialog would, it is a valid workflow.
			w := domain.Workflow{Nodes: append([]domain.NodeSpec(nil), tpl.Nodes...), Connections: tpl.Connections}
			for _, in := range entry.Inputs {
				for i := range w.Nodes {
					if w.Nodes[i].ID == in.NodeID {
						params := map[string]any{in.Param.Name: placeholder(in.Param)}
						for k, v := range w.Nodes[i].Params {
							params[k] = v
						}

						w.Nodes[i].Params = params
					}
				}
			}

			require.NoError(t, reg.Validate(w))
			require.NoError(t, reg.CheckFields(w), "its field settings name fields its items have")
			require.NoError(t, reg.CheckExpressions(w, func(string) error { return nil }), "and its expressions are where they work")
		})
	}
}

func TestInputs(t *testing.T) {
	s, _ := newService(t)

	tests := []struct {
		id   domain.TemplateID
		want []string // node.param
	}{
		{id: "first-workflow"},
		{id: "chat-alerts", want: []string{"chat.url", "chat.credential"}}, // the credential is optional
		{id: "your-positions", want: []string{"positions.wallet"}},
		{id: "low-balance", want: []string{"balance.wallet"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			entry, err := s.Get(context.Background(), alice, tt.id)
			require.NoError(t, err)

			var got []string
			for _, in := range entry.Inputs {
				got = append(got, string(in.NodeID)+"."+in.Param.Name)
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUse(t *testing.T) {
	ctx := context.Background()

	t.Run("copies the template with the values asked for", func(t *testing.T) {
		s, d := newService(t)

		d.workflows.EXPECT().Create(ctx, alice, mock.MatchedBy(func(def workflows.Definition) bool {
			return def.Name == "My alerts" && def.Nodes[5].Params["url"] == "https://chat.example/hook" &&
				def.Nodes[5].Params["credential"] == "c-1" &&
				def.Nodes[5].Params["send"] == "all" && len(def.Connections) == 5
		})).Return(domain.Workflow{ID: "wf-9"}, nil)

		w, err := s.Use(ctx, alice, "chat-alerts", library.UseInput{
			Name:   " My alerts ",
			Values: map[domain.NodeID]map[string]any{"chat": {"url": "https://chat.example/hook", "credential": "c-1"}},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.WorkflowID("wf-9"), w.ID)

		again, err := library.Builtins()
		require.NoError(t, err)
		assert.Nil(t, again[4].Nodes[5].Params["url"], "the scenario itself is untouched")
	})

	t.Run("only what the template asks for", func(t *testing.T) {
		s, _ := newService(t)

		_, err := s.Use(ctx, alice, "chat-alerts", library.UseInput{
			Values: map[domain.NodeID]map[string]any{"search": {"query": "sneaky"}},
		})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("an unknown template", func(t *testing.T) {
		s, d := newService(t)
		d.repo.EXPECT().Get(ctx, domain.TemplateID("ghost")).Return(domain.Template{}, domain.ErrNotFound)

		_, err := s.Use(ctx, alice, "ghost", library.UseInput{})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestShare(t *testing.T) {
	ctx := context.Background()

	source := domain.Workflow{
		ID: "wf-1", OwnerID: alice.User.ID, Name: "Balance pings",
		Nodes: []domain.NodeSpec{
			{ID: "t", Type: "trigger.manual"},
			{ID: "b", Type: "polymarket.balance", Params: map[string]any{"wallet": "w-alice"}, Notes: "mine"},
			{ID: "h", Type: "output.webhook", Params: map[string]any{"url": "https://secret/hook", "send": "each", "credential": "c-alice"}},
		},
		Connections: []domain.Connection{{From: "t", FromPort: "main", To: "b", ToPort: "main"}},
	}

	t.Run("leaves out personal settings and becomes an input", func(t *testing.T) {
		s, d := newService(t)
		d.workflows.EXPECT().Get(ctx, alice, source.ID).Return(source, nil)
		d.repo.EXPECT().BySource(ctx, alice.User.ID, source.ID).Return(domain.Template{}, domain.ErrNotFound)
		d.repo.EXPECT().CountByAuthor(ctx, alice.User.ID).Return(3, nil)
		d.repo.EXPECT().Save(ctx, mock.Anything).RunAndReturn(func(_ context.Context, tpl domain.Template) (domain.Template, error) {
			return tpl, nil
		})

		entry, err := s.Share(ctx, alice, source.ID, library.ShareInput{Summary: " Pings ", Description: "How it works"})
		require.NoError(t, err)

		assert.True(t, entry.Mine)
		assert.Equal(t, "Balance pings", entry.Name)
		assert.Equal(t, "Pings", entry.Summary)
		assert.Equal(t, source.ID, entry.SourceID)
		assert.Equal(t, now, entry.CreatedAt)
		assert.Empty(t, entry.Nodes[1].Params, "the wallet stays home")
		assert.Equal(t, map[string]any{"send": "each"}, entry.Nodes[2].Params, "so do the webhook URL and credential")
		assert.Equal(t, "mine", entry.Nodes[1].Notes)
		assert.Equal(t, "w-alice", source.Nodes[1].Params["wallet"], "the workflow is untouched")

		var inputs []string
		for _, in := range entry.Inputs {
			inputs = append(inputs, string(in.NodeID)+"."+in.Param.Name)
		}

		assert.Equal(t, []string{"b.wallet", "h.url", "h.credential"}, inputs)
	})

	t.Run("sharing again updates the copy, whatever the allowance", func(t *testing.T) {
		s, d := newService(t)
		d.workflows.EXPECT().Get(ctx, alice, source.ID).Return(source, nil)
		d.repo.EXPECT().BySource(ctx, alice.User.ID, source.ID).Return(domain.Template{ID: "tpl-1"}, nil)
		d.repo.EXPECT().Save(ctx, mock.Anything).Return(domain.Template{ID: "tpl-1", AuthorID: alice.User.ID}, nil)

		entry, err := s.Share(ctx, alice, source.ID, library.ShareInput{})
		require.NoError(t, err)
		assert.Equal(t, domain.TemplateID("tpl-1"), entry.ID)
	})

	t.Run("a member shares a limited number", func(t *testing.T) {
		s, d := newService(t)
		d.workflows.EXPECT().Get(ctx, alice, source.ID).Return(source, nil)
		d.repo.EXPECT().BySource(ctx, alice.User.ID, source.ID).Return(domain.Template{}, domain.ErrNotFound)
		d.repo.EXPECT().CountByAuthor(ctx, alice.User.ID).Return(library.MaxShared, nil)

		_, err := s.Share(ctx, alice, source.ID, library.ShareInput{})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("only your own workflows", func(t *testing.T) {
		s, d := newService(t)
		d.workflows.EXPECT().Get(ctx, bob, source.ID).Return(domain.Workflow{}, domain.ErrNotFound)

		_, err := s.Share(ctx, bob, source.ID, library.ShareInput{})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("a long description is refused", func(t *testing.T) {
		s, _ := newService(t)

		_, err := s.Share(ctx, alice, source.ID, library.ShareInput{Description: string(make([]rune, 4001))})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})
}

func TestListAndDelete(t *testing.T) {
	ctx := context.Background()
	shared := domain.Template{ID: "tpl-1", AuthorID: alice.User.ID, Name: "Alice's"}

	t.Run("built-in scenarios lead the first page only", func(t *testing.T) {
		s, d := newService(t)
		d.repo.EXPECT().List(ctx, ports.PageFilter{Limit: 10}).Return(ports.Page[domain.Template]{Items: []domain.Template{shared}, NextCursor: "tpl-1"}, nil)
		d.repo.EXPECT().List(ctx, ports.PageFilter{Limit: 10, Cursor: "tpl-1"}).Return(ports.Page[domain.Template]{}, nil)

		first, err := s.List(ctx, bob, ports.PageFilter{Limit: 10})
		require.NoError(t, err)

		builtins, _ := library.Builtins()
		require.Len(t, first.Items, len(builtins)+1)
		assert.True(t, first.Items[0].BuiltIn())
		assert.False(t, first.Items[len(first.Items)-1].Mine, "bob did not share it")
		assert.Equal(t, "tpl-1", first.NextCursor)

		second, err := s.List(ctx, bob, ports.PageFilter{Limit: 10, Cursor: "tpl-1"})
		require.NoError(t, err)
		assert.Empty(t, second.Items)
	})

	tests := []struct {
		name    string
		who     auth.Principal
		id      domain.TemplateID
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "the author deletes", who: alice, id: shared.ID,
			setup: func(d deps) {
				d.repo.EXPECT().Get(ctx, shared.ID).Return(shared, nil)
				d.repo.EXPECT().Delete(ctx, shared.ID).Return(nil)
			},
		},
		{
			name: "an admin deletes, to keep the library clean", who: root, id: shared.ID,
			setup: func(d deps) {
				d.repo.EXPECT().Get(ctx, shared.ID).Return(shared, nil)
				d.repo.EXPECT().Delete(ctx, shared.ID).Return(nil)
			},
		},
		{
			name: "others may not", who: bob, id: shared.ID,
			setup: func(d deps) {
				d.repo.EXPECT().Get(ctx, shared.ID).Return(shared, nil)
			},
			wantErr: domain.ErrForbidden,
		},
		{name: "built-ins stay", who: root, id: "first-workflow", setup: func(deps) {}, wantErr: domain.ErrForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, d := newService(t)
			tt.setup(d)

			require.ErrorIs(t, s.Delete(ctx, tt.who, tt.id), tt.wantErr)
		})
	}
}
