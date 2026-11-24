package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const ReadType = "data.read"

// How Read dataset reads: a row for each item, by its key, or every row.
const (
	ReadByKey = "by key"
	ReadEvery = "every row"
)

// MissingPort is where an item goes whose key the dataset has no row for.
const MissingPort = "missing"

// MaxRows is the most rows Read dataset sends on at once.
const MaxRows = 1000

var _ engine.Node = (*Read)(nil)

// Read reads one of the owner's datasets: for each item, the row whose key is the item's,
// put on it, those with none out of missing; or every row, the newest so many, an item each.
type Read struct {
	store ports.DatasetStore
}

func NewRead(store ports.DatasetStore) *Read {
	return &Read{store: store}
}

func (*Read) Type() engine.NodeType {
	return engine.NodeType{
		Name:  ReadType,
		Label: "Read dataset",
		Description: "Reads one of your datasets: for each item, the row whose key is the item's, put on it, items with " +
			"none going out of missing; or every row, the newest 1,000 at most, an item each.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, MissingPort},
		Reads:   true,
		Items: &engine.Items{
			Passed: true, Named: "field", Alone: engine.Choice{Param: "read", Value: ReadEvery},
			Fields: []engine.Field{{
				Name: "row", Kind: engine.FieldAny, Description: "The row: an object of the dataset's fields.",
			}},
		},
		Params: []engine.ParamSpec{
			datasetParam("The dataset read: one of yours."),
			{
				Name: "read", Label: "Read", Kind: engine.ParamString, Options: []string{ReadByKey, ReadEvery}, Default: ReadByKey,
				Description: "By key: for each item, the row last added with its key. Every row: the dataset's rows, an item " +
					"each, oldest first.",
			},
			{
				Name: "key", Label: "Key", Kind: engine.ParamString, Numbers: true,
				Description: "By key: each item's key, like {{ city }}, as the dataset's key field has it.",
			},
			{
				Name: "limit", Label: "Rows", Kind: engine.ParamNumber, Default: MaxRows, Check: checkLimit,
				Description: "Every row: the newest so many, up to 1,000.",
			},
			{Name: "field", Label: "Row field", Kind: engine.ParamString, Default: "row", Description: "Where the row goes on the item."},
		},
		Check: checkRead,
	}
}

func checkLimit(v any) error {
	n, err := engine.Params{"limit": v}.Int("limit")
	if err == nil && (n < 1 || n > MaxRows) {
		return fmt.Errorf("from 1 to %d", MaxRows)
	}

	return nil
}

func checkRead(p engine.Params) error {
	if read, _ := p.String("read"); read != ReadByKey {
		return nil
	}

	if key, _ := p.Text("key"); strings.TrimSpace(key) == "" {
		return errors.New("by key, it needs a key, like {{ city }}, to look each item's row up by")
	}

	return nil
}

func (n *Read) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return engine.Ports{}, nil
	}

	first := engine.ItemRef{Port: engine.DefaultPort}

	p, err := rc.Params.Resolve(first)
	if err != nil {
		return nil, err
	}

	name, err := p.String("dataset")
	if err != nil {
		return nil, err
	}

	// Gone, or kept from its owner: the run fails at once.
	d, err := n.store.Get(ctx, rc.OwnerID, name)
	if err != nil {
		return nil, err
	}

	if read, _ := p.String("read"); read == ReadEvery {
		return n.every(ctx, d, p, len(items))
	}

	if d.KeyField == "" {
		return nil, fmt.Errorf("%w: the dataset %q has no key field to look rows up by: read every row instead",
			engine.ErrInvalidParam, d.Name)
	}

	return n.byKey(ctx, rc, d, items)
}

// every sends on the newest rows, an item each, together for every item received.
func (n *Read) every(ctx context.Context, d domain.Dataset, p engine.Params, received int) (engine.Ports, error) {
	limit, err := p.Int("limit") // from 1 to MaxRows, as its check has it
	if err != nil {
		return nil, err
	}

	field, err := p.String("field")
	if err != nil {
		return nil, err
	}

	rows, err := n.store.Newest(ctx, d, limit)
	if err != nil {
		return nil, err
	}

	srcs := make([]engine.ItemRef, received)
	for i := range srcs {
		srcs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
	}

	out := engine.Ports{}

	for _, r := range rows {
		row, err := decode(r.Row)
		if err != nil {
			return nil, err
		}

		out.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{field: row}, Source: srcs})
	}

	return out, nil
}

// byKey puts on each item the row last added with its key, or sends it out of missing.
func (n *Read) byKey(ctx context.Context, rc engine.RunContext, d domain.Dataset, items []engine.Item) (engine.Ports, error) {
	type found struct {
		row     map[string]any
		missing bool
		err     error
	}

	out := engine.Ports{}
	looked := map[string]found{}

	for i, item := range items {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		field, err := p.String("field")
		if err != nil {
			return nil, err
		}

		key, err := p.Text("key")
		if err == nil && strings.TrimSpace(key) == "" {
			err = fmt.Errorf("%w %q: empty for this item", engine.ErrInvalidParam, "key")
		}

		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		f, ok := looked[key]
		if !ok {
			row, err := n.store.Latest(ctx, d, key)

			switch {
			case errors.Is(err, domain.ErrNotFound):
				f.missing = true
			case err != nil && ctx.Err() != nil:
				return nil, ctx.Err()
			case err != nil:
				f.err = err
			default:
				f.row, f.err = decode(row.Row)
			}

			looked[key] = f
		}

		switch {
		case f.err != nil:
			out.Fail(item, src, f.err)
		case f.missing:
			out.Add(MissingPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
		default:
			j := maps.Clone(item.JSON)
			if j == nil {
				j = map[string]any{}
			}

			j[field] = f.row
			out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

// decode is a stored row as an item holds it, its numbers exact.
func decode(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var row map[string]any
	if err := dec.Decode(&row); err != nil {
		return nil, fmt.Errorf("a stored row is not an object: %w", err)
	}

	return row, nil
}
