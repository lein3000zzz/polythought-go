package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
)

// CredentialService is what the handlers need from the vault.
type CredentialService interface {
	Types() []engine.CredentialType
	List(ctx context.Context, p auth.Principal) ([]domain.Credential, error)
	Create(ctx context.Context, p auth.Principal, in credentials.Input) (domain.Credential, error)
	Update(ctx context.Context, p auth.Principal, id domain.CredentialID, ch credentials.Change) (domain.Credential, error)
	Delete(ctx context.Context, p auth.Principal, id domain.CredentialID) error
	Test(ctx context.Context, p auth.Principal, id domain.CredentialID) (domain.Credential, error)
}

type credentialFieldDTO struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty" validate:"optional"`
	Required    bool   `json:"required"`
	// Secret values are write-only: stored encrypted and never sent back.
	Secret bool `json:"secret"`
} // @name CredentialField

type credentialTypeDTO struct {
	Name        string               `json:"name"`
	Label       string               `json:"label"`
	Description string               `json:"description,omitempty" validate:"optional"`
	Fields      []credentialFieldDTO `json:"fields"`
	// Testable types can be tried against their service.
	Testable bool `json:"testable"`
} // @name CredentialType

type credentialTypeList struct {
	CredentialTypes []credentialTypeDTO `json:"credentialTypes"`
} // @name CredentialTypeList

type credentialDTO struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	// Values holds the values that are not secret, by field name.
	Values map[string]string `json:"values"`
	// Secrets names the secret values that are set; the values themselves are never sent.
	Secrets   []string  `json:"secrets"`
	CreatedAt time.Time `json:"createdAt" format:"date-time"`
	UpdatedAt time.Time `json:"updatedAt" format:"date-time"`
	// TestedAt is absent until the credential is tested. TestError says why the last test
	// failed, and is absent when it passed.
	TestedAt  *time.Time `json:"testedAt,omitempty" validate:"optional" format:"date-time"`
	TestError string     `json:"testError,omitempty" validate:"optional"`
} // @name Credential

type credentialList struct {
	Credentials []credentialDTO `json:"credentials"`
} // @name CredentialList

type createCredentialRequest struct {
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
} // @name CreateCredentialRequest

type updateCredentialRequest struct {
	Name string `json:"name"`
	// Values to change, by field name. A value left out keeps what is saved, and so does
	// an empty secret.
	Values map[string]string `json:"values" validate:"optional"`
} // @name UpdateCredentialRequest

func toCredentialDTO(c domain.Credential) credentialDTO {
	values := c.Fields
	if values == nil {
		values = map[string]string{}
	}

	secrets := c.Secrets
	if secrets == nil {
		secrets = []string{}
	}

	return credentialDTO{
		ID: string(c.ID), Type: c.Type, Name: c.Name, Values: values, Secrets: secrets,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		TestedAt: optionalTime(c.TestedAt), TestError: c.TestError,
	}
}

// listCredentialTypes lists the kinds of credential nodes can use.
//
//	@Summary	List credential types
//	@ID			listCredentialTypes
//	@Tags		credentials
//	@Success	200	{object}	credentialTypeList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/credential-types [get]
func (s *Server) listCredentialTypes(_ context.Context, c *app.RequestContext) {
	types := s.credentials.Types()

	out := credentialTypeList{CredentialTypes: make([]credentialTypeDTO, len(types))}

	for i, t := range types {
		d := credentialTypeDTO{
			Name: t.Name, Label: t.Label, Description: t.Description,
			Fields: make([]credentialFieldDTO, len(t.Fields)), Testable: t.Test != nil,
		}

		for j, f := range t.Fields {
			d.Fields[j] = credentialFieldDTO{Name: f.Name, Label: f.Label, Description: f.Description, Required: f.Required, Secret: f.Secret}
		}

		out.CredentialTypes[i] = d
	}

	c.JSON(http.StatusOK, out)
}

// listCredentials lists your credentials by name, without their secret values.
//
//	@Summary	List your credentials
//	@ID			listCredentials
//	@Tags		credentials
//	@Success	200	{object}	credentialList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/credentials [get]
func (s *Server) listCredentials(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.credentials.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := credentialList{Credentials: make([]credentialDTO, len(list))}
	for i, cred := range list {
		out.Credentials[i] = toCredentialDTO(cred)
	}

	c.JSON(http.StatusOK, out)
}

// createCredential adds a credential to your vault. Problems name the name ("name") or
// the field ("values.<field>") they are about.
//
//	@Summary	Create a credential
//	@ID			createCredential
//	@Tags		credentials
//	@Param		body	body		createCredentialRequest	true	"Credential"
//	@Success	201		{object}	credentialDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	409		{object}	errorBody	"credential_exists"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/credentials [post]
func (s *Server) createCredential(ctx context.Context, c *app.RequestContext) {
	var req createCredentialRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	cred, err := s.credentials.Create(ctx, p, credentials.Input{Type: req.Type, Name: req.Name, Values: req.Values})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toCredentialDTO(cred))
}

// updateCredential renames a credential or changes its values.
//
//	@Summary	Change a credential
//	@ID			updateCredential
//	@Tags		credentials
//	@Param		id		path		string					true	"Credential ID"
//	@Param		body	body		updateCredentialRequest	true	"Changes"
//	@Success	200		{object}	credentialDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"credential_exists"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/credentials/{id} [patch]
func (s *Server) updateCredential(ctx context.Context, c *app.RequestContext) {
	var req updateCredentialRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	cred, err := s.credentials.Update(ctx, p, domain.CredentialID(c.Param("id")), credentials.Change{Name: req.Name, Values: req.Values})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCredentialDTO(cred))
}

// deleteCredential removes a credential. Workflows that use it fail until they get
// another.
//
//	@Summary	Delete a credential
//	@ID			deleteCredential
//	@Tags		credentials
//	@Param		id	path	string	true	"Credential ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/credentials/{id} [delete]
func (s *Server) deleteCredential(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.credentials.Delete(ctx, p, domain.CredentialID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// testCredential tries a credential against its service. A failed test is still a 200:
// the credential comes back with testError set.
//
//	@Summary	Test a credential
//	@ID			testCredential
//	@Tags		credentials
//	@Param		id	path		string	true	"Credential ID"
//	@Success	200	{object}	credentialDTO
//	@Failure	400	{object}	errorBody	"invalid_input: this type cannot be tested"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/credentials/{id}/test [post]
func (s *Server) testCredential(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	cred, err := s.credentials.Test(ctx, p, domain.CredentialID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCredentialDTO(cred))
}
