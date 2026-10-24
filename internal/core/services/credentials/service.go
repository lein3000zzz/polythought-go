// Package credentials is each user's vault: typed credentials, such as API keys, that
// nodes use on the user's behalf. Secret values are encrypted at rest, never returned to
// clients, and only ever handed to the node that uses them.
package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	maxName           = 64
	maxValue          = 4096
	defaultMaxPerUser = 100
	testTimeout       = 15 * time.Second
)

var _ ports.CredentialReader = (*Service)(nil)

// Types are the credential types the nodes declared.
type Types interface {
	CredentialType(name string) (engine.CredentialType, bool)
	CredentialTypes() []engine.CredentialType
}

type Service struct {
	types      Types
	repo       ports.CredentialRepository
	box        ports.SecretBox
	log        logging.Logger
	now        func() time.Time
	maxPerUser int
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(types Types, repo ports.CredentialRepository, box ports.SecretBox, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		types:      types,
		repo:       repo,
		box:        box,
		log:        log.With("component", "credentials"),
		now:        func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		maxPerUser: defaultMaxPerUser,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Input is a new credential.
type Input struct {
	Type   string
	Name   string
	Values map[string]string // by field name
}

// Change edits a credential. A value left out keeps what is saved, and so does an empty
// secret, so a form need not show secrets to save other changes.
type Change struct {
	Name   string
	Values map[string]string
}

// Types lists the kinds of credential users can create.
func (s *Service) Types() []engine.CredentialType {
	return s.types.CredentialTypes()
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]domain.Credential, error) {
	return s.repo.List(ctx, p.User.ID)
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (domain.Credential, error) {
	typ, ok := s.types.CredentialType(in.Type)
	if !ok {
		return domain.Credential{}, fmt.Errorf("%w: no credential type %q", domain.ErrInvalidInput, in.Type)
	}

	name, values, err := clean(typ, in.Name, in.Values)
	if err != nil {
		return domain.Credential{}, err
	}

	n, err := s.repo.CountForOwner(ctx, p.User.ID)
	if err != nil {
		return domain.Credential{}, err
	}

	if n >= s.maxPerUser {
		return domain.Credential{}, fmt.Errorf("%w: at most %d credentials per user", domain.ErrInvalidInput, s.maxPerUser)
	}

	now := s.now()
	c := domain.Credential{
		ID:        domain.CredentialID(uuid.Must(uuid.NewV7()).String()),
		OwnerID:   p.User.ID,
		Type:      typ.Name,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}

	sealed, err := s.seal(ctx, typ, &c, values)
	if err != nil {
		return domain.Credential{}, err
	}

	if err := s.repo.Create(ctx, sealed); err != nil {
		return domain.Credential{}, err
	}

	s.log.Info(ctx, "Credential created", "user_id", p.User.ID, "credential_id", c.ID, "type", c.Type)

	return c, nil
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id domain.CredentialID, ch Change) (domain.Credential, error) {
	stored, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Credential{}, err
	}

	typ, ok := s.types.CredentialType(stored.Credential.Type)
	if !ok {
		return domain.Credential{}, fmt.Errorf("%w: credential type %q is no longer available", domain.ErrInvalidInput, stored.Credential.Type)
	}

	current, err := s.open(ctx, stored)
	if err != nil {
		return domain.Credential{}, err
	}

	merged := maps.Clone(current)

	for name, v := range ch.Values {
		if f, ok := field(typ, name); ok && f.Secret && strings.TrimSpace(v) == "" {
			continue // kept
		}

		merged[name] = v
	}

	name, values, err := clean(typ, ch.Name, merged)
	if err != nil {
		return domain.Credential{}, err
	}

	// Where a secret goes, such as a base URL, is the user's to change, or their agents'
	// where they let them use credentials (D404): a new secret or name is anyone's.
	if !p.MayUseCredentials() {
		for _, f := range typ.Fields {
			if !f.Secret && values[f.Name] != current[f.Name] {
				return domain.Credential{}, fmt.Errorf("%w: changing %s needs the user to let agents and tokens use credentials: Settings, Connected agents",
					domain.ErrForbidden, f.Label)
			}
		}
	}

	c := stored.Credential
	c.Name = name
	c.UpdatedAt = s.now()

	if !maps.Equal(values, current) {
		// An earlier test said nothing about the new values.
		c.TestedAt, c.TestError = time.Time{}, ""
	}

	sealed, err := s.seal(ctx, typ, &c, values)
	if err != nil {
		return domain.Credential{}, err
	}

	if err := s.repo.Update(ctx, sealed); err != nil {
		return domain.Credential{}, err
	}

	s.log.Info(ctx, "Credential updated", "user_id", p.User.ID, "credential_id", c.ID)

	return c, nil
}

// Delete removes a credential. Workflows that use it fail until they get another.
func (s *Service) Delete(ctx context.Context, p auth.Principal, id domain.CredentialID) error {
	// A token or an agent deletes only where its user lets it (D404).
	if err := p.NeedDelete(); err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.log.Info(ctx, "Credential deleted", "user_id", p.User.ID, "credential_id", id)

	return nil
}

// Test tries a credential against its service and records the outcome on it. A failed
// test is not an error: the credential comes back with TestError set.
func (s *Service) Test(ctx context.Context, p auth.Principal, id domain.CredentialID) (domain.Credential, error) {
	// A test sends the secret, to where the credential says (D404).
	if !p.MayUseCredentials() {
		return domain.Credential{}, fmt.Errorf("%w: testing a credential needs the user to let agents and tokens use credentials: Settings, Connected agents",
			domain.ErrForbidden)
	}

	stored, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Credential{}, err
	}

	typ, ok := s.types.CredentialType(stored.Credential.Type)
	if !ok || typ.Test == nil {
		return domain.Credential{}, fmt.Errorf("%w: this kind of credential cannot be tested", domain.ErrInvalidInput)
	}

	values, err := s.open(ctx, stored)
	if err != nil {
		return domain.Credential{}, err
	}

	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	c := stored.Credential
	c.TestedAt, c.TestError = s.now(), ""

	if err := typ.Test(testCtx, values); err != nil {
		if ctx.Err() != nil {
			return domain.Credential{}, ctx.Err()
		}

		c.TestError = err.Error()
	}

	if err := s.repo.RecordTest(ctx, p.User.ID, id, c.TestedAt, c.TestError); err != nil {
		return domain.Credential{}, err
	}

	return c, nil
}

// Values returns every value of one of owner's credentials, for a node running on their
// behalf, and its type. A credential of none of the types is refused, so a node never
// gets a secret meant for a different service.
func (s *Service) Values(ctx context.Context, owner domain.UserID, id domain.CredentialID, types []string) (string, map[string]string, error) {
	stored, err := s.repo.Get(ctx, owner, id)
	if err != nil {
		return "", nil, err
	}

	c := stored.Credential
	if !slices.Contains(types, c.Type) {
		return "", nil, fmt.Errorf("%w: %q is a %s credential, not %s", domain.ErrInvalidInput, c.Name, c.Type, strings.Join(types, " or "))
	}

	values, err := s.open(ctx, stored)
	if err != nil {
		return "", nil, err
	}

	return c.Type, values, nil
}

func field(typ engine.CredentialType, name string) (engine.CredentialField, bool) {
	i := slices.IndexFunc(typ.Fields, func(f engine.CredentialField) bool { return f.Name == name })
	if i < 0 {
		return engine.CredentialField{}, false
	}

	return typ.Fields[i], true
}

// clean trims the name and values and checks them, reporting every problem at once:
// "name" or "values.<field>" in Param, or no Param for what the type's check finds.
func clean(typ engine.CredentialType, name string, values map[string]string) (string, map[string]string, error) {
	problems := domain.ValidationError{Base: domain.ErrInvalidInput}

	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxName {
		problems.Add(domain.Problem{Param: "name", Message: fmt.Sprintf("a name of 1 to %d characters", maxName)})
	}

	out := make(map[string]string, len(typ.Fields))

	for _, key := range slices.Sorted(maps.Keys(values)) {
		if _, ok := field(typ, key); !ok {
			problems.Add(domain.Problem{Param: "values." + key, Message: fmt.Sprintf("%s has no field %q", typ.Label, key)})
		}
	}

	for _, f := range typ.Fields {
		v := strings.TrimSpace(values[f.Name])

		switch {
		case v == "" && f.Required:
			problems.Add(domain.Problem{Param: "values." + f.Name, Message: f.Label + " is required"})
		case len(v) > maxValue:
			problems.Add(domain.Problem{Param: "values." + f.Name, Message: fmt.Sprintf("%s is longer than %d characters", f.Label, maxValue)})
		case v != "":
			out[f.Name] = v
		}
	}

	if len(problems.Problems) == 0 && typ.Check != nil {
		if err := typ.Check(out); err != nil {
			problems.Add(domain.Problem{Message: err.Error()})
		}
	}

	return name, out, problems.Err()
}

// seal puts the values that are not secret on c, in the clear, and seals the rest.
func (s *Service) seal(ctx context.Context, typ engine.CredentialType, c *domain.Credential, values map[string]string) (ports.SealedCredential, error) {
	c.Fields = make(map[string]string)
	c.Secrets = []string{}
	secrets := make(map[string]string)

	for _, f := range typ.Fields {
		v, ok := values[f.Name]

		switch {
		case !ok:
		case f.Secret:
			secrets[f.Name] = v
			c.Secrets = append(c.Secrets, f.Name)
		default:
			c.Fields[f.Name] = v
		}
	}

	out := ports.SealedCredential{Credential: *c}

	if len(secrets) == 0 {
		return out, nil
	}

	plain, err := json.Marshal(secrets)
	if err != nil {
		return ports.SealedCredential{}, err
	}

	if out.Sealed, err = s.box.Seal(ctx, plain, aad(c.ID)); err != nil {
		return ports.SealedCredential{}, err
	}

	return out, nil
}

// open returns every value of a stored credential.
func (s *Service) open(ctx context.Context, stored ports.SealedCredential) (map[string]string, error) {
	values := maps.Clone(stored.Credential.Fields)
	if values == nil {
		values = make(map[string]string)
	}

	if stored.Sealed == nil {
		return values, nil
	}

	plain, err := s.box.Open(ctx, stored.Sealed, aad(stored.Credential.ID))
	if err != nil {
		return nil, fmt.Errorf("credentials: open %s: %w", stored.Credential.ID, err)
	}

	var secrets map[string]string
	if err := json.Unmarshal(plain, &secrets); err != nil {
		return nil, fmt.Errorf("credentials: decode %s: %w", stored.Credential.ID, err)
	}

	maps.Copy(values, secrets)

	return values, nil
}

// aad binds sealed values to their credential, so they cannot be moved to another.
func aad(id domain.CredentialID) []byte {
	return []byte("credential:" + string(id))
}
