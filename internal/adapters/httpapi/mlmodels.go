package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/mlmodels"
)

// MLModelService is users' own models, reached by their workers or at a URL.
type MLModelService interface {
	List(ctx context.Context, owner domain.UserID) ([]mlmodels.View, error)
	Get(ctx context.Context, owner domain.UserID, name string) (mlmodels.View, error)
	Create(ctx context.Context, owner domain.UserID, spec mlmodels.Spec) (mlmodels.View, error)
	SetURL(ctx context.Context, owner domain.UserID, name, url string, cred domain.CredentialID) (mlmodels.View, error)
	Delete(ctx context.Context, owner domain.UserID, name string) error
	Keys(ctx context.Context, owner domain.UserID, name string) ([]domain.WorkerKey, error)
	CreateKey(ctx context.Context, p auth.Principal, name, keyName string) (string, domain.WorkerKey, error)
	DeleteKey(ctx context.Context, owner domain.UserID, name string, id domain.WorkerKeyID) error
	Ask(ctx context.Context, owner domain.UserID, name string, items []json.RawMessage, schema json.RawMessage, wait time.Duration) (mlmodels.Answer, error)
	Limits() mlmodels.Limits

	// A worker's: the model its key serves, the next request, and its answer or failure.
	Worker(ctx context.Context, secret, address string) (domain.Model, error)
	Take(ctx context.Context, m domain.Model, w mlmodels.Worker) (mlmodels.Request, bool, error)
	Answer(ctx context.Context, m domain.Model, id string, w mlmodels.Worker, answers []json.RawMessage) error
	Fail(ctx context.Context, m domain.Model, id string, w mlmodels.Worker, reason string) error
}

type mlModelDTO struct {
	Name string `json:"name"`
	// Kind is worker, for a model whose workers take its requests, or url, for one called
	// at a URL.
	Kind string `json:"kind" enums:"worker,url"`
	// URL and CredentialID are a URL model's: where it is called, and the http.header
	// credential sent with each call.
	URL          string `json:"url,omitempty" validate:"optional"`
	CredentialID string `json:"credentialId,omitempty" validate:"optional"`
	// Workers are a worker model's workers connected now, by name.
	Workers      []connectedWorkerDTO `json:"workers"`
	LastAnswerAt *time.Time           `json:"lastAnswerAt,omitempty" validate:"optional" format:"date-time"`
	LastError    string               `json:"lastError,omitempty" validate:"optional"`
	LastErrorAt  *time.Time           `json:"lastErrorAt,omitempty" validate:"optional" format:"date-time"`
	CreatedAt    time.Time            `json:"createdAt" format:"date-time"`
	UpdatedAt    time.Time            `json:"updatedAt" format:"date-time"`
} // @name MLModel

type connectedWorkerDTO struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty" validate:"optional"`
	// Since is when it connected, this time; Seen when it last called for a request.
	Since time.Time `json:"since" format:"date-time"`
	Seen  time.Time `json:"seen" format:"date-time"`
	// Holding is how many requests it holds now.
	Holding int `json:"holding"`
} // @name ConnectedWorker

type mlModelListDTO struct {
	Models []mlModelDTO     `json:"models"`
	Limits mlModelLimitsDTO `json:"limits"`
} // @name MLModelList

// mlModelLimitsDTO are the instance's limits, for a form to say.
type mlModelLimitsDTO struct {
	Models       int `json:"models"`       // a user's
	Workers      int `json:"workers"`      // connected to a model at once
	Items        int `json:"items"`        // a request's
	RequestBytes int `json:"requestBytes"` // a request's
	AnswerBytes  int `json:"answerBytes"`  // an answer's
	// MaxWaitSeconds is the longest an asker may wait; PollSeconds how long a worker's call
	// for a request is held open.
	MaxWaitSeconds int `json:"maxWaitSeconds"`
	PollSeconds    int `json:"pollSeconds"`
} // @name MLModelLimits

type createMLModelRequest struct {
	// Name is lower-case letters, digits, - and _, up to 64; unique among yours, and fixed.
	Name string `json:"name"`
	Kind string `json:"kind" enums:"worker,url"`
	// URL and CredentialID are a URL model's; a worker model has neither.
	URL          string `json:"url" validate:"optional"`
	CredentialID string `json:"credentialId" validate:"optional"`
} // @name CreateMLModelRequest

type mlModelURLRequest struct {
	URL          string `json:"url"`
	CredentialID string `json:"credentialId" validate:"optional"`
} // @name MLModelURLRequest

type workerKeyDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Prefix is the key's start, to tell it apart.
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt" format:"date-time"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty" validate:"optional" format:"date-time"`
} // @name WorkerKey

type workerKeyListDTO struct {
	Keys []workerKeyDTO `json:"keys"`
} // @name WorkerKeyList

type createWorkerKeyRequest struct {
	Name string `json:"name"`
} // @name CreateWorkerKeyRequest

type newWorkerKeyDTO struct {
	Key workerKeyDTO `json:"key"`
	// Secret is the key itself, shown this once.
	Secret string `json:"secret"`
} // @name NewWorkerKey

//nolint:unused // the body as documented; askBody reads it
type askModelRequest struct {
	// Items are JSON objects, one per answer wanted.
	Items []map[string]any `json:"items"`
	// Schema is a JSON Schema each answer is to keep to; the model is told it.
	Schema map[string]any `json:"schema,omitempty" validate:"optional"`
	// WaitSeconds is how long to wait for the answers: 30 unless said, up to the instance's
	// most.
	WaitSeconds int `json:"waitSeconds,omitempty" validate:"optional" minimum:"1"`
} // @name AskModelRequest

// askBody is askModelRequest as read: its items' JSON as sent.
type askBody struct {
	Items       []json.RawMessage `json:"items"`
	Schema      json.RawMessage   `json:"schema"`
	WaitSeconds int               `json:"waitSeconds"`
}

type modelAnswersDTO struct {
	// Answers are one per item, in their order.
	Answers []any `json:"answers"`
	// Version is what the worker or the URL said it was; Worker the worker that answered.
	Version string `json:"version,omitempty" validate:"optional"`
	Worker  string `json:"worker,omitempty" validate:"optional"`
	// Refused are the answers that do not keep to the schema, by index, saying why.
	Refused map[string]string `json:"refused,omitempty" validate:"optional"`
} // @name ModelAnswers

type workerTakeRequest struct {
	// Worker is this worker's name, unique among the model's, like gpu-box-1.
	Worker string `json:"worker"`
	// Version is the model's version it serves, which each answer carries.
	Version string `json:"version" validate:"optional"`
} // @name WorkerTake

type workerRequestDTO struct {
	// Model is the model the key serves.
	Model   string        `json:"model"`
	Request workerTaskDTO `json:"request"`
} // @name WorkerRequest

type workerTaskDTO struct {
	ID string `json:"id"`
	// Items are the asker's, one answer each.
	Items []any `json:"items"`
	// Schema is what each answer is to keep to, if any.
	Schema any `json:"schema,omitempty" validate:"optional"`
	// Deadline is when its asker stops waiting.
	Deadline time.Time `json:"deadline" format:"date-time"`
} // @name WorkerTask

//nolint:unused // the body as documented; answerBody reads it
type workerAnswerRequest struct {
	Worker  string `json:"worker"`
	Version string `json:"version" validate:"optional"`
	// Answers are one per item, in their order: any JSON.
	Answers []any `json:"answers"`
} // @name WorkerAnswer

// answerBody is workerAnswerRequest as read: each answer's JSON as sent.
type answerBody struct {
	Worker  string            `json:"worker"`
	Version string            `json:"version"`
	Answers []json.RawMessage `json:"answers"`
}

type workerFailRequest struct {
	Worker  string `json:"worker"`
	Version string `json:"version" validate:"optional"`
	// Reason is why, up to 500 characters: each item fails with it.
	Reason string `json:"reason"`
} // @name WorkerFail

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

func toMLModelDTO(v mlmodels.View) mlModelDTO {
	d := mlModelDTO{
		Name: v.Name, Kind: string(v.Kind), URL: v.URL, CredentialID: string(v.Credential),
		Workers:      make([]connectedWorkerDTO, len(v.Workers)),
		LastAnswerAt: timeOrNil(v.LastAnswerAt), LastError: v.LastError, LastErrorAt: timeOrNil(v.LastErrorAt),
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}

	for i, w := range v.Workers {
		d.Workers[i] = connectedWorkerDTO{Name: w.Name, Version: w.Version, Since: w.Since, Seen: w.Seen, Holding: w.Holding}
	}

	return d
}

func toWorkerKeyDTO(k domain.WorkerKey) workerKeyDTO {
	return workerKeyDTO{ID: string(k.ID), Name: k.Name, Prefix: k.Prefix, CreatedAt: k.CreatedAt, LastUsedAt: timeOrNil(k.LastUsedAt)}
}

// asAny is raw JSON as a value that marshals back the same, its numbers exact.
func asAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}

	return v
}

// hasMLModels answers 404 where the server has no models.
func (s *Server) hasMLModels(ctx context.Context, c *app.RequestContext) {
	if s.mlModels == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}

// listMLModels lists your models, by name, with their workers connected now.
//
//	@Summary	List models
//	@ID			listMLModels
//	@Tags		models
//	@Success	200	{object}	mlModelListDTO
//	@Failure	403	{object}	errorBody	"data_off: an admin keeps models from you"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models [get]
func (s *Server) listMLModels(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.mlModels.List(ctx, p.User.ID)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	l := s.mlModels.Limits()
	out := mlModelListDTO{
		Models: make([]mlModelDTO, len(list)),
		Limits: mlModelLimitsDTO{
			Models: l.Models, Workers: l.Workers, Items: l.Items, RequestBytes: l.RequestBytes, AnswerBytes: l.AnswerBytes,
			MaxWaitSeconds: int(l.MaxWait / time.Second), PollSeconds: int(l.Poll / time.Second),
		},
	}

	for i, v := range list {
		out.Models[i] = toMLModelDTO(v)
	}

	c.JSON(http.StatusOK, out)
}

// createMLModel makes a model.
//
//	@Summary	Make a model
//	@ID			createMLModel
//	@Tags		models
//	@Param		body	body		createMLModelRequest	true	"The model"
//	@Success	201		{object}	mlModelDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	409		{object}	errorBody	"model_exists; model_limit: as many as you may have"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models [post]
func (s *Server) createMLModel(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req createMLModelRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	v, err := s.mlModels.Create(ctx, p.User.ID, mlmodels.Spec{
		Name: req.Name, Kind: domain.ModelKind(req.Kind), URL: req.URL, Credential: domain.CredentialID(req.CredentialID),
	})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toMLModelDTO(v))
}

// getMLModel is one of your models.
//
//	@Summary	Get a model
//	@ID			getMLModel
//	@Tags		models
//	@Param		name	path		string	true	"Its name"
//	@Success	200		{object}	mlModelDTO
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models/{name} [get]
func (s *Server) getMLModel(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	v, err := s.mlModels.Get(ctx, p.User.ID, c.Param("name"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toMLModelDTO(v))
}

// setMLModelURL changes where a URL model is called, and with what credential.
//
//	@Summary	Set a URL model's address
//	@ID			setMLModelURL
//	@Tags		models
//	@Param		name	path		string				true	"Its name"
//	@Param		body	body		mlModelURLRequest	true	"Its URL and credential"
//	@Success	200		{object}	mlModelDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models/{name} [put]
func (s *Server) setMLModelURL(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req mlModelURLRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	v, err := s.mlModels.SetURL(ctx, p.User.ID, c.Param("name"), req.URL, domain.CredentialID(req.CredentialID))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toMLModelDTO(v))
}

// deleteMLModel deletes a model and its keys: its workers are refused from then on.
//
//	@Summary	Delete a model
//	@ID			deleteMLModel
//	@Tags		models
//	@Param		name	path	string	true	"Its name"
//	@Success	204
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models/{name} [delete]
func (s *Server) deleteMLModel(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.mlModels.Delete(ctx, p.User.ID, c.Param("name")); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// askMLModel asks a model about items and waits for its answers, one an item, as a
// workflow does: to try a model out.
//
//	@Summary	Ask a model
//	@ID			askMLModel
//	@Tags		models
//	@Param		name	path		string			true	"Its name"
//	@Param		body	body		askModelRequest	true	"The items"
//	@Success	200		{object}	modelAnswersDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	409		{object}	errorBody	"model_offline: no worker connected"
//	@Failure	504		{object}	errorBody	"model_timeout: no answer in time"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models/{name}/ask [post]
func (s *Server) askMLModel(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req askBody
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	wait := mlmodels.DefaultWait
	if req.WaitSeconds > 0 {
		wait = time.Duration(req.WaitSeconds) * time.Second
	}

	a, err := s.mlModels.Ask(ctx, p.User.ID, c.Param("name"), req.Items, req.Schema, wait)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := modelAnswersDTO{Answers: make([]any, len(a.Answers)), Version: a.Version, Worker: a.Worker}
	for i, raw := range a.Answers {
		out.Answers[i] = asAny(raw)
	}

	for i, why := range a.Refused {
		if out.Refused == nil {
			out.Refused = map[string]string{}
		}

		out.Refused[strconv.Itoa(i)] = why.Error()
	}

	c.JSON(http.StatusOK, out)
}

// listWorkerKeys lists a worker model's keys, the newest first: never their secrets.
//
//	@Summary	List a model's worker keys
//	@ID			listWorkerKeys
//	@Tags		models
//	@Param		name	path		string	true	"Its name"
//	@Success	200		{object}	workerKeyListDTO
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models/{name}/keys [get]
func (s *Server) listWorkerKeys(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	keys, err := s.mlModels.Keys(ctx, p.User.ID, c.Param("name"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := workerKeyListDTO{Keys: make([]workerKeyDTO, len(keys))}
	for i, k := range keys {
		out.Keys[i] = toWorkerKeyDTO(k)
	}

	c.JSON(http.StatusOK, out)
}

// createWorkerKey makes a key a worker serves the model with, and that model alone: at a
// browser only, its secret given this once.
//
//	@Summary	Make a worker key
//	@ID			createWorkerKey
//	@Tags		models
//	@Param		name	path		string					true	"Its model's name"
//	@Param		body	body		createWorkerKeyRequest	true	"The key's name"
//	@Success	201		{object}	newWorkerKeyDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	403		{object}	errorBody	"forbidden: at a browser only"
//	@Security	SessionCookie
//	@Router		/models/{name}/keys [post]
func (s *Server) createWorkerKey(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	var req createWorkerKeyRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	secret, k, err := s.mlModels.CreateKey(ctx, p, c.Param("name"), req.Name)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, newWorkerKeyDTO{Key: toWorkerKeyDTO(k), Secret: secret})
}

// deleteWorkerKey revokes a worker key: its workers are refused from their next call.
//
//	@Summary	Revoke a worker key
//	@ID			deleteWorkerKey
//	@Tags		models
//	@Param		name	path	string	true	"Its model's name"
//	@Param		id		path	string	true	"The key's ID"
//	@Success	204
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/models/{name}/keys/{id} [delete]
func (s *Server) deleteWorkerKey(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.mlModels.DeleteKey(ctx, p.User.ID, c.Param("name"), domain.WorkerKeyID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

const workerModelKey = "worker_model"

// requireWorker signs a worker in with its model's worker key, as a bearer token: the
// worker routes take nothing else.
func (s *Server) requireWorker(ctx context.Context, c *app.RequestContext) {
	secret, ok := strings.CutPrefix(string(c.GetHeader("Authorization")), "Bearer ")
	if !ok {
		s.fail(ctx, c, domain.ErrUnauthenticated)
		c.Abort()

		return
	}

	m, err := s.mlModels.Worker(ctx, strings.TrimSpace(secret), c.ClientIP())
	if err != nil {
		s.fail(ctx, c, err)
		c.Abort()

		return
	}

	c.Set(workerModelKey, m)
	c.Next(ctx)
}

func workerModel(c *app.RequestContext) domain.Model {
	m, _ := c.Get(workerModelKey)
	model, _ := m.(domain.Model)

	return model
}

// workerNext is a worker's call for its model's next request: held open until one comes,
// or for the instance's poll, then 204 with none. Call again either way.
//
//	@Summary	Take the next request (a worker's)
//	@ID			workerNext
//	@Tags		workers
//	@Param		body	body		workerTakeRequest	true	"The worker"
//	@Success	200		{object}	workerRequestDTO
//	@Success	204		"none came: call again"
//	@Failure	401		{object}	errorBody	"unauthenticated: not a worker key"
//	@Failure	409		{object}	errorBody	"model_limit: as many workers connected as the model may have"
//	@Security	WorkerKey
//	@Router		/worker/next [post]
func (s *Server) workerNext(ctx context.Context, c *app.RequestContext) {
	var req workerTakeRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	m := workerModel(c)

	r, ok, err := s.mlModels.Take(ctx, m, mlmodels.Worker{Name: req.Worker, Version: req.Version})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	if !ok {
		c.Status(http.StatusNoContent)
		return
	}

	task := workerTaskDTO{ID: r.ID, Items: make([]any, len(r.Items)), Schema: asAny(r.Schema), Deadline: r.Deadline}
	for i, it := range r.Items {
		task.Items[i] = asAny(it)
	}

	c.JSON(http.StatusOK, workerRequestDTO{Model: m.Name, Request: task})
}

// workerAnswer answers a request: one answer an item, in their order. The first answer
// counts; a later one finds the request gone.
//
//	@Summary	Answer a request (a worker's)
//	@ID			workerAnswer
//	@Tags		workers
//	@Param		id		path	string				true	"The request's ID"
//	@Param		body	body	workerAnswerRequest	true	"The answers"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input: not one answer an item"
//	@Failure	404	{object}	errorBody	"not_found: answered already, or its asker stopped waiting"
//	@Security	WorkerKey
//	@Router		/worker/requests/{id}/answer [post]
func (s *Server) workerAnswer(ctx context.Context, c *app.RequestContext) {
	var req answerBody
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	err := s.mlModels.Answer(ctx, workerModel(c), c.Param("id"), mlmodels.Worker{Name: req.Worker, Version: req.Version}, req.Answers)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// workerFail fails a request, saying why: each of its items fails with the reason.
//
//	@Summary	Fail a request (a worker's)
//	@ID			workerFail
//	@Tags		workers
//	@Param		id		path	string				true	"The request's ID"
//	@Param		body	body	workerFailRequest	true	"Why"
//	@Success	204
//	@Failure	404	{object}	errorBody	"not_found: answered already, or its asker stopped waiting"
//	@Security	WorkerKey
//	@Router		/worker/requests/{id}/fail [post]
func (s *Server) workerFail(ctx context.Context, c *app.RequestContext) {
	var req workerFailRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	err := s.mlModels.Fail(ctx, workerModel(c), c.Param("id"), mlmodels.Worker{Name: req.Worker, Version: req.Version}, req.Reason)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
