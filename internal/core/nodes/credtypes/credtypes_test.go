package credtypes_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
)

func TestHTTPHeader(t *testing.T) {
	typ := credtypes.HTTPHeaderType()
	require.NoError(t, engine.NewRegistry().RegisterCredentialType(typ))

	tests := []struct {
		name, header, value string
		wantErr             string
	}{
		{name: "bearer token", header: "Authorization", value: "Bearer abc.def"},
		{name: "custom header", header: "X-Api-Key", value: "k1\twith tab"},
		{name: "empty name", header: "", value: "v", wantErr: "is not a header name"},
		{name: "space in the name", header: "X Api", value: "v", wantErr: `"X Api" is not a header name`},
		{name: "colon in the name", header: "X:Y", value: "v", wantErr: "is not a header name"},
		{name: "one the sender sets", header: "content-type", value: "text/plain", wantErr: "Content-Type is set by Polythought itself"},
		{name: "host", header: "Host", value: "evil.example", wantErr: "Host is set by Polythought itself"},
		{name: "a line break in the value", header: "X-Key", value: "v\r\nX-Other: 1", wantErr: "cannot contain line breaks"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := typ.Check(map[string]string{"header": tc.header, "value": tc.value})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			assert.ErrorContains(t, err, tc.wantErr)
		})
	}

	var secret []string
	for _, f := range typ.Fields {
		if f.Secret {
			secret = append(secret, f.Name)
		}
	}

	assert.Equal(t, []string{"value"}, secret, "the header name is shown, its value never")
}

func TestHTTPAuthTypes(t *testing.T) {
	r := engine.NewRegistry()

	for _, typ := range []engine.CredentialType{credtypes.HTTPBearerType(), credtypes.HTTPBasicType(), credtypes.HTTPQueryType()} {
		require.NoError(t, r.RegisterCredentialType(typ), typ.Name)
	}

	tests := []struct {
		name    string
		typ     engine.CredentialType
		values  map[string]string
		wantErr string
	}{
		{name: "a token", typ: credtypes.HTTPBearerType(), values: map[string]string{"token": "abc.def"}},
		{name: "a token over two lines", typ: credtypes.HTTPBearerType(), values: map[string]string{"token": "a\nb"}, wantErr: "the token cannot contain line breaks"},
		{name: "a username and password", typ: credtypes.HTTPBasicType(), values: map[string]string{"username": "ann", "password": "p:w"}},
		{name: "a key as the username", typ: credtypes.HTTPBasicType(), values: map[string]string{"username": "sk_live_1"}},
		{name: "a colon in the username", typ: credtypes.HTTPBasicType(), values: map[string]string{"username": "a:b"}, wantErr: "cannot contain a colon"},
		{name: "a key in the URL", typ: credtypes.HTTPQueryType(), values: map[string]string{"name": "apikey", "value": "k&1"}},
		{name: "a line break in its value", typ: credtypes.HTTPQueryType(), values: map[string]string{"name": "apikey", "value": "k\r\n"}, wantErr: "the value cannot contain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.typ.Check(tc.values)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			assert.ErrorContains(t, err, tc.wantErr)
		})
	}

	secret := map[string][]string{}
	for _, typ := range []engine.CredentialType{credtypes.HTTPBearerType(), credtypes.HTTPBasicType(), credtypes.HTTPQueryType()} {
		for _, f := range typ.Fields {
			if f.Secret {
				secret[typ.Name] = append(secret[typ.Name], f.Name)
			}
		}
	}

	assert.Equal(t, map[string][]string{
		credtypes.HTTPBearer: {"token"}, credtypes.HTTPBasic: {"password"}, credtypes.HTTPQuery: {"value"},
	}, secret)
}
