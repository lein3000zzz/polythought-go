// Package mlmodels is users' own models: named, each served by workers that dial out and take
// its requests by a long poll, with a worker key of its own, or called at a URL. Workflows
// ask them, a batch of items a run, and they answer each item.
package mlmodels

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	// KeyPrefix starts every worker key, and prefixLength of it is kept to tell keys apart.
	KeyPrefix    = "pw_"
	prefixLength = 10

	// DefaultWait is how long an asker waits to begin with.
	DefaultWait = 30 * time.Second

	maxKeys       = 20  // a model's
	maxName       = 64  // a worker's, a key's, a version
	maxReason     = 500 // a worker's failure
	presenceSlack = 30 * time.Second
	blockFor      = time.Minute
	usedEvery     = time.Minute // how often a key's last use is kept
)

// Limits are an admin's, read on every use.
type Limits struct {
	Models       int           // a user's
	Workers      int           // connected to a model at once
	Items        int           // a request's
	RequestBytes int           // a request's items together
	AnswerBytes  int           // a request's answers together
	MaxWait      time.Duration // the longest an asker may wait
	Poll         time.Duration // the longest a worker's poll waits
	Lease        time.Duration // how long a worker holds a request before another may take it
}

// Gate says whether a user may use models now.
type Gate interface {
	DataAllowed(ctx context.Context, owner domain.UserID) (bool, error)
}

type Deps struct {
	Repo   ports.ModelRepository
	Gate   Gate
	Limits func() Limits
	// HTTP calls URL models, as webhooks are called; without it, there are none.
	HTTP ports.HTTPRequester
	// Credentials reads a URL model's http.header credential; without it, none takes one.
	Credentials ports.CredentialReader
	// Misses limits each address's wrong worker keys.
	Misses ports.RateLimiter
	Logger logging.Logger
	Now    func() time.Time
}

type Service struct {
	deps   Deps
	log    logging.Logger
	broker *broker

	mu      sync.Mutex
	blocked map[string]time.Time // addresses with too many wrong keys, until when
}

func New(deps Deps) *Service {
	if deps.Logger == nil {
		deps.Logger = logging.Noop()
	}

	if deps.Now == nil {
		deps.Now = time.Now
	}

	return &Service{
		deps: deps, log: deps.Logger.With("component", "models"), broker: newBroker(deps.Now),
		blocked: map[string]time.Time{},
	}
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalidInput, fmt.Sprintf(format, args...))
}

// allowed refuses a user an admin keeps from models.
func (s *Service) allowed(ctx context.Context, owner domain.UserID) error {
	if s.deps.Gate == nil {
		return nil
	}

	ok, err := s.deps.Gate.DataAllowed(ctx, owner)
	if err != nil {
		return err
	}

	if !ok {
		return domain.ErrDataOff
	}

	return nil
}

// window is how long a worker counts as connected after it last polled: its poll, and
// some.
func (s *Service) window() time.Duration { return s.deps.Limits().Poll + presenceSlack }

// View is a model with its workers connected now.
type View struct {
	domain.Model
	Workers []Connected
}

func (s *Service) view(m domain.Model) View {
	v := View{Model: m, Workers: []Connected{}}
	if m.Kind == domain.ModelWorker {
		v.Workers = s.broker.connected(m.ID, s.window())
	}

	return v
}

// Spec is a new model, or a URL model's new address.
type Spec struct {
	Name       string
	Kind       domain.ModelKind
	URL        string
	Credential domain.CredentialID
}

func (s *Service) Create(ctx context.Context, owner domain.UserID, spec Spec) (View, error) {
	if err := s.allowed(ctx, owner); err != nil {
		return View{}, err
	}

	spec.Name = strings.TrimSpace(spec.Name)
	if !nameRe.MatchString(spec.Name) {
		return View{}, invalid("name %q: lower-case letters, digits, - and _, up to 64, like temperature-model", spec.Name)
	}

	if !spec.Kind.Valid() {
		return View{}, invalid("kind %q: worker, for workers that take its requests, or url, for one called at a URL", spec.Kind)
	}

	if err := s.checkURL(ctx, owner, spec); err != nil {
		return View{}, err
	}

	have, err := s.deps.Repo.List(ctx, owner)
	if err != nil {
		return View{}, err
	}

	if most := s.deps.Limits().Models; len(have) >= most {
		return View{}, fmt.Errorf("%w: you have %d models, the most you may", domain.ErrModelLimit, most)
	}

	now := s.deps.Now().UTC()
	m := domain.Model{
		ID: domain.ModelID(uuid.Must(uuid.NewV7()).String()), OwnerID: owner, Name: spec.Name, Kind: spec.Kind,
		URL: strings.TrimSpace(spec.URL), Credential: spec.Credential, CreatedAt: now, UpdatedAt: now,
	}

	if err := s.deps.Repo.Create(ctx, m); err != nil {
		return View{}, err
	}

	s.log.Info(ctx, "Model made", "owner_id", owner, "model", m.Name, "kind", m.Kind)

	return s.view(m), nil
}

// checkURL checks a URL model's address and credential, and that a worker model has
// neither.
func (s *Service) checkURL(ctx context.Context, owner domain.UserID, spec Spec) error {
	if spec.Kind == domain.ModelWorker {
		if strings.TrimSpace(spec.URL) != "" || spec.Credential != "" {
			return invalid("a worker model has no URL nor credential: its workers take its requests")
		}

		return nil
	}

	if s.deps.HTTP == nil {
		return invalid("models at a URL are not available here")
	}

	u, err := url.Parse(strings.TrimSpace(spec.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return invalid("url %q: an http or https address, like https://models.example.com/predict", spec.URL)
	}

	if spec.Credential == "" {
		return nil
	}

	if s.deps.Credentials == nil {
		return invalid("credentials are not available here")
	}

	if _, _, err := s.deps.Credentials.Values(ctx, owner, spec.Credential, []string{credtypes.HTTPHeader}); err != nil {
		return fmt.Errorf("credential: %w", err)
	}

	return nil
}

func (s *Service) List(ctx context.Context, owner domain.UserID) ([]View, error) {
	if err := s.allowed(ctx, owner); err != nil {
		return nil, err
	}

	list, err := s.deps.Repo.List(ctx, owner)
	if err != nil {
		return nil, err
	}

	out := make([]View, len(list))
	for i, m := range list {
		out[i] = s.view(m)
	}

	return out, nil
}

// model is the owner's model by name, once the gate lets them.
func (s *Service) model(ctx context.Context, owner domain.UserID, name string) (domain.Model, error) {
	if err := s.allowed(ctx, owner); err != nil {
		return domain.Model{}, err
	}

	m, err := s.deps.Repo.Get(ctx, owner, strings.TrimSpace(name))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Model{}, fmt.Errorf("%w: you have no model %q", domain.ErrNotFound, name)
	}

	return m, err
}

func (s *Service) Get(ctx context.Context, owner domain.UserID, name string) (View, error) {
	m, err := s.model(ctx, owner, name)
	if err != nil {
		return View{}, err
	}

	return s.view(m), nil
}

// SetURL changes where a URL model is called, and with what credential.
func (s *Service) SetURL(ctx context.Context, owner domain.UserID, name, address string, cred domain.CredentialID) (View, error) {
	m, err := s.model(ctx, owner, name)
	if err != nil {
		return View{}, err
	}

	if m.Kind != domain.ModelURL {
		return View{}, invalid("%q is a worker model: it has no URL", m.Name)
	}

	if err := s.checkURL(ctx, owner, Spec{Kind: domain.ModelURL, URL: address, Credential: cred}); err != nil {
		return View{}, err
	}

	if err := s.deps.Repo.SetURL(ctx, m.ID, strings.TrimSpace(address), cred, s.deps.Now()); err != nil {
		return View{}, err
	}

	return s.Get(ctx, owner, name)
}

// Delete deletes a model and its keys: its workers are refused from then on.
func (s *Service) Delete(ctx context.Context, owner domain.UserID, name string) error {
	m, err := s.model(ctx, owner, name)
	if err != nil {
		return err
	}

	if err := s.deps.Repo.Delete(ctx, m.ID); err != nil {
		return err
	}

	s.log.Info(ctx, "Model deleted", "owner_id", owner, "model", m.Name)

	return nil
}

// Keys are a worker model's keys, the newest first.
func (s *Service) Keys(ctx context.Context, owner domain.UserID, name string) ([]domain.WorkerKey, error) {
	m, err := s.model(ctx, owner, name)
	if err != nil {
		return nil, err
	}

	return s.deps.Repo.Keys(ctx, m.ID)
}

// CreateKey makes a worker key for one model, at a browser only, as API tokens are made:
// its secret is given once, here, and kept hashed.
func (s *Service) CreateKey(ctx context.Context, p auth.Principal, name, keyName string) (string, domain.WorkerKey, error) {
	if !p.ViaSession() {
		return "", domain.WorkerKey{}, fmt.Errorf("%w: worker keys are made at a browser", domain.ErrForbidden)
	}

	m, err := s.model(ctx, p.User.ID, name)
	if err != nil {
		return "", domain.WorkerKey{}, err
	}

	if m.Kind != domain.ModelWorker {
		return "", domain.WorkerKey{}, invalid("%q is called at its URL: only a worker model has worker keys", m.Name)
	}

	keyName = strings.TrimSpace(keyName)
	if keyName == "" || utf8.RuneCountInString(keyName) > maxName {
		return "", domain.WorkerKey{}, invalid("a key's name is 1 to %d characters, like laptop or gpu-box", maxName)
	}

	have, err := s.deps.Repo.Keys(ctx, m.ID)
	if err != nil {
		return "", domain.WorkerKey{}, err
	}

	if len(have) >= maxKeys {
		return "", domain.WorkerKey{}, fmt.Errorf("%w: %q has %d keys, the most a model may: revoke one first", domain.ErrModelLimit, m.Name, maxKeys)
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", domain.WorkerKey{}, fmt.Errorf("mlmodels: random: %w", err)
	}

	secret := KeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	k := domain.WorkerKey{
		ID: domain.WorkerKeyID(uuid.Must(uuid.NewV7()).String()), ModelID: m.ID, Name: keyName,
		Prefix: secret[:prefixLength], CreatedAt: s.deps.Now().UTC(),
	}

	if err := s.deps.Repo.CreateKey(ctx, k, hash(secret)); err != nil {
		return "", domain.WorkerKey{}, err
	}

	s.log.Info(ctx, "Worker key made", "owner_id", m.OwnerID, "model", m.Name, "key_id", k.ID)

	return secret, k, nil
}

func hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// DeleteKey revokes a worker key: its workers are refused from their next call.
func (s *Service) DeleteKey(ctx context.Context, owner domain.UserID, name string, id domain.WorkerKeyID) error {
	m, err := s.model(ctx, owner, name)
	if err != nil {
		return err
	}

	if err := s.deps.Repo.DeleteKey(ctx, m.ID, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: %q has no such key", domain.ErrNotFound, m.Name)
		}

		return err
	}

	s.log.Info(ctx, "Worker key revoked", "owner_id", owner, "model", m.Name, "key_id", id)

	return nil
}

// Worker is the model a worker key serves, for the worker calling with it from address:
// refused when the key is wrong, its owner kept from models, or the address has tried too
// many wrong keys lately.
func (s *Service) Worker(ctx context.Context, secret, address string) (domain.Model, error) {
	if s.isBlocked(address) {
		return domain.Model{}, domain.ErrRateLimited
	}

	if !strings.HasPrefix(secret, KeyPrefix) {
		s.missed(address)
		return domain.Model{}, domain.ErrUnauthenticated
	}

	k, m, err := s.deps.Repo.KeyByHash(ctx, hash(secret))
	if errors.Is(err, domain.ErrNotFound) {
		s.missed(address)
		return domain.Model{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return domain.Model{}, err
	}

	if err := s.allowed(ctx, m.OwnerID); err != nil {
		return domain.Model{}, err
	}

	if now := s.deps.Now(); now.Sub(k.LastUsedAt) >= usedEvery {
		if err := s.deps.Repo.KeyUsed(ctx, k.ID, now); err != nil {
			s.log.Warn(ctx, "Worker key's last use not kept", "key_id", k.ID, logging.Err(err))
		}
	}

	return m, nil
}

func (s *Service) isBlocked(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	until, ok := s.blocked[address]
	if ok && !s.deps.Now().Before(until) {
		delete(s.blocked, address)
		return false
	}

	return ok
}

// missed counts a wrong key against the address, which past its limit is refused a while.
func (s *Service) missed(address string) {
	if s.deps.Misses == nil || s.deps.Misses.Allow(address) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.deps.Now()
	for a, until := range s.blocked {
		if !now.Before(until) {
			delete(s.blocked, a)
		}
	}

	s.blocked[address] = now.Add(blockFor)
}

func checkWorker(w Worker) (Worker, error) {
	w.Name, w.Version = strings.TrimSpace(w.Name), strings.TrimSpace(w.Version)

	switch {
	case w.Name == "" || utf8.RuneCountInString(w.Name) > maxName:
		return w, invalid("a worker's name is 1 to %d characters, unique among the model's workers", maxName)
	case utf8.RuneCountInString(w.Version) > maxName:
		return w, invalid("a version is at most %d characters", maxName)
	}

	return w, nil
}

// Take hands a worker of m the next request, waiting up to the poll for one.
func (s *Service) Take(ctx context.Context, m domain.Model, w Worker) (Request, bool, error) {
	w, err := checkWorker(w)
	if err != nil {
		return Request{}, false, err
	}

	if m.Kind != domain.ModelWorker {
		return Request{}, false, invalid("%q is called at its URL: it has no workers", m.Name)
	}

	l := s.deps.Limits()

	return s.broker.take(ctx, m.ID, w, l.Poll, l.Lease, s.window(), l.Workers)
}

// Answer answers a request of m: one answer an item, in their order.
func (s *Service) Answer(ctx context.Context, m domain.Model, id string, w Worker, answers []json.RawMessage) error {
	w, err := checkWorker(w)
	if err != nil {
		return err
	}

	size := 0
	for _, a := range answers {
		size += len(a)
	}

	if most := s.deps.Limits().AnswerBytes; size > most {
		return invalid("the answers take %d bytes, more than the %d an answer may", size, most)
	}

	return s.broker.settle(m.ID, id, outcome{answer: Answer{Answers: answers, Version: w.Version, Worker: w.Name}})
}

// Fail fails a request of m, saying why: each of its items fails with the reason.
func (s *Service) Fail(ctx context.Context, m domain.Model, id string, w Worker, reason string) error {
	w, err := checkWorker(w)
	if err != nil {
		return err
	}

	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxReason {
		return invalid("say why, in 1 to %d characters", maxReason)
	}

	return s.broker.settle(m.ID, id, outcome{err: fmt.Errorf("the model's worker %s failed: %s", w.Name, reason)})
}

// Ask asks the owner's model about items, each a JSON object, and waits up to wait for
// its answers, one an item. A schema, if any, is what each answer is to keep to: the model
// is told it, and the answers that do not are refused by their index. A worker model with
// none connected fails at once.
func (s *Service) Ask(ctx context.Context, owner domain.UserID, name string, items []json.RawMessage, rawSchema json.RawMessage, wait time.Duration) (Answer, error) {
	m, err := s.model(ctx, owner, name)
	if err != nil {
		return Answer{}, err
	}

	schema, err := ParseSchema(rawSchema)
	if err != nil {
		return Answer{}, invalid("%s", err.Error())
	}

	l := s.deps.Limits()

	switch {
	case len(items) == 0:
		return Answer{}, invalid("no items to ask about")
	case len(items) > l.Items:
		return Answer{}, invalid("%d items, more than the %d a request may hold", len(items), l.Items)
	case wait <= 0 || wait > l.MaxWait:
		return Answer{}, invalid("a wait of %s: more than nothing, up to %s", wait, l.MaxWait)
	}

	size := len(schema.Raw())
	for _, it := range items {
		size += len(it)
	}

	if size > l.RequestBytes {
		return Answer{}, invalid("the items take %d bytes, more than the %d a request may", size, l.RequestBytes)
	}

	var a Answer

	if m.Kind == domain.ModelURL {
		a, err = s.callURL(ctx, m, items, schema.Raw(), wait, l.AnswerBytes)
	} else {
		a, err = s.broker.ask(ctx, m.ID, items, schema.Raw(), wait, s.window())
	}

	if err == nil && schema != nil {
		a.Refused = map[int]error{}

		for i, answer := range a.Answers {
			if err := schema.Check(answer); err != nil {
				a.Refused[i] = err
			}
		}
	}

	now := s.deps.Now()

	switch {
	case err == nil:
		if err := s.deps.Repo.Answered(ctx, m.ID, now); err != nil {
			s.log.Warn(ctx, "Model's last answer not kept", "model", m.Name, logging.Err(err))
		}
	case ctx.Err() == nil:
		if err := s.deps.Repo.Failed(ctx, m.ID, err.Error(), now); err != nil {
			s.log.Warn(ctx, "Model's last error not kept", "model", m.Name, logging.Err(err))
		}
	}

	return a, err
}

// urlAnswer is what a URL model answers.
type urlAnswer struct {
	Answers []json.RawMessage `json:"answers"`
	Version string            `json:"version"`
}

// callURL asks a URL model, as a webhook is called: POSTed JSON of the model's name, the
// items and the schema, with its credential's header; it answers JSON of the answers and
// its version.
func (s *Service) callURL(ctx context.Context, m domain.Model, items []json.RawMessage, schema json.RawMessage, wait time.Duration, most int) (Answer, error) {
	if s.deps.HTTP == nil {
		return Answer{}, invalid("models at a URL are not available here")
	}

	body, err := json.Marshal(struct {
		Model  string            `json:"model"`
		Items  []json.RawMessage `json:"items"`
		Schema json.RawMessage   `json:"schema,omitempty"`
	}{m.Name, items, schema})
	if err != nil {
		return Answer{}, err
	}

	header := map[string]string{"Content-Type": "application/json", "Accept": "application/json"}

	if m.Credential != "" {
		if s.deps.Credentials == nil {
			return Answer{}, invalid("credentials are not available here")
		}

		_, values, err := s.deps.Credentials.Values(ctx, m.OwnerID, m.Credential, []string{credtypes.HTTPHeader})
		if err != nil {
			return Answer{}, fmt.Errorf("the model's credential: %w", err)
		}

		header[values["header"]] = values["value"]
	}

	resp, err := s.deps.HTTP.Do(ctx, ports.HTTPRequest{Method: "POST", URL: m.URL, Header: header, Body: body, Timeout: wait})
	if err != nil {
		return Answer{}, fmt.Errorf("the model's URL: %w", err)
	}

	if resp.Status < 200 || resp.Status > 299 {
		return Answer{}, fmt.Errorf("the model's URL answered %d: %s", resp.Status, snippet(resp.Body))
	}

	if len(resp.Body) > most {
		return Answer{}, fmt.Errorf("the model's URL answered %d bytes, more than the %d an answer may", len(resp.Body), most)
	}

	var got urlAnswer

	dec := json.NewDecoder(bytes.NewReader(resp.Body))
	if err := dec.Decode(&got); err != nil {
		return Answer{}, fmt.Errorf(`the model's URL answered no JSON of {"answers": […]}: %w`, err)
	}

	if len(got.Answers) != len(items) {
		return Answer{}, fmt.Errorf("the model's URL gave %d answers for %d items: one an item, in their order", len(got.Answers), len(items))
	}

	return Answer{Answers: got.Answers, Version: strings.TrimSpace(got.Version)}, nil
}

// snippet is the start of a body, for an error.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:200]) + "…"
	}

	return s
}

// Limits are the admin's, as they are now.
func (s *Service) Limits() Limits { return s.deps.Limits() }
