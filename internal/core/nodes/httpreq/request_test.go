package httpreq_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/httpreq"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// fake answers each request with answer, keeping what it was asked.
type fake struct {
	asked  []ports.HTTPRequest
	answer func(req ports.HTTPRequest) (ports.HTTPResponse, error)
}

func (f *fake) Do(_ context.Context, req ports.HTTPRequest) (ports.HTTPResponse, error) {
	f.asked = append(f.asked, req)
	if f.answer == nil {
		return jsonAnswer(200, `{"ok":true}`), nil
	}

	return f.answer(req)
}

func jsonAnswer(status int, body string) ports.HTTPResponse {
	return ports.HTTPResponse{Status: status, Header: map[string][]string{"Content-Type": {"application/json; charset=utf-8"}}, Body: []byte(body)}
}

func markets(n int) engine.Ports {
	in := engine.Ports{}
	for i := range n {
		in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{
			"id": fmt.Sprintf("m%d", i), "question": fmt.Sprintf("Will it rain %d?", i), "price": decimal.RequireFromString("0.53"),
		}})
	}

	return in
}

func run(t *testing.T, node *httpreq.Request, params map[string]any, in engine.Ports) engine.Ports {
	t.Helper()

	out, err := node.Run(context.Background(), engine.RunContext{OwnerID: "u-1", Params: engine.LiteralParams(node.Type(), params)}, in)
	require.NoError(t, err)

	return out
}

func TestRequest(t *testing.T) {
	t.Run("a GET for each item, its JSON answer on the item", func(t *testing.T) {
		f := &fake{answer: func(ports.HTTPRequest) (ports.HTTPResponse, error) {
			return jsonAnswer(200, `{"temp": 71.5, "id": 12345678901234567890}`), nil
		}}

		out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/weather?city=nyc"}, markets(2))

		require.Len(t, f.asked, 2)
		assert.Equal(t, "GET", f.asked[0].Method)
		assert.Equal(t, "https://api.example.com/weather?city=nyc", f.asked[0].URL)
		assert.Nil(t, f.asked[0].Body)
		assert.Equal(t, 10*time.Second, f.asked[0].Timeout)

		items := out[engine.DefaultPort]
		require.Len(t, items, 2)
		assert.Equal(t, "m0", items[0].JSON["id"], "the item's own fields stay")

		res := items[0].JSON["response"].(map[string]any)
		assert.Equal(t, 200, res["status"])
		assert.Equal(t, "application/json; charset=utf-8", res["headers"].(map[string]any)["content-type"])

		body := res["body"].(map[string]any)
		assert.Equal(t, json.Number("71.5"), body["temp"])
		assert.Equal(t, json.Number("12345678901234567890"), body["id"], "every digit")

		require.NoError(t, shape.Check(httpreq.New(f, nil, nil).Type(), out))
	})

	t.Run("query lines encoded and added to the URL's own", func(t *testing.T) {
		f := &fake{}
		run(t, httpreq.New(f, nil, nil), map[string]any{
			"url":   "https://api.example.com/search?limit=5",
			"query": []any{"q = will it rain & snow?", "", "tag=a/b"},
		}, markets(1))

		require.Len(t, f.asked, 1)
		u, err := url.Parse(f.asked[0].URL)
		require.NoError(t, err)
		assert.Equal(t, "5", u.Query().Get("limit"))
		assert.Equal(t, "will it rain & snow?", u.Query().Get("q"))
		assert.Equal(t, "a/b", u.Query().Get("tag"))
	})

	t.Run("headers, and a JSON body with each value in its place", func(t *testing.T) {
		f := &fake{}
		run(t, httpreq.New(f, nil, nil), map[string]any{
			"method":  "post",
			"url":     "https://api.example.com/notes",
			"headers": []any{"accept: application/json", "X-Trace: a:b"},
			"body":    `{"q": "{{ question }} \"quoted\"", "price": {{ price }}, "id": {{ id }}, "none": {{ missing }}, "all": {{ item }}}`,
		}, engine.Ports{engine.DefaultPort: {{JSON: map[string]any{
			"question": `Say "hi"?`, "price": decimal.RequireFromString("0.53"), "id": "m1",
		}}}})

		require.Len(t, f.asked, 1)
		req := f.asked[0]
		assert.Equal(t, "POST", req.Method)
		assert.Equal(t, map[string]string{"Accept": "application/json", "X-Trace": "a:b", "Content-Type": "application/json"}, req.Header)
		assert.JSONEq(t, `{"q": "Say \"hi\"? \"quoted\"", "price": "0.53", "id": "m1", "none": null,
			"all": {"question": "Say \"hi\"?", "price": "0.53", "id": "m1"}}`, string(req.Body))
	})

	t.Run("a form body, its values encoded, and the headers' Content-Type kept", func(t *testing.T) {
		f := &fake{}
		run(t, httpreq.New(f, nil, nil), map[string]any{
			"method": "PUT", "url": "https://api.example.com/", "bodyAs": "form", "body": "q={{ question }}&id={{ id }}",
		}, markets(1))
		run(t, httpreq.New(f, nil, nil), map[string]any{
			"method": "PATCH", "url": "https://api.example.com/", "bodyAs": "text", "body": "{{ question }}",
			"headers": []any{"Content-Type: text/csv"},
		}, markets(1))

		require.Len(t, f.asked, 2)
		assert.Equal(t, "q=Will+it+rain+0%3F&id=m0", string(f.asked[0].Body))
		assert.Equal(t, "application/x-www-form-urlencoded", f.asked[0].Header["Content-Type"])
		assert.Equal(t, "Will it rain 0?", string(f.asked[1].Body))
		assert.Equal(t, "text/csv", f.asked[1].Header["Content-Type"])
	})

	t.Run("a body that is not JSON fails the item, unsent", func(t *testing.T) {
		f := &fake{}
		out := run(t, httpreq.New(f, nil, nil), map[string]any{"method": "POST", "url": "https://api.example.com/", "body": `{"q": {{ question }`}, markets(1))

		assert.Empty(t, f.asked)
		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "the body is not JSON")
	})

	t.Run("an answer not 2xx out the error port, its status and body on the item", func(t *testing.T) {
		f := &fake{answer: func(ports.HTTPRequest) (ports.HTTPResponse, error) {
			return jsonAnswer(429, `{"error": "slow down"}`), nil
		}}

		out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/x?key=secret", "field": "answer"}, markets(1))

		assert.Empty(t, out[engine.DefaultPort])
		require.Len(t, out[engine.ErrorPort], 1)

		failed := out[engine.ErrorPort][0]
		assert.EqualError(t, failed.Err, `GET api.example.com answered 429: {"error": "slow down"}`, "the host, never the URL")

		res := failed.JSON["answer"].(map[string]any)
		assert.Equal(t, 429, res["status"])
		assert.Equal(t, map[string]any{"error": "slow down"}, res["body"])
	})

	t.Run("a 4xx is final, a 5xx or 429 may be tried again (D440)", func(t *testing.T) {
		for status, final := range map[int]bool{400: true, 404: true, 422: true, 408: false, 429: false, 500: false, 503: false} {
			f := &fake{answer: func(ports.HTTPRequest) (ports.HTTPResponse, error) { return jsonAnswer(status, `{}`), nil }}
			out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/"}, markets(1))

			require.Len(t, out[engine.ErrorPort], 1)
			assert.Equal(t, final, engine.IsFinal(out[engine.ErrorPort][0].Err), status)
		}
	})

	t.Run("a request that fails fails its item", func(t *testing.T) {
		f := &fake{answer: func(ports.HTTPRequest) (ports.HTTPResponse, error) {
			return ports.HTTPResponse{}, errors.New("GET api.example.com: no answer in time")
		}}

		out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/"}, markets(1))

		require.Len(t, out[engine.ErrorPort], 1)
		assert.EqualError(t, out[engine.ErrorPort][0].Err, "GET api.example.com: no answer in time")
		assert.NotContains(t, out[engine.ErrorPort][0].JSON, "response")
	})

	t.Run("read as asked", func(t *testing.T) {
		html := ports.HTTPResponse{Status: 200, Header: map[string][]string{"Content-Type": {"text/html"}}, Body: []byte(`<p>{"a":1}</p>`)}
		untyped := ports.HTTPResponse{Status: 200, Body: []byte(`{"a":1}`)}

		for _, tc := range []struct {
			name, as string
			res      ports.HTTPResponse
			want     any
			wantErr  string
		}{
			{name: "auto, JSON that says so", as: "auto", res: jsonAnswer(200, `[1, "two"]`), want: []any{json.Number("1"), "two"}},
			{name: "auto, JSON that does not say so", as: "auto", res: untyped, want: `{"a":1}`},
			{name: "auto, JSON that says so but is not", as: "auto", res: jsonAnswer(200, `{"a":`), want: `{"a":`},
			{name: "json, whatever it says", as: "json", res: untyped, want: map[string]any{"a": json.Number("1")}},
			{name: "json that is not", as: "json", res: html, wantErr: "GET api.example.com answered 200: its answer is not JSON"},
			{name: "json, two values", as: "json", res: jsonAnswer(200, `{} {}`), wantErr: "more after the JSON"},
			{name: "text, JSON too", as: "text", res: jsonAnswer(200, `{"a":1}`), want: `{"a":1}`},
			{name: "nothing", as: "json", res: ports.HTTPResponse{Status: 204}, want: ""},
			{name: "bytes that are not text, as base64", as: "auto", res: ports.HTTPResponse{Status: 200, Body: []byte{0xff, 0xfe}}, want: "//4="},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := &fake{answer: func(ports.HTTPRequest) (ports.HTTPResponse, error) { return tc.res, nil }}
				out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/", "response": tc.as}, markets(1))

				if tc.wantErr != "" {
					require.Len(t, out[engine.ErrorPort], 1)
					assert.ErrorContains(t, out[engine.ErrorPort][0].Err, tc.wantErr)

					return
				}

				require.Len(t, out[engine.DefaultPort], 1)
				assert.Equal(t, tc.want, out[engine.DefaultPort][0].JSON["response"].(map[string]any)["body"])
			})
		}
	})

	t.Run("once: one request, its answer on every item", func(t *testing.T) {
		f := &fake{}
		out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/", "send": "once"}, markets(3))

		assert.Len(t, f.asked, 1)
		require.Len(t, out[engine.DefaultPort], 3)

		for i, it := range out[engine.DefaultPort] {
			assert.Equal(t, fmt.Sprintf("m%d", i), it.JSON["id"])
			assert.Equal(t, map[string]any{"ok": true}, it.JSON["response"].(map[string]any)["body"])
		}
	})

	t.Run("at most as many requests a run as an admin says", func(t *testing.T) {
		f := &fake{}
		out := run(t, httpreq.New(f, nil, func() int { return 2 }), map[string]any{"url": "https://api.example.com/"}, markets(3))

		assert.Len(t, f.asked, 2)
		assert.Len(t, out[engine.DefaultPort], 2)
		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "at most 2 requests a run")

		f = &fake{}
		run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/"}, markets(httpreq.DefaultMaxRequests+1))
		assert.Len(t, f.asked, httpreq.DefaultMaxRequests, "100 unless said")
	})

	t.Run("settings it refuses", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			params  map[string]any
			wantErr string
		}{
			{name: "a URL not http", params: map[string]any{"url": "file:///etc/passwd"}, wantErr: "not an http or https address"},
			{name: "a query line without =", params: map[string]any{"url": "https://a.example/", "query": []any{"q"}}, wantErr: `query: "q" is not name=value`},
			{name: "a header line without :", params: map[string]any{"url": "https://a.example/", "headers": []any{"Accept"}}, wantErr: `headers: "Accept" is not name:value`},
			{name: "too long a timeout", params: map[string]any{"url": "https://a.example/", "timeout": "2m"}, wantErr: "at most 1m0s"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := &fake{}
				out := run(t, httpreq.New(f, nil, nil), tc.params, markets(1))

				assert.Empty(t, f.asked)
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorContains(t, out[engine.ErrorPort][0].Err, tc.wantErr)
			})
		}
	})

	t.Run("nothing in, nothing asked", func(t *testing.T) {
		f := &fake{}
		out := run(t, httpreq.New(f, nil, nil), map[string]any{"url": "https://api.example.com/"}, engine.Ports{})

		assert.Empty(t, f.asked)
		assert.Empty(t, out)
	})
}

func TestRequestCredentials(t *testing.T) {
	const owner = domain.UserID("u-1")

	for _, tc := range []struct {
		name       string
		typ        string
		values     map[string]string
		wantHeader map[string]string
		wantURL    string
	}{
		{
			name: "a header", typ: credtypes.HTTPHeader, values: map[string]string{"header": "x-api-key", "value": "k-1"},
			wantHeader: map[string]string{"X-Api-Key": "k-1", "Authorization": "mine"}, wantURL: "https://api.example.com/?a=1",
		},
		{
			name: "a bearer token, over the headers' Authorization", typ: credtypes.HTTPBearer, values: map[string]string{"token": "t-1"},
			wantHeader: map[string]string{"Authorization": "Bearer t-1"}, wantURL: "https://api.example.com/?a=1",
		},
		{
			name: "a username and password", typ: credtypes.HTTPBasic, values: map[string]string{"username": "ann", "password": "p:w"},
			wantHeader: map[string]string{"Authorization": "Basic YW5uOnA6dw=="}, wantURL: "https://api.example.com/?a=1",
		},
		{
			name: "a key in the URL", typ: credtypes.HTTPQuery, values: map[string]string{"name": "apikey", "value": "k&1"},
			wantHeader: map[string]string{"Authorization": "mine"}, wantURL: "https://api.example.com/?a=1&apikey=k%261",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creds := portsmocks.NewMockCredentialReader(t)
			creds.EXPECT().Values(mock.Anything, owner, domain.CredentialID("c-1"), credtypes.HTTPTypes).Return(tc.typ, tc.values, nil).Once()

			f := &fake{}
			out := run(t, httpreq.New(f, creds, nil), map[string]any{
				"url": "https://api.example.com/?a=1", "credential": "c-1", "headers": []any{"Authorization: mine"},
			}, markets(2))

			require.Len(t, out[engine.DefaultPort], 2)
			require.Len(t, f.asked, 2, "the credential read once a run")
			assert.Equal(t, tc.wantHeader, f.asked[0].Header)
			assert.Equal(t, tc.wantURL, f.asked[0].URL)
		})
	}

	t.Run("one it cannot read fails the run", func(t *testing.T) {
		creds := portsmocks.NewMockCredentialReader(t)
		creds.EXPECT().Values(mock.Anything, owner, domain.CredentialID("c-9"), credtypes.HTTPTypes).Return("", nil, domain.ErrNotFound).Once()

		node := httpreq.New(&fake{}, creds, nil)
		_, err := node.Run(context.Background(), engine.RunContext{
			OwnerID: owner, Params: engine.LiteralParams(node.Type(), map[string]any{"url": "https://a.example/", "credential": "c-9"}),
		}, markets(1))

		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

// TestRequestExpressions runs the node in a workflow, its settings' {{ }} worked out for
// each item by the executor.
func TestRequestExpressions(t *testing.T) {
	registry := engine.NewRegistry()
	require.NoError(t, registry.RegisterCredentialType(credtypes.HTTPHeaderType()))
	require.NoError(t, registry.RegisterCredentialType(credtypes.HTTPBearerType()))
	require.NoError(t, registry.RegisterCredentialType(credtypes.HTTPBasicType()))
	require.NoError(t, registry.RegisterCredentialType(credtypes.HTTPQueryType()))
	require.NoError(t, registry.RegisterTrigger(triggers.Manual{}))

	f := &fake{answer: func(req ports.HTTPRequest) (ports.HTTPResponse, error) {
		if req.URL == "https://api.example.com/markets/m1?q=Will+it+rain+1%3F" {
			return jsonAnswer(404, `{"error": "no such market"}`), nil
		}

		return jsonAnswer(200, `{"ok": true}`), nil
	}}
	require.NoError(t, registry.RegisterNode(httpreq.New(f, nil, nil)))

	executor, err := engine.NewExecutor(observability.NoopUtility(), registry)
	require.NoError(t, err)

	wf := domain.Workflow{
		ID: "wf-1",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: triggers.ManualType},
			{ID: "ask", Type: httpreq.RequestType, ContinueOnFail: true, Params: map[string]any{
				"method":  "POST",
				"url":     "https://api.example.com/markets/{{ id }}",
				"query":   []any{"q={{ question }}"},
				"headers": []any{"X-Market: {{ id }}"},
				"body":    `{"price": {{ price }}}`,
			}},
		},
		Connections: []domain.Connection{{From: "manual", FromPort: engine.DefaultPort, To: "ask", ToPort: engine.DefaultPort}},
	}

	res, err := executor.Execute(context.Background(), wf, "manual", markets(2))
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionSucceeded, res.Execution.Status, res.Execution.Error)

	require.Len(t, f.asked, 2)
	assert.Equal(t, "https://api.example.com/markets/m0?q=Will+it+rain+0%3F", f.asked[0].URL)
	assert.Equal(t, "m0", f.asked[0].Header["X-Market"])
	assert.JSONEq(t, `{"price": "0.53"}`, string(f.asked[0].Body))

	out := res.Outputs["ask"]
	require.Len(t, out[engine.DefaultPort], 1)
	require.Len(t, out[engine.ErrorPort], 1)
	assert.Equal(t, "m1", out[engine.ErrorPort][0].JSON["id"])
	assert.Equal(t, 404, out[engine.ErrorPort][0].JSON["response"].(map[string]any)["status"])
}
