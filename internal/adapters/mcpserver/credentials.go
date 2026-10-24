package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
)

// The tools for the user's credentials (D403, D404): listed by name and kind for picking
// in nodes, made and changed with secrets the user gave the agent, sealed and never given
// back, tested, and deleted where the user lets agents.

// Credentials is the user's vault: the credentials service.
type Credentials interface {
	Types() []engine.CredentialType
	List(ctx context.Context, p auth.Principal) ([]domain.Credential, error)
	Create(ctx context.Context, p auth.Principal, in credentials.Input) (domain.Credential, error)
	Update(ctx context.Context, p auth.Principal, id domain.CredentialID, ch credentials.Change) (domain.Credential, error)
	Delete(ctx context.Context, p auth.Principal, id domain.CredentialID) error
	Test(ctx context.Context, p auth.Principal, id domain.CredentialID) (domain.Credential, error)
}

type credentialFieldOut struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty" jsonschema:"sealed once given, and never given back"`
}

type credentialTypeOut struct {
	Name        string               `json:"name"`
	Label       string               `json:"label"`
	Description string               `json:"description,omitempty"`
	Fields      []credentialFieldOut `json:"fields"`
	Testable    bool                 `json:"testable,omitempty" jsonschema:"test_credential tries it against its service"`
}

type credentialTypeList struct {
	Types []credentialTypeOut `json:"types"`
}

type credentialOut struct {
	ID        string            `json:"id" jsonschema:"what a node's credential setting takes"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Fields    map[string]string `json:"fields,omitempty" jsonschema:"its values that are not secret"`
	Secrets   []string          `json:"secrets,omitempty" jsonschema:"the secret fields set, never their values"`
	TestedAt  *time.Time        `json:"testedAt,omitempty"`
	TestError string            `json:"testError,omitempty" jsonschema:"why its last test failed; absent when it passed"`
}

type credentialList struct {
	Credentials []credentialOut `json:"credentials"`
}

type createCredentialIn struct {
	Type   string            `json:"type" jsonschema:"its kind, as list_credential_types has them"`
	Name   string            `json:"name" jsonschema:"what the user knows it by, 1 to 64 characters"`
	Values map[string]string `json:"values" jsonschema:"by field name; the secret ones as the user gave them, sealed and never given back"`
}

type updateCredentialIn struct {
	ID     string            `json:"id" jsonschema:"the credential's ID"`
	Name   string            `json:"name,omitempty" jsonschema:"a new name; the one it has unless said"`
	Values map[string]string `json:"values,omitempty" jsonschema:"the fields to change, by name; a secret left out or empty keeps the one sealed"`
}

type credentialIn struct {
	ID string `json:"id" jsonschema:"the credential's ID"`
}

func (s *Server) addCredentialTools(srv *mcp.Server) {
	add(s, srv, Tool[struct{}, credentialTypeList]{
		Name: "list_credential_types", Title: "List credential types", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The kinds of credential the user can keep, such as an HTTP header or a model provider's key, with their fields. " +
			"get_node_type says which kinds a node's credential setting takes.",
		Handle: s.listCredentialTypes,
	})

	add(s, srv, Tool[struct{}, credentialList]{
		Name: "list_credentials", Title: "List credentials", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The user's credentials, by name and kind, to pick in a node's credential setting by ID. Their secrets are never given. " +
			"A workflow that uses one is saved and run by agents only where the user lets them use credentials.",
		Handle: s.listCredentials,
	})

	add(s, srv, Tool[createCredentialIn, credentialOut]{
		Name: "create_credential", Title: "Create a credential", Scope: domain.ScopeBuild,
		Description: "Keep a credential the user gave you, such as an API key, for their nodes to use: its secrets are sealed " +
			"and never given back, to you or anyone. Ask the user for the secret; never make one up.",
		Handle: s.createCredential,
	})

	add(s, srv, Tool[updateCredentialIn, credentialOut]{
		Name: "update_credential", Title: "Change a credential", Scope: domain.ScopeBuild,
		Description: "Rename a credential, give it a new secret the user gave you, or change its other fields. Changing those, such as " +
			"a base URL, sends its secret somewhere new, and needs the user to let agents use credentials.",
		Handle: s.updateCredential,
	})

	add(s, srv, Tool[credentialIn, credentialOut]{
		Name: "test_credential", Title: "Test a credential", Scope: domain.ScopeBuild,
		Description: "Try a credential against its service, as Test on Settings does, and keep the outcome: testError says why it " +
			"failed. It calls the service with the user's secret, where the user lets agents use credentials.",
		Handle: s.testCredential,
	})

	add(s, srv, Tool[credentialIn, deletedOut]{
		Name: "delete_credential", Title: "Delete a credential", Scope: domain.ScopeBuild, Destructive: true,
		Description: "Delete a credential: its secret is gone, and workflows that use it fail until they get another. It cannot be " +
			"undone, and works only where the user lets agents delete.",
		Handle: s.deleteCredential,
	})
}

func (s *Server) listCredentialTypes(_ context.Context, _ auth.Principal, _ struct{}) (credentialTypeList, error) {
	types := s.credentials.Types()
	out := credentialTypeList{Types: make([]credentialTypeOut, len(types))}

	for i, t := range types {
		o := credentialTypeOut{Name: t.Name, Label: t.Label, Description: t.Description, Testable: t.Test != nil, Fields: make([]credentialFieldOut, len(t.Fields))}
		for j, f := range t.Fields {
			o.Fields[j] = credentialFieldOut{Name: f.Name, Label: f.Label, Description: f.Description, Required: f.Required, Secret: f.Secret}
		}

		out.Types[i] = o
	}

	return out, nil
}

func (s *Server) listCredentials(ctx context.Context, p auth.Principal, _ struct{}) (credentialList, error) {
	list, err := s.credentials.List(ctx, p)
	if err != nil {
		return credentialList{}, err
	}

	out := credentialList{Credentials: make([]credentialOut, len(list))}
	for i, c := range list {
		out.Credentials[i] = credentialOf(c)
	}

	return out, nil
}

func (s *Server) createCredential(ctx context.Context, p auth.Principal, in createCredentialIn) (credentialOut, error) {
	c, err := s.credentials.Create(ctx, p, credentials.Input{Type: in.Type, Name: in.Name, Values: in.Values})
	if err != nil {
		return credentialOut{}, err
	}

	return credentialOf(c), nil
}

func (s *Server) updateCredential(ctx context.Context, p auth.Principal, in updateCredentialIn) (credentialOut, error) {
	name := strings.TrimSpace(in.Name)

	if name == "" {
		list, err := s.credentials.List(ctx, p)
		if err != nil {
			return credentialOut{}, err
		}

		i := slices.IndexFunc(list, func(c domain.Credential) bool { return string(c.ID) == in.ID })
		if i < 0 {
			return credentialOut{}, fmt.Errorf("%w: no credential %q; list_credentials lists them", domain.ErrNotFound, in.ID)
		}

		name = list[i].Name
	}

	c, err := s.credentials.Update(ctx, p, domain.CredentialID(in.ID), credentials.Change{Name: name, Values: in.Values})
	if err != nil {
		return credentialOut{}, err
	}

	return credentialOf(c), nil
}

func (s *Server) testCredential(ctx context.Context, p auth.Principal, in credentialIn) (credentialOut, error) {
	c, err := s.credentials.Test(ctx, p, domain.CredentialID(in.ID))
	if err != nil {
		return credentialOut{}, err
	}

	return credentialOf(c), nil
}

func (s *Server) deleteCredential(ctx context.Context, p auth.Principal, in credentialIn) (deletedOut, error) {
	if err := s.credentials.Delete(ctx, p, domain.CredentialID(in.ID)); err != nil {
		return deletedOut{}, err
	}

	return deletedOut{Deleted: in.ID}, nil
}

// credentialOf is a credential as an agent sees it: never a secret.
func credentialOf(c domain.Credential) credentialOut {
	return credentialOut{
		ID: string(c.ID), Name: c.Name, Type: c.Type, Fields: c.Fields, Secrets: c.Secrets, TestedAt: optionalTime(c.TestedAt),
		TestError: c.TestError,
	}
}
