// Package httpreq holds the HTTP request node (D438): any web API's data on the items, or
// the items sent to one.
package httpreq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const RequestType = "http.request"

const (
	// DefaultMaxRequests is how many requests a node makes in a run, unless an admin says.
	DefaultMaxRequests = 100

	defaultTimeout = 10 * time.Second
	maxTimeout     = time.Minute

	// quoted is how much of a refused answer's body its error quotes.
	quoted = 200
)

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

var _ engine.Node = (*Request)(nil)

// Request makes an HTTP request for each item, or one for them all, and puts the answer
// on the items: its status, its headers and its body, JSON read as such. An answer whose
// status is not 2xx fails its items, the answer on them still, for an error branch.
type Request struct {
	requester   ports.HTTPRequester
	credentials ports.CredentialReader // nil where there is no vault, e.g. the demo
	max         func() int             // the requests a run may make, as an admin set; nil for the default
}

func New(requester ports.HTTPRequester, credentials ports.CredentialReader, max func() int) *Request {
	return &Request{requester: requester, credentials: credentials, max: max}
}

var responseField = engine.Field{Name: "response", Kind: engine.FieldObject, Description: "The answer.", Fields: []engine.Field{
	shape.Number("status", "Its status, like 200."),
	{
		Name: "headers", Kind: engine.FieldAny,
		Description: "Its headers, by name in lower case, like content-type; one given twice has its values joined by commas.",
	},
	{Name: "body", Kind: engine.FieldAny, Description: "Its body: what the JSON holds, read, or else the text."},
}}

func (*Request) Type() engine.NodeType {
	return engine.NodeType{
		Name:  RequestType,
		Label: "HTTP request",
		Description: "Asks any web API for data, or sends it some: a GET, POST, PUT, PATCH or DELETE, the answer put " +
			"on each item, JSON read as such. An answer not 2xx fails the item, the answer on it still.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   &engine.Items{Passed: true, Named: "field", Fields: []engine.Field{responseField}},
		Params: []engine.ParamSpec{
			{Name: "method", Label: "Method", Kind: engine.ParamString, Default: "GET", Options: methods},
			{
				Name: "url", Label: "URL", Kind: engine.ParamString, Required: true,
				Description: "https://… and any {{ field }}, e.g. https://api.example.com/markets/{{ id }}. A key belongs in " +
					"a credential, below: sharing the workflow keeps the URL.",
			},
			{
				Name: "query", Label: "Query", Kind: engine.ParamList,
				Description: "name=value, one per line, added to the URL, each encoded for you: q={{ question }}.",
			},
			{
				Name: "headers", Label: "Headers", Kind: engine.ParamList,
				Description: "Name: value, one per line, e.g. Accept: application/json.",
			},
			{
				Name: "body", Template: true, Multiline: true, Label: "Body", Kind: engine.ParamString,
				Description: `Sent with the request, empty for none. As json, a {{ }} inside quotes is written as ` +
					`text, and anywhere else as JSON: {"question": "{{ question }}", "item": {{ item }}}.`,
			},
			{
				Name: "bodyAs", Label: "Body as", Kind: engine.ParamString, Default: "json", Options: []string{"json", "form", "text"},
				Description: "json, checked as such; form, name=value&… with each value encoded; or text as it is. Its " +
					"Content-Type, unless Headers give one.",
			},
			{
				Name: "credential", Label: "Credential", Kind: engine.ParamCredential, CredentialTypes: credtypes.HTTPTypes,
				Description: "Optional. A header, a bearer token, a username and password, or a key in the URL, from your " +
					"Credentials page.",
			},
			{
				Name: "send", Label: "Send", Kind: engine.ParamString, Default: "each", Options: []string{"each", "once"},
				Description: "each: a request for each item, up to 100 a run unless an admin says otherwise. once: one " +
					"request with the first item's values, its answer on every item.",
			},
			{
				Name: "response", Label: "Read the answer as", Kind: engine.ParamString, Default: "auto",
				Options:     []string{"auto", "json", "text"},
				Description: "auto: JSON when it says it is, text otherwise. json: JSON, failing the item if it is not.",
			},
			{Name: "field", Label: "Answer field", Kind: engine.ParamString, Default: "response", Description: "Where the answer goes on the item."},
			{
				Name: "timeout", Label: "Timeout", Kind: engine.ParamDuration, Default: "10s",
				Description: "How long a request may take, redirects and all; at most a minute.",
			},
		},
	}
}

// auth is what a credential adds to each request.
type auth struct {
	header map[string]string
	query  url.Values
}

// call is one request, worked out for an item.
type call struct {
	req   ports.HTTPRequest
	host  string
	read  string // auto, json or text
	field string
}

func (n *Request) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return out, nil
	}

	first, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	send, err := first.String("send")
	if err != nil {
		return nil, err
	}

	a, err := n.auth(ctx, rc, first)
	if err != nil {
		return nil, err
	}

	if send == "once" {
		return n.once(ctx, rc, items, a)
	}

	limit := n.limit()

	for i, item := range items {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		if i >= limit {
			out.Fail(item, src, fmt.Errorf("not sent: at most %d requests a run; send it once for every item instead", limit))
			continue
		}

		c, err := n.prepare(rc, item, src, a)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		res, err := n.requester.Do(ctx, c.req)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		answer(out, item, src, c, res, err)
	}

	return out, nil
}

// once makes one request, with the first item's values, and puts its answer on every item,
// each with its own field.
func (n *Request) once(ctx context.Context, rc engine.RunContext, items []engine.Item, a auth) (engine.Ports, error) {
	out := engine.Ports{}
	src := engine.ItemRef{Port: engine.DefaultPort}

	c, err := n.prepare(rc, items[0], src, a)

	var res ports.HTTPResponse
	if err == nil {
		res, err = n.requester.Do(ctx, c.req)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	for i, item := range items {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		p, perr := rc.Params.Resolve(src)
		if perr != nil {
			out.Fail(item, src, perr)
			continue
		}

		own := c
		if own.field, perr = p.String("field"); perr != nil {
			out.Fail(item, src, perr)
			continue
		}

		answer(out, item, src, own, res, nil)
	}

	return out, nil
}

func (n *Request) limit() int {
	if n.max != nil {
		if m := n.max(); m > 0 {
			return m
		}
	}

	return DefaultMaxRequests
}

// answer puts the answer on the item: on main for a 2xx, on the error port otherwise,
// saying why; err, the request's own, fails the item without one.
func answer(out engine.Ports, item engine.Item, src engine.ItemRef, c call, res ports.HTTPResponse, err error) {
	if err != nil {
		out.Fail(item, src, err)
		return
	}

	ok := res.Status >= 200 && res.Status <= 299

	body, err := read(res, c.read, ok)
	if err != nil {
		out.Fail(item, src, fmt.Errorf("%s %s answered %d: %w", c.req.Method, c.host, res.Status, err))
		return
	}

	j := maps.Clone(item.JSON)
	if j == nil {
		j = map[string]any{}
	}

	j[c.field] = map[string]any{"status": res.Status, "headers": lowered(res.Header), "body": body}

	if ok {
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
		return
	}

	why := fmt.Errorf("%s %s answered %d", c.req.Method, c.host, res.Status)
	if text := strings.TrimSpace(string(res.Body)); text != "" && utf8.ValidString(text) {
		why = fmt.Errorf("%w: %s", why, clip(text))
	}

	out.Add(engine.ErrorPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}, Err: why})
}

// prepare works the request out for an item, with its settings.
func (n *Request) prepare(rc engine.RunContext, item engine.Item, src engine.ItemRef, a auth) (call, error) {
	p, err := rc.Params.Resolve(src)
	if err != nil {
		return call{}, err
	}

	var c call

	method, err := p.String("method")
	if err != nil {
		return call{}, err
	}

	if c.req.Method = strings.ToUpper(strings.TrimSpace(method)); !slices.Contains(methods, c.req.Method) {
		return call{}, fmt.Errorf("method %q: one of %s", method, strings.Join(methods, ", "))
	}

	raw, err := p.String("url")
	if err != nil {
		return call{}, err
	}

	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return call{}, errors.New("the URL is not an http or https address")
	}

	c.host = u.Hostname()

	query, err := pairs(p, "query", "=")
	if err != nil {
		return call{}, err
	}

	extra := url.Values{}
	for _, kv := range query {
		extra.Add(kv[0], kv[1])
	}

	for name, values := range a.query {
		extra[name] = values
	}

	if len(extra) > 0 {
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}

		u.RawQuery += extra.Encode()
	}

	c.req.URL = u.String()

	headers, err := pairs(p, "headers", ":")
	if err != nil {
		return call{}, err
	}

	c.req.Header = map[string]string{}
	for _, kv := range headers {
		c.req.Header[textproto.CanonicalMIMEHeaderKey(kv[0])] = kv[1]
	}

	for name, value := range a.header {
		c.req.Header[textproto.CanonicalMIMEHeaderKey(name)] = value // the credential's wins
	}

	if err := body(p, item, &c.req); err != nil {
		return call{}, err
	}

	if c.req.Timeout, err = timeout(p); err != nil {
		return call{}, err
	}

	if c.read, err = p.String("response"); err != nil {
		return call{}, err
	}

	if c.field, err = p.String("field"); err != nil {
		return call{}, err
	}

	return c, nil
}

// pairs reads a list setting of name<sep>value lines, leaving out empty ones.
func pairs(p engine.Params, name, sep string) ([][2]string, error) {
	if p[name] == nil {
		return nil, nil
	}

	lines, err := p.Strings(name)
	if err != nil {
		return nil, err
	}

	var out [][2]string

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		k, v, found := strings.Cut(line, sep)
		if k = strings.TrimSpace(k); !found || k == "" {
			return nil, fmt.Errorf("%s: %q is not name%svalue", name, line, sep)
		}

		out = append(out, [2]string{k, strings.TrimSpace(v)})
	}

	return out, nil
}

// body fills the body in for the item, as the setting says, and its Content-Type unless
// the headers give one.
func body(p engine.Params, item engine.Item, req *ports.HTTPRequest) error {
	if p["body"] == nil {
		return nil
	}

	t, err := p.Template("body")
	if err != nil {
		return err
	}

	as, err := p.String("bodyAs")
	if err != nil {
		return err
	}

	var text, kind string

	switch as {
	case "form":
		text, kind = t.Render(item.JSON, url.QueryEscape), "application/x-www-form-urlencoded"
	case "text":
		text, kind = t.Render(item.JSON, engine.Same), "text/plain; charset=utf-8"
	default:
		text, kind = t.Fill(item.JSON, jsonValue), "application/json"

		if strings.TrimSpace(text) != "" && !json.Valid([]byte(text)) {
			var v any
			err := json.Unmarshal([]byte(text), &v)

			return fmt.Errorf("the body is not JSON: %w", err)
		}
	}

	if strings.TrimSpace(text) == "" {
		return nil
	}

	req.Body = []byte(text)

	if _, set := req.Header["Content-Type"]; !set {
		req.Header["Content-Type"] = kind
	}

	return nil
}

// jsonValue writes a value into JSON where it stands: inside a string's quotes, as that
// string's text; anywhere else, as JSON itself.
func jsonValue(sofar string, v any) string {
	if inString(sofar) {
		b, _ := json.Marshal(engine.Format(v))
		return string(b[1 : len(b)-1])
	}

	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(engine.Format(v))
	}

	return string(b)
}

// inString reports whether JSON text ends inside a string.
func inString(s string) bool {
	in, escaped := false, false

	for i := range len(s) {
		switch c := s[i]; {
		case escaped:
			escaped = false
		case c == '\\' && in:
			escaped = true
		case c == '"':
			in = !in
		}
	}

	return in
}

func timeout(p engine.Params) (time.Duration, error) {
	if p["timeout"] == nil {
		return defaultTimeout, nil
	}

	d, err := p.Duration("timeout")
	if err != nil {
		return 0, err
	}

	if d > maxTimeout {
		return 0, fmt.Errorf("timeout %s: at most %s", d, maxTimeout)
	}

	return d, nil
}

// read is the answer's body: JSON read, as asked or as it says it is, or its text. A
// refused answer's body that is not JSON is its text, whatever was asked.
func read(res ports.HTTPResponse, as string, ok bool) (any, error) {
	if len(bytes.TrimSpace(res.Body)) == 0 {
		return string(res.Body), nil
	}

	if as == "text" || (as == "auto" && !isJSON(res.Header)) {
		return text(res.Body), nil
	}

	dec := json.NewDecoder(bytes.NewReader(res.Body))
	dec.UseNumber() // exact: a price stays 0.53, an ID every digit

	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		if as == "json" && ok {
			if err == nil {
				err = errors.New("more after the JSON")
			}

			return nil, fmt.Errorf("its answer is not JSON: %w", err)
		}

		return text(res.Body), nil
	}

	return v, nil
}

// text is a body as text; one that is not UTF-8, an image say, as base64.
func text(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}

	return base64.StdEncoding.EncodeToString(b)
}

// isJSON reports whether the headers say the body is JSON: application/json, or a type
// ending +json.
func isJSON(h map[string][]string) bool {
	for name, values := range h {
		if !strings.EqualFold(name, "Content-Type") || len(values) == 0 {
			continue
		}

		kind, _, err := mime.ParseMediaType(values[0])

		return err == nil && (kind == "application/json" || strings.HasSuffix(kind, "+json"))
	}

	return false
}

// lowered are the headers by name in lower case, a header's values joined.
func lowered(h map[string][]string) map[string]any {
	out := make(map[string]any, len(h))

	for name, values := range h {
		out[strings.ToLower(name)] = strings.Join(values, ", ")
	}

	return out
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= quoted {
		return s
	}

	return string([]rune(s)[:quoted-1]) + "…"
}

// auth reads the credential, if the node has one, as what it adds to each request.
func (n *Request) auth(ctx context.Context, rc engine.RunContext, p engine.Params) (auth, error) {
	if p["credential"] == nil {
		return auth{}, nil
	}

	id, err := p.String("credential")
	if err != nil {
		return auth{}, err
	}

	if n.credentials == nil {
		return auth{}, errors.New("credentials are not available here")
	}

	typ, values, err := n.credentials.Values(ctx, rc.OwnerID, domain.CredentialID(id), credtypes.HTTPTypes)
	if err != nil {
		return auth{}, fmt.Errorf("credential: %w", err)
	}

	switch typ {
	case credtypes.HTTPBearer:
		return auth{header: map[string]string{"Authorization": "Bearer " + values["token"]}}, nil
	case credtypes.HTTPBasic:
		pair := base64.StdEncoding.EncodeToString([]byte(values["username"] + ":" + values["password"]))
		return auth{header: map[string]string{"Authorization": "Basic " + pair}}, nil
	case credtypes.HTTPQuery:
		return auth{query: url.Values{values["name"]: {values["value"]}}}, nil
	default:
		return auth{header: map[string]string{values["header"]: values["value"]}}, nil
	}
}
