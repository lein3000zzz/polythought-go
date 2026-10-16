package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.CustomNodeRepository = (*CustomNodes)(nil)

const customNodesNameKey = "custom_nodes_owner_id_name_key"

type CustomNodes struct {
	db *DB
}

func NewCustomNodes(db *DB) *CustomNodes {
	return &CustomNodes{db: db}
}

func (r *CustomNodes) Create(ctx context.Context, n domain.CustomNode) error {
	m, err := toCustomNodeModel(n)
	if err != nil {
		return err
	}

	_, err = table.CustomNodes.INSERT(table.CustomNodes.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))
	if isUniqueViolation(err, customNodesNameKey) {
		return domain.ErrCustomNodeTaken
	}

	return err
}

func (r *CustomNodes) Update(ctx context.Context, n domain.CustomNode, expectedVersion int) error {
	where, err := ownedCustomNode(n.OwnerID, n.ID)
	if err != nil {
		return err
	}

	m, err := toCustomNodeModel(n)
	if err != nil {
		return err
	}

	t := table.CustomNodes

	res, err := t.UPDATE(t.Label, t.Description, t.Settings, t.Outputs, t.Items, t.Source, t.Version, t.UpdatedAt).
		MODEL(m).
		WHERE(where.AND(t.Version.EQ(jet.Int(int64(expectedVersion))))).
		ExecContext(ctx, r.db.conn(ctx))

	if err := affectedOne(res, err); !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	// Nothing matched: either the node is gone or someone saved first.
	if _, getErr := r.Get(ctx, n.OwnerID, n.ID); getErr == nil {
		return domain.ErrConflict
	}

	return domain.ErrNotFound
}

func (r *CustomNodes) Get(ctx context.Context, owner domain.UserID, id domain.CustomNodeID) (domain.CustomNode, error) {
	where, err := ownedCustomNode(owner, id)
	if err != nil {
		return domain.CustomNode{}, err
	}

	var m model.CustomNodes

	err = jet.SELECT(table.CustomNodes.AllColumns).FROM(table.CustomNodes).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.CustomNode{}, notFound(err)
	}

	return toCustomNode(m)
}

func (r *CustomNodes) List(ctx context.Context, owner domain.UserID) ([]domain.CustomNode, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	var rows []model.CustomNodes

	err = jet.SELECT(table.CustomNodes.AllColumns).
		FROM(table.CustomNodes).
		WHERE(table.CustomNodes.OwnerID.EQ(jet.UUID(oid))).
		ORDER_BY(table.CustomNodes.Name.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.CustomNode, len(rows))

	for i, m := range rows {
		if out[i], err = toCustomNode(m); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func (r *CustomNodes) CountForOwner(ctx context.Context, owner domain.UserID) (int, error) {
	oid, err := parseID(owner)
	if err != nil {
		return 0, err
	}

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(table.CustomNodes).
		WHERE(table.CustomNodes.OwnerID.EQ(jet.UUID(oid))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *CustomNodes) Delete(ctx context.Context, owner domain.UserID, id domain.CustomNodeID) error {
	where, err := ownedCustomNode(owner, id)
	if err != nil {
		return err
	}

	return affectedOne(table.CustomNodes.DELETE().WHERE(where).ExecContext(ctx, r.db.conn(ctx)))
}

// ownedCustomNode matches one custom node of owner's: someone else's is not found.
func ownedCustomNode(owner domain.UserID, id domain.CustomNodeID) (jet.BoolExpression, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	nid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	return table.CustomNodes.ID.EQ(jet.UUID(nid)).AND(table.CustomNodes.OwnerID.EQ(jet.UUID(oid))), nil
}

// The documents a custom node's settings and items are kept as.
type (
	settingDoc struct {
		Name        string   `json:"name"`
		Label       string   `json:"label,omitempty"`
		Description string   `json:"description,omitempty"`
		Kind        string   `json:"kind"`
		Required    bool     `json:"required,omitempty"`
		Default     any      `json:"default,omitempty"`
		Options     []string `json:"options,omitempty"`
		Field       bool     `json:"field,omitempty"`
		FieldKinds  []string `json:"fieldKinds,omitempty"`
	}

	itemsDoc struct {
		Passed bool       `json:"passed,omitempty"`
		Fields []fieldDoc `json:"fields"`
	}

	fieldDoc struct {
		Name        string     `json:"name,omitempty"`
		Description string     `json:"description,omitempty"`
		Kind        string     `json:"kind"`
		Optional    bool       `json:"optional,omitempty"`
		Nullable    bool       `json:"nullable,omitempty"`
		Options     []string   `json:"options,omitempty"`
		Of          *fieldDoc  `json:"of,omitempty"`
		Fields      []fieldDoc `json:"fields,omitempty"`
	}
)

func toCustomNodeModel(n domain.CustomNode) (model.CustomNodes, error) {
	id, err := parseID(n.ID)
	if err != nil {
		return model.CustomNodes{}, err
	}

	owner, err := parseID(n.OwnerID)
	if err != nil {
		return model.CustomNodes{}, err
	}

	settings := make([]settingDoc, len(n.Settings))
	for i, s := range n.Settings {
		settings[i] = settingDoc{
			Name: s.Name, Label: s.Label, Description: s.Description, Kind: s.Kind, Required: s.Required,
			Default: s.Default, Options: s.Options, Field: s.Field, FieldKinds: s.FieldKinds,
		}
	}

	outputs := n.Outputs
	if outputs == nil {
		outputs = []string{}
	}

	var docs [3][]byte

	for i, v := range []any{settings, outputs, itemsDoc{Passed: n.Items.Passed, Fields: fieldDocs(n.Items.Fields)}} {
		if docs[i], err = json.Marshal(v); err != nil {
			return model.CustomNodes{}, fmt.Errorf("postgres: encode custom node: %w", err)
		}
	}

	return model.CustomNodes{
		ID: id, OwnerID: owner, Name: n.Name, Label: n.Label, Description: n.Description,
		Settings: string(docs[0]), Outputs: string(docs[1]), Items: string(docs[2]),
		Source: n.Source, Version: int32(n.Version), CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, //nolint:gosec // versions stay far below 2^31
	}, nil
}

func fieldDocs(fields []domain.CustomField) []fieldDoc {
	out := make([]fieldDoc, len(fields))

	for i, f := range fields {
		out[i] = fieldDoc{
			Name: f.Name, Description: f.Description, Kind: f.Kind, Optional: f.Optional, Nullable: f.Nullable,
			Options: f.Options, Fields: fieldDocs(f.Fields),
		}

		if f.Of != nil {
			of := fieldDocs([]domain.CustomField{*f.Of})[0]
			out[i].Of = &of
		}
	}

	return out
}

// toCustomNode keeps numbers in defaults as json.Number, so a decimal like 0.02 stays exact.
func toCustomNode(m model.CustomNodes) (domain.CustomNode, error) {
	var (
		settings []settingDoc
		outputs  []string
		items    itemsDoc
	)

	for _, d := range []struct {
		raw string
		v   any
	}{{m.Settings, &settings}, {m.Outputs, &outputs}, {m.Items, &items}} {
		dec := json.NewDecoder(bytes.NewReader([]byte(d.raw)))
		dec.UseNumber()

		if err := dec.Decode(d.v); err != nil {
			return domain.CustomNode{}, fmt.Errorf("postgres: decode custom node %s: %w", m.ID, err)
		}
	}

	n := domain.CustomNode{
		ID: domain.CustomNodeID(m.ID.String()), OwnerID: domain.UserID(m.OwnerID.String()),
		Name: m.Name, Label: m.Label, Description: m.Description,
		Settings: make([]domain.CustomSetting, len(settings)), Outputs: outputs,
		Items:  domain.CustomItems{Passed: items.Passed, Fields: customFields(items.Fields)},
		Source: m.Source, Version: int(m.Version), CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC(),
	}

	for i, s := range settings {
		n.Settings[i] = domain.CustomSetting{
			Name: s.Name, Label: s.Label, Description: s.Description, Kind: s.Kind, Required: s.Required,
			Default: s.Default, Options: s.Options, Field: s.Field, FieldKinds: s.FieldKinds,
		}
	}

	return n, nil
}

func customFields(docs []fieldDoc) []domain.CustomField {
	out := make([]domain.CustomField, len(docs))

	for i, d := range docs {
		out[i] = domain.CustomField{
			Name: d.Name, Description: d.Description, Kind: d.Kind, Optional: d.Optional, Nullable: d.Nullable,
			Options: d.Options, Fields: customFields(d.Fields),
		}

		if d.Of != nil {
			of := customFields([]fieldDoc{*d.Of})[0]
			out[i].Of = &of
		}
	}

	return out
}
