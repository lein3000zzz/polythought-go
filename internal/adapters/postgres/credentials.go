package postgres

import (
	"context"
	"encoding/json"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.CredentialRepository = (*Credentials)(nil)

const credentialsNameKey = "credentials_owner_id_name_key"

type Credentials struct {
	db *DB
}

func NewCredentials(db *DB) *Credentials {
	return &Credentials{db: db}
}

func (r *Credentials) Create(ctx context.Context, c ports.SealedCredential) error {
	m, err := toCredentialModel(c)
	if err != nil {
		return err
	}

	_, err = table.Credentials.INSERT(table.Credentials.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx))

	return credentialErr(err)
}

func (r *Credentials) Update(ctx context.Context, c ports.SealedCredential) error {
	m, err := toCredentialModel(c)
	if err != nil {
		return err
	}

	ct := table.Credentials

	return credentialErr(affectedOne(ct.UPDATE(ct.Name, ct.Fields, ct.Secrets, ct.Sealed, ct.UpdatedAt, ct.TestedAt, ct.TestError).
		MODEL(m).
		WHERE(ct.ID.EQ(jet.UUID(m.ID)).AND(ct.OwnerID.EQ(jet.UUID(m.OwnerID)))).
		ExecContext(ctx, r.db.conn(ctx))))
}

func credentialErr(err error) error {
	if isUniqueViolation(err, credentialsNameKey) {
		return domain.ErrCredentialExists
	}

	return err
}

func (r *Credentials) Get(ctx context.Context, owner domain.UserID, id domain.CredentialID) (ports.SealedCredential, error) {
	where, err := ownedCredential(owner, id)
	if err != nil {
		return ports.SealedCredential{}, err
	}

	var m model.Credentials

	err = jet.SELECT(table.Credentials.AllColumns).FROM(table.Credentials).WHERE(where).QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return ports.SealedCredential{}, notFound(err)
	}

	return toSealedCredential(m)
}

func (r *Credentials) List(ctx context.Context, owner domain.UserID) ([]domain.Credential, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	var rows []model.Credentials

	err = jet.SELECT(table.Credentials.AllColumns).
		FROM(table.Credentials).
		WHERE(table.Credentials.OwnerID.EQ(jet.UUID(oid))).
		ORDER_BY(table.Credentials.Name.ASC(), table.Credentials.ID.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Credential, len(rows))

	for i, m := range rows {
		c, err := toSealedCredential(m)
		if err != nil {
			return nil, err
		}

		out[i] = c.Credential
	}

	return out, nil
}

func (r *Credentials) CountForOwner(ctx context.Context, owner domain.UserID) (int, error) {
	oid, err := parseID(owner)
	if err != nil {
		return 0, err
	}

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(table.Credentials).
		WHERE(table.Credentials.OwnerID.EQ(jet.UUID(oid))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *Credentials) Delete(ctx context.Context, owner domain.UserID, id domain.CredentialID) error {
	where, err := ownedCredential(owner, id)
	if err != nil {
		return err
	}

	return affectedOne(table.Credentials.DELETE().WHERE(where).ExecContext(ctx, r.db.conn(ctx)))
}

func (r *Credentials) RecordTest(ctx context.Context, owner domain.UserID, id domain.CredentialID, at time.Time, testErr string) error {
	where, err := ownedCredential(owner, id)
	if err != nil {
		return err
	}

	ct := table.Credentials

	return affectedOne(ct.UPDATE(ct.TestedAt, ct.TestError).
		SET(jet.TimestampzT(at), jet.String(testErr)).
		WHERE(where).
		ExecContext(ctx, r.db.conn(ctx)))
}

// ownedCredential matches one credential of owner's: someone else's is not found.
func ownedCredential(owner domain.UserID, id domain.CredentialID) (jet.BoolExpression, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	cid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	return table.Credentials.ID.EQ(jet.UUID(cid)).AND(table.Credentials.OwnerID.EQ(jet.UUID(oid))), nil
}

func toCredentialModel(s ports.SealedCredential) (model.Credentials, error) {
	c := s.Credential

	id, err := parseID(c.ID)
	if err != nil {
		return model.Credentials{}, err
	}

	owner, err := parseID(c.OwnerID)
	if err != nil {
		return model.Credentials{}, err
	}

	fields := c.Fields
	if fields == nil {
		fields = map[string]string{}
	}

	secrets := c.Secrets
	if secrets == nil {
		secrets = []string{}
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return model.Credentials{}, err
	}

	secretsJSON, err := json.Marshal(secrets)
	if err != nil {
		return model.Credentials{}, err
	}

	m := model.Credentials{
		ID: id, OwnerID: owner, Type: c.Type, Name: c.Name,
		Fields: string(fieldsJSON), Secrets: string(secretsJSON),
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		TestedAt: timePtr(c.TestedAt), TestError: c.TestError,
	}

	if s.Sealed != nil {
		m.Sealed = &s.Sealed
	}

	return m, nil
}

func toSealedCredential(m model.Credentials) (ports.SealedCredential, error) {
	c := domain.Credential{
		ID: domain.CredentialID(m.ID.String()), OwnerID: domain.UserID(m.OwnerID.String()),
		Type: m.Type, Name: m.Name,
		CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC(),
		TestedAt: timeOf(m.TestedAt), TestError: m.TestError,
	}

	if err := json.Unmarshal([]byte(m.Fields), &c.Fields); err != nil {
		return ports.SealedCredential{}, err
	}

	if err := json.Unmarshal([]byte(m.Secrets), &c.Secrets); err != nil {
		return ports.SealedCredential{}, err
	}

	out := ports.SealedCredential{Credential: c}
	if m.Sealed != nil {
		out.Sealed = *m.Sealed
	}

	return out, nil
}
