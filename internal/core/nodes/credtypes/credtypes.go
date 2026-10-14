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
