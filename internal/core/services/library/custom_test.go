package library_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	custom "github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	librarymocks "github.com/lein3000zzz/polythought-go/internal/mocks/library"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// split is a custom node with a required wallet, as its author has it.
func split() domain.CustomNode {
	return domain.CustomNode{
		ID: "n-alice", OwnerID: alice.User.ID, Version: 3, CreatedAt: now, UpdatedAt: now,
		Name: "split", Label: "Split", Description: "Wide or tight.",
		Settings: []domain.CustomSetting{
			{Name: "threshold", Label: "threshold", Kind: "decimal", Default: "0.02"},
			{Name: "wallet", Label: "Wallet", Kind: "wallet", Required: true},
		},
		Outputs: []string{"wide", "tight"},
		Items:   domain.CustomItems{Passed: true, Fields: []domain.CustomField{}},
		Source:  "export function run(items) { return { wide: items } }",
	}
}

// bundled is split as a template brings it: its definition only.
func bundled() domain.CustomNode {
	n := split()

	return domain.CustomNode{
		Name: n.Name, Label: n.Label, Description: n.Description,
		Settings: n.Settings, Outputs: n.Outputs, Items: n.Items, Source: n.Source,
	}
}

func usingSplit() domain.Template {
	return domain.Template{
		ID: "tpl-1", AuthorID: alice.User.ID, Name: "Spread watch",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"},
			{ID: "split", Type: "custom.split", Params: map[string]any{"threshold": "0.03"}},
			{ID: "log", Type: "output.log"},
		},
		Connections: []domain.Connection{
			{From: "manual", FromPort: "main", To: "split", ToPort: "main"},
			{From: "split", FromPort: "wide", To: "log", ToPort: "main"},
		},
		CustomNodes: []domain.CustomNode{bundled()},
	}
}

type customDeps struct {
	deps
	custom *librarymocks.MockCustomNodes
}

func withCustom(t *testing.T) (*library.Service, customDeps) {
	t.Helper()

	d := customDeps{
		deps:   deps{repo: portsmocks.NewMockTemplateRepository(t), workflows: librarymocks.NewMockWorkflows(t)},
		custom: librarymocks.NewMockCustomNodes(t),
	}

	own, err := registry(t).With(customnodes.New(split(), nil, nil))
	require.NoError(t, err)

	s, err := library.New(d.repo, d.workflows, registry(t), logging.Noop(),
		library.WithOwnTypes(own), library.WithCustomNodes(d.custom))
	require.NoError(t, err)

	return s, d
}

func TestShareBundlesCustomNodes(t *testing.T) {
	ctx := context.Background()
	s, d := withCustom(t)

	other := split()
	other.Name = "unused"

	source := domain.Workflow{ID: "wf-1", OwnerID: alice.User.ID, Name: "Spread watch", Nodes: []domain.NodeSpec{
		{ID: "manual", Type: "trigger.manual"},
		{ID: "split", Type: "custom.split", Params: map[string]any{"threshold": "0.03", "wallet": "w-alice"}},
	}}

	d.workflows.EXPECT().Get(ctx, alice, source.ID).Return(source, nil)
	d.custom.EXPECT().List(ctx, alice).Return([]domain.CustomNode{split(), other}, nil)
	d.repo.EXPECT().BySource(ctx, alice.User.ID, source.ID).Return(domain.Template{}, domain.ErrNotFound)
	d.repo.EXPECT().CountByAuthor(ctx, alice.User.ID).Return(0, nil)
	d.repo.EXPECT().Save(ctx, mock.Anything).RunAndReturn(func(_ context.Context, tpl domain.Template) (domain.Template, error) {
		return tpl, nil
	})

	entry, err := s.Share(ctx, alice, source.ID, library.ShareInput{})
	require.NoError(t, err)

	assert.Equal(t, []domain.CustomNode{bundled()}, entry.CustomNodes, "the one it uses, as a definition")
	assert.Equal(t, map[string]any{"threshold": "0.03"}, entry.Nodes[1].Params, "its wallet stays home")

	require.Len(t, entry.CustomTypes, 1)
	assert.Equal(t, "custom.split", entry.CustomTypes[0].Name)

	var inputs []string
	for _, in := range entry.Inputs {
		inputs = append(inputs, string(in.NodeID)+"."+in.Param.Name)
	}

	assert.Equal(t, []string{"split.wallet"}, inputs, "asked of whoever uses it")
}

func TestUseCopiesCustomNodes(t *testing.T) {
	ctx := context.Background()
	values := library.UseInput{Values: map[domain.NodeID]map[string]any{"split": {"wallet": "w-bob"}}}

	nodeTypes := func(def workflows.Definition) []string {
		out := make([]string, len(def.Nodes))
		for i, n := range def.Nodes {
			out[i] = n.Type
		}

		return out
	}

	t.Run("a copy when they have none", func(t *testing.T) {
		s, d := withCustom(t)
		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(usingSplit(), nil)
		d.custom.EXPECT().List(ctx, bob).Return(nil, nil)
		d.custom.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def custom.Definition) bool {
			return def.Name == "split" && def.Label == "Split" && len(def.Settings) == 2 && strings.Contains(def.Source, "wide")
		})).Return(domain.CustomNode{ID: "n-bob", Name: "split"}, nil)
		d.workflows.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def workflows.Definition) bool {
			return nodeTypes(def)[1] == "custom.split" && def.Nodes[1].Params["wallet"] == "w-bob"
		})).Return(domain.Workflow{ID: "wf-bob"}, nil)

		w, err := s.Use(ctx, bob, "tpl-1", values)
		require.NoError(t, err)
		assert.Equal(t, domain.WorkflowID("wf-bob"), w.ID)
	})

	t.Run("theirs when it is the same", func(t *testing.T) {
		s, d := withCustom(t)

		theirs := split()
		theirs.ID, theirs.OwnerID = "n-bob", bob.User.ID

		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(usingSplit(), nil)
		d.custom.EXPECT().List(ctx, bob).Return([]domain.CustomNode{theirs}, nil)
		d.workflows.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def workflows.Definition) bool {
			return nodeTypes(def)[1] == "custom.split"
		})).Return(domain.Workflow{ID: "wf-bob"}, nil)

		_, err := s.Use(ctx, bob, "tpl-1", values)
		require.NoError(t, err)
	})

	t.Run("a renamed copy when theirs differs, the workflow pointing at it", func(t *testing.T) {
		s, d := withCustom(t)

		theirs, second := split(), split()
		theirs.Source = "export function run(items) { return { tight: items } }"
		second.Name, second.Label = "split-2", "Taken, and not the same"

		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(usingSplit(), nil)
		d.custom.EXPECT().List(ctx, bob).Return([]domain.CustomNode{theirs, second}, nil)
		d.custom.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def custom.Definition) bool { return def.Name == "split-3" })).
			Return(domain.CustomNode{ID: "n-3", Name: "split-3"}, nil)
		d.workflows.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def workflows.Definition) bool {
			return assert.ObjectsAreEqual([]string{"trigger.manual", "custom.split-3", "output.log"}, nodeTypes(def))
		})).Return(domain.Workflow{ID: "wf-bob"}, nil)

		_, err := s.Use(ctx, bob, "tpl-1", values)
		require.NoError(t, err)
	})

	t.Run("a copy an earlier use made, when it is the same", func(t *testing.T) {
		s, d := withCustom(t)

		differs := split()
		differs.Source = "export function run(items) { return { tight: items } }"

		named := func(name string, n domain.CustomNode) domain.CustomNode {
			n.Name = name
			return n
		}

		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(usingSplit(), nil)
		d.custom.EXPECT().List(ctx, bob).Return([]domain.CustomNode{
			differs, named("split-02", split()), named("split-x2", split()),
			named("split-3", split()), named("split-2", split()),
		}, nil)
		d.workflows.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def workflows.Definition) bool {
			return nodeTypes(def)[1] == "custom.split-2"
		})).Return(domain.Workflow{ID: "wf-bob"}, nil)

		_, err := s.Use(ctx, bob, "tpl-1", values)
		require.NoError(t, err, "no copy made: the lowest of the same is used")
	})

	t.Run("a long name stays within 40", func(t *testing.T) {
		s, d := withCustom(t)

		long := strings.Repeat("a", 39) + "b"
		tpl := usingSplit()
		tpl.CustomNodes[0].Name = long
		tpl.Nodes[1].Type = "custom." + long

		theirs := split()
		theirs.Name, theirs.Source = long, "different"

		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(tpl, nil)
		d.custom.EXPECT().List(ctx, bob).Return([]domain.CustomNode{theirs}, nil)
		d.custom.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def custom.Definition) bool {
			return def.Name == strings.Repeat("a", 38)+"-2"
		})).Return(domain.CustomNode{ID: "n-2"}, nil)
		d.workflows.EXPECT().Create(ctx, bob, mock.Anything).Return(domain.Workflow{ID: "wf-bob"}, nil)

		_, err := s.Use(ctx, bob, "tpl-1", values)
		require.NoError(t, err)
	})

	t.Run("the copies go again when the workflow cannot be made", func(t *testing.T) {
		s, d := withCustom(t)
		refused := &domain.ValidationError{Problems: []domain.Problem{{Message: "no"}}}

		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(usingSplit(), nil)
		d.custom.EXPECT().List(ctx, bob).Return(nil, nil)
		d.custom.EXPECT().Create(ctx, bob, mock.Anything).Return(domain.CustomNode{ID: "n-bob", Name: "split"}, nil)
		d.workflows.EXPECT().Create(ctx, bob, mock.Anything).Return(domain.Workflow{}, refused)
		d.custom.EXPECT().Delete(mock.Anything, bob, domain.CustomNodeID("n-bob"), true).Return(nil).Once()

		_, err := s.Use(ctx, bob, "tpl-1", values)
		require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
	})

	t.Run("and when a later copy cannot be made, the earlier ones", func(t *testing.T) {
		s, d := withCustom(t)

		tpl := usingSplit()
		second := bundled()
		second.Name = "other"
		tpl.CustomNodes = append(tpl.CustomNodes, second)

		d.repo.EXPECT().Get(ctx, domain.TemplateID("tpl-1")).Return(tpl, nil)
		d.custom.EXPECT().List(ctx, bob).Return(nil, nil)
		d.custom.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def custom.Definition) bool { return def.Name == "split" })).
			Return(domain.CustomNode{ID: "n-1", Name: "split"}, nil)
		d.custom.EXPECT().Create(ctx, bob, mock.MatchedBy(func(def custom.Definition) bool { return def.Name == "other" })).
			Return(domain.CustomNode{}, domain.ErrCodeOff)
		d.custom.EXPECT().Delete(mock.Anything, bob, domain.CustomNodeID("n-1"), true).Return(errors.New("gone already")).Once()

		_, err := s.Use(ctx, bob, "tpl-1", values)
		require.ErrorIs(t, err, domain.ErrCodeOff)
		assert.ErrorContains(t, err, "its custom node other")
	})
}
