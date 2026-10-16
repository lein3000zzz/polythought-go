package library

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	custom "github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
)

// CustomNodes is what the library needs from the custom node service: the sharer's, to
// bundle, and the user's, to reuse or copy into (A3, D61).
type CustomNodes interface {
	List(ctx context.Context, p auth.Principal) ([]domain.CustomNode, error)
	Create(ctx context.Context, p auth.Principal, def custom.Definition) (domain.CustomNode, error)
	Delete(ctx context.Context, p auth.Principal, id domain.CustomNodeID, confirm bool) error
}

// WithCustomNodes bundles the custom nodes a shared workflow uses, and copies them for
// whoever uses it. Without it, a shared workflow brings none.
func WithCustomNodes(c CustomNodes) Option {
	return func(s *Service) { s.custom = c }
}

// maxName is the longest a custom node's name may be, which a copy's number fits in.
const maxName = 40

// bundle is the custom nodes of p's that nodes use, as definitions only.
func (s *Service) bundle(ctx context.Context, p auth.Principal, nodes []domain.NodeSpec) ([]domain.CustomNode, error) {
	if s.custom == nil {
		return nil, nil
	}

	mine, err := s.custom.List(ctx, p)
	if err != nil {
		return nil, err
	}

	var out []domain.CustomNode

	for _, n := range mine {
		if slices.ContainsFunc(nodes, func(spec domain.NodeSpec) bool { return spec.Type == engine.CustomTypePrefix+n.Name }) {
			out = append(out, definitionOf(n))
		}
	}

	return out, nil
}

func definitionOf(n domain.CustomNode) domain.CustomNode {
	return domain.CustomNode{
		Name: n.Name, Label: n.Label, Description: n.Description,
		Settings: n.Settings, Outputs: n.Outputs, Items: n.Items, Source: n.Source,
	}
}

// copyCustomNodes gives p the custom nodes a template brings (D61): one of theirs that
// is the same is used as it is; one that differs is copied under the next free name. It
// returns the types the template's nodes must name instead, and the copies it made, which
// it removes again should one fail.
func (s *Service) copyCustomNodes(
	ctx context.Context,
	p auth.Principal,
	bundled []domain.CustomNode,
) (map[string]string, []domain.CustomNodeID, error) {
	if len(bundled) == 0 {
		return nil, nil, nil
	}

	if s.custom == nil {
		return nil, nil, fmt.Errorf("%w: it brings custom nodes, which this server does not take", domain.ErrInvalidInput)
	}

	mine, err := s.custom.List(ctx, p)
	if err != nil {
		return nil, nil, err
	}

	byName := make(map[string]domain.CustomNode, len(mine))
	for _, n := range mine {
		byName[n.Name] = n
	}

	renamed := map[string]string{}

	var made []domain.CustomNodeID

	for _, def := range bundled {
		if same, ok := alike(def, byName); ok {
			if same != def.Name {
				renamed[engine.CustomTypePrefix+def.Name] = engine.CustomTypePrefix + same
			}

			continue
		}

		name := def.Name
		if _, taken := byName[name]; taken {
			name = freeName(def.Name, byName)
		}

		n, err := s.custom.Create(ctx, p, custom.Definition{
			Name: name, Label: def.Label, Description: def.Description,
			Settings: def.Settings, Outputs: def.Outputs, Items: def.Items, Source: def.Source,
		})
		if err != nil {
			s.removeCopies(ctx, p, made)
			return nil, nil, fmt.Errorf("its custom node %s: %w", def.Name, err)
		}

		made = append(made, n.ID)
		byName[name] = n

		if name != def.Name {
			renamed[engine.CustomTypePrefix+def.Name] = engine.CustomTypePrefix + name
		}
	}

	return renamed, made, nil
}

// removeCopies deletes copies a use that failed made. It is best effort: what it cannot
// delete stays on the user's Nodes page, to delete there.
func (s *Service) removeCopies(ctx context.Context, p auth.Principal, made []domain.CustomNodeID) {
	for _, id := range made {
		if err := s.custom.Delete(context.WithoutCancel(ctx), p, id, true); err != nil {
			s.log.Warn(ctx, "Copy of a custom node not removed", "user_id", p.User.ID, "custom_node_id", id, logging.Err(err))
		}
	}
}

// alike finds a node of mine that is the same as def: under its name, or a copy an
// earlier use made under its name and a number, the lowest first.
func alike(def domain.CustomNode, mine map[string]domain.CustomNode) (string, bool) {
	if have, ok := mine[def.Name]; ok && sameDefinition(have, def) {
		return def.Name, true
	}

	best, bestNumber := "", 0

	for name, have := range mine {
		number, isCopy := copyNumber(def.Name, name)
		if isCopy && sameDefinition(have, def) && (best == "" || number < bestNumber) {
			best, bestNumber = name, number
		}
	}

	return best, best != ""
}

// copyNumber says whether name is base with a number freeName adds, and which:
// spread-split-2 is spread-split's copy 2.
func copyNumber(base, name string) (int, bool) {
	i := strings.LastIndexByte(name, '-')
	if i < 0 || i == len(name)-1 || name[i+1] == '0' {
		return 0, false
	}

	n, err := strconv.Atoi(name[i+1:])
	if err != nil || n < 2 {
		return 0, false
	}

	return n, numbered(base, n) == name
}

// freeName is name with the lowest number from 2 that no node in taken has: spread-split-2.
func freeName(name string, taken map[string]domain.CustomNode) string {
	for i := 2; ; i++ {
		if _, ok := taken[numbered(name, i)]; !ok {
			return numbered(name, i)
		}
	}
}

// numbered is name with number n, shortened to fit within maxName.
func numbered(name string, n int) string {
	suffix := fmt.Sprintf("-%d", n)

	if len(name)+len(suffix) > maxName {
		name = strings.TrimRight(name[:maxName-len(suffix)], "-")
	}

	return name + suffix
}

// sameDefinition says whether a and b would make the same node: label, description,
// settings, outputs, items and code. A list left out is the same as an empty one.
func sameDefinition(a, b domain.CustomNode) bool {
	return a.Label == b.Label && a.Description == b.Description && a.Source == b.Source &&
		slices.Equal(a.Outputs, b.Outputs) && a.Items.Passed == b.Items.Passed &&
		slices.EqualFunc(a.Settings, b.Settings, sameSetting) &&
		slices.EqualFunc(a.Items.Fields, b.Items.Fields, sameField)
}

func sameSetting(a, b domain.CustomSetting) bool {
	return a.Name == b.Name && a.Label == b.Label && a.Description == b.Description && a.Kind == b.Kind &&
		a.Required == b.Required && reflect.DeepEqual(a.Default, b.Default) &&
		slices.Equal(a.Options, b.Options) && a.Field == b.Field && slices.Equal(a.FieldKinds, b.FieldKinds)
}

func sameField(a, b domain.CustomField) bool {
	if (a.Of == nil) != (b.Of == nil) || a.Of != nil && !sameField(*a.Of, *b.Of) {
		return false
	}

	return a.Name == b.Name && a.Description == b.Description && a.Kind == b.Kind &&
		a.Optional == b.Optional && a.Nullable == b.Nullable && slices.Equal(a.Options, b.Options) &&
		slices.EqualFunc(a.Fields, b.Fields, sameField)
}

// typesOf are the node types t's nodes use: the built-in ones, and the custom nodes it
// brings, as they are in it.
func (s *Service) typesOf(t domain.Template) NodeTypes {
	if len(t.CustomNodes) == 0 {
		return s.types
	}

	own := make(map[string]engine.NodeType, len(t.CustomNodes))
	for _, n := range t.CustomNodes {
		typ := customnodes.TypeOf(n)
		own[typ.Name] = typ
	}

	return bundledTypes{base: s.types, own: own}
}

type bundledTypes struct {
	base NodeTypes
	own  map[string]engine.NodeType
}

func (b bundledTypes) Type(name string) (engine.NodeType, bool, bool) {
	if typ, ok := b.own[name]; ok {
		return typ, false, true
	}

	return b.base.Type(name)
}
