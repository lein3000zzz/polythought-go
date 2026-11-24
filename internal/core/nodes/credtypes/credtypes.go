// Package credtypes declares the built-in credential types: what users keep in their
// vault for the built-in nodes to use.
package credtypes

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// HTTPHeader is one header a request carries, such as Authorization.
const HTTPHeader = "http.header"

// reservedHeaders describe the request itself; the sender sets them.
var reservedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Content-Type": true, "Transfer-Encoding": true,
	"Connection": true, "Keep-Alive": true, "Upgrade": true, "Te": true, "Trailer": true,
}

func HTTPHeaderType() engine.CredentialType {
	return engine.CredentialType{
		Name:  HTTPHeader,
		Label: "HTTP header",
		Description: "A header sent with each request, for services that want a key or token, " +
			"e.g. Authorization: Bearer <token>.",
		Fields: []engine.CredentialField{
			{Name: "header", Label: "Header", Required: true, Description: "Its name, e.g. Authorization or X-Api-Key."},
			{Name: "value", Label: "Value", Required: true, Secret: true, Description: "e.g. Bearer <token>."},
		},
		Check: func(values map[string]string) error { return CheckHeader(values["header"], values["value"]) },
	}
}

// The other ways a request authenticates (D438), for the HTTP request node.
const (
	HTTPBearer = "http.bearer"
	HTTPBasic  = "http.basic"
	HTTPQuery  = "http.query"
)

// HTTPTypes are every credential type an HTTP request takes.
var HTTPTypes = []string{HTTPHeader, HTTPBearer, HTTPBasic, HTTPQuery}

func HTTPBearerType() engine.CredentialType {
	return engine.CredentialType{
		Name:        HTTPBearer,
		Label:       "Bearer token",
		Description: "A token sent as Authorization: Bearer <token>, as most APIs take one.",
		Fields: []engine.CredentialField{
			{Name: "token", Label: "Token", Required: true, Secret: true, Description: "Without Bearer before it."},
		},
		Check: func(values map[string]string) error { return checkText("the token", values["token"]) },
	}
}

func HTTPBasicType() engine.CredentialType {
	return engine.CredentialType{
		Name:        HTTPBasic,
		Label:       "Username and password",
		Description: "Sent as HTTP basic authentication, which some APIs take, a key as the username for some.",
		Fields: []engine.CredentialField{
			{Name: "username", Label: "Username", Required: true},
			{Name: "password", Label: "Password", Secret: true, Description: "Empty, if the service wants none."},
		},
		Check: func(values map[string]string) error {
			if strings.Contains(values["username"], ":") {
				return errors.New("the username cannot contain a colon")
			}

			return errors.Join(checkText("the username", values["username"]), checkText("the password", values["password"]))
		},
	}
}

func HTTPQueryType() engine.CredentialType {
	return engine.CredentialType{
		Name:        HTTPQuery,
		Label:       "Key in the URL",
		Description: "A key some APIs take in the address, e.g. ?apikey=<key>, added to each request's URL.",
		Fields: []engine.CredentialField{
			{Name: "name", Label: "Name", Required: true, Description: "The query parameter's name, e.g. apikey or appid."},
			{Name: "value", Label: "Value", Required: true, Secret: true},
		},
		Check: func(values map[string]string) error {
			return errors.Join(checkText("the name", values["name"]), checkText("the value", values["value"]))
		},
	}
}

// checkText reports whether s is one line of text.
func checkText(what, s string) error {
	if strings.ContainsFunc(s, func(r rune) bool { return r < ' ' && r != '\t' || r == 0x7f }) {
		return fmt.Errorf("%s cannot contain line breaks or control characters", what)
	}

	return nil
}

// CheckHeader reports whether name and value make a header Polythought may send.
func CheckHeader(name, value string) error {
	if name == "" || strings.IndexFunc(name, func(r rune) bool { return !isTokenChar(r) }) >= 0 {
		return fmt.Errorf("%q is not a header name: use letters, digits and dashes", name)
	}

	if reservedHeaders[textproto.CanonicalMIMEHeaderKey(name)] {
		return fmt.Errorf("%s is set by Polythought itself", textproto.CanonicalMIMEHeaderKey(name))
	}

	if strings.ContainsFunc(value, func(r rune) bool { return r < ' ' && r != '\t' || r == 0x7f }) {
		return errors.New("the value cannot contain line breaks or control characters")
	}

	return nil
}

// isTokenChar is RFC 9110's tchar: what a header name is made of.
func isTokenChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune("!#$%&'*+-.^_`|~", r)
	}
}
