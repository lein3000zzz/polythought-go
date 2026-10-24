package ai

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const LocalAgentType = "ai.localAgent"

// maxTask is the most a task may be, as JSON: an agent takes it in one tool call's answer,
// which is held to 32 KiB, with room for what the call adds.
const maxTask = 24 << 10

// What a local agent's answer brings back, as the waits service gives it to Resume.
const (
	TaskAnswers = "answers" // each: an answer per item, in order
	TaskAnswer  = "answer"  // together: the one answer
	TaskFailed  = "failed"  // why the agent could not, when it said so
)

var _ engine.Resumer = (*LocalAgent)(nil)

// LocalAgent hands a task to one of its owner's own agents, such as Claude Code connected
// over MCP with the tasks scope (D386): a prompt for each item or for all of them, and the
// answer's shape, as the AI agent node's. The run waits for one of them to answer; with
// none waiting for tasks, it fails at once, as it does at its timeout. Polythought runs no
// agent itself and holds no login of the user's.
type LocalAgent struct {
	agents ports.TaskAgents // nil: no agent ever takes one
	now    func() time.Time
}

func NewLocalAgent(agents ports.TaskAgents, now func() time.Time) *LocalAgent {
	return &LocalAgent{agents: agents, now: now}
}

func (*LocalAgent) Type() engine.NodeType {
	return engine.NodeType{
		Name:  LocalAgentType,
		Label: "Local agent",
		Description: "Hands a task to one of your own agents, such as Claude Code or Codex connected with the tasks scope, " +
			"and waits for its answer: for each item or once for all of them, as text or as JSON in the shape you give. " +
			"Fails at once when none of your agents is waiting for tasks.",
		Inputs:    []string{engine.DefaultPort},
		Outputs:   []string{engine.DefaultPort},
		SeesItems: true,
		Items: &engine.Items{
			Passed: true, Named: "field", Alone: engine.Choice{Param: "call", Value: CallTogether},
			Fields: []engine.Field{localAnswerField},
		},
		Params: []engine.ParamSpec{
			{
				Name: "call", Label: "Ask", Kind: engine.ParamString, Default: CallEach, Options: []string{CallEach, CallTogether},
				Description: "each: an answer per item, added to it. together: one answer for all of them, and one item out, " +
					"the answer alone.",
			},
			{
				Name: "prompt", Label: "Task", Kind: engine.ParamString, Required: true, Multiline: true,
				Description: "What to do, like Read up on {{ question }} and say whether it is worth watching. Taking them " +
					"together, {{ items }} are all of them. Your agent reads it as a request from this workflow.",
			},
			{
				Name: "include", Label: "Include the items", Kind: engine.ParamBool, Default: true,
				Description: "Taking them together: the items go with the task, as JSON. Each item always goes with its own.",
			},
			{
				Name: "answerAs", Label: "Answer as", Kind: engine.ParamString, Default: AnswerText,
				Options:     []string{AnswerText, AnswerFields, AnswerSchema},
				Description: "text as it is written; fields, the Fields below; schema, the JSON schema below.",
			},
			{
				Name: "fields", Label: "Fields", Kind: engine.ParamTable, Columns: fieldColumns,
				Description: "Answering in fields: a row each, like watch, true or false.",
			},
			{
				Name: "schema", Label: "JSON schema", Kind: engine.ParamString, Multiline: true,
				Description: "Answering to a schema: the answer's, for nested shapes.",
			},
			{
				Name: "field", Label: "Answer field", Kind: engine.ParamString, Default: "answer",
				Description: "Where the answer goes on the item.",
			},
			{
				Name: "timeout", Label: "Timeout", Kind: engine.ParamDuration, Default: "10m",
				Description: "How long to wait for the answer before the items fail, e.g. 10m or 2h. At most the instance's " +
					"longest wait, 7 days unless an admin changed it.",
			},
		},
	}
}

// localAnswerField is what the node adds, under the answer field's name.
var localAnswerField = engine.Field{Name: "answer", Kind: engine.FieldObject, Description: "What your agent answered.", Fields: []engine.Field{
	shape.Optional(shape.Text("text", "The answer as the agent wrote it, answering as text.")),
	{Name: "json", Kind: engine.FieldAny, Optional: true, Description: "The answer as JSON, answering in fields or to a schema."},
	shape.Text("by", "Who answered: your agent by its name, or you, answering in the app."),
	shape.Time("answeredAt", "When it answered."),
}}

// localState is what the node keeps while it waits.
type localState struct {
	Call     string          `json:"call"`
	AnswerAs string          `json:"answerAs"`
	Schema   json.RawMessage `json:"schema,omitempty"`
	Field    string          `json:"field"`
	Timeout  string          `json:"timeout"`
}

func (n *LocalAgent) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return engine.Ports{}, nil
	}

	first := engine.ItemRef{Port: engine.DefaultPort, Index: 0}

	p, err := rc.Params.Resolve(first)
	if err != nil {
		return nil, err
	}

	st, timeout, err := readLocal(p)
	if err != nil {
		return nil, err
	}

	if n.agents == nil || !n.agents.Around(rc.OwnerID) {
		return failAll(items, st.Call, errors.New("none of your agents is waiting for tasks: connect one with the tasks scope "+
			"and have it call wait_for_task")), nil
	}

	task := &ports.WaitTask{Call: st.Call, AnswerAs: st.AnswerAs, Schema: st.Schema}

	if st.Call == CallTogether {
		if task.Prompt, err = promptOf(p); err != nil {
			return nil, err
		}

		if include, _ := p.Bool("include"); include {
			for _, it := range items {
				task.Items = append(task.Items, ports.TaskItem{Item: it.JSON})
			}
		}
	} else {
		for i, it := range items {
			p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort, Index: i})
			if err != nil {
				return nil, err
			}

			prompt, err := promptOf(p)
			if err != nil {
				return nil, err
			}

			task.Items = append(task.Items, ports.TaskItem{Prompt: prompt, Item: it.JSON})
		}
	}

	if data, err := json.Marshal(task); err != nil {
		return nil, err
	} else if len(data) > maxTask {
		return nil, fmt.Errorf("the task is %d KiB, more than an agent takes at once (%d KiB): send fewer items, or fewer fields of each",
			len(data)>>10, maxTask>>10)
	}

	key, err := taskKey()
	if err != nil {
		return nil, err
	}

	state, err := toMap(st)
	if err != nil {
		return nil, err
	}

	return nil, &engine.Wait{Until: n.now().Add(timeout), Key: key, Task: task, State: state}
}

// Resume sends the items on with the agent's answer, or fails them with why there is none.
func (n *LocalAgent) Resume(_ context.Context, _ engine.RunContext, in engine.Ports, r engine.Resumption) (engine.Ports, error) {
	var st localState
	if err := fromMap(r.State, &st); err != nil {
		return nil, err
	}

	items := in[engine.DefaultPort]

	switch {
	case r.TimedOut:
		return failAll(items, st.Call, fmt.Errorf("none of your agents answered within %s", st.Timeout)), nil
	case r.Payload[TaskFailed] != nil:
		why, _ := r.Payload[TaskFailed].(string)
		return failAll(items, st.Call, fmt.Errorf("%s could not do it: %s", agentName(r), why)), nil
	}

	task := ports.WaitTask{AnswerAs: st.AnswerAs, Schema: st.Schema}
	at := n.now().UTC().Format(time.RFC3339)
	out := engine.Ports{}

	if st.Call == CallTogether {
		answer, err := CheckAnswer(task, r.Payload[TaskAnswer])
		if err != nil {
			return failAll(items, st.Call, err), nil
		}

		srcs := make([]engine.ItemRef, len(items))
		for i := range items {
			srcs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
		}

		out.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{st.Field: answerOf(st, answer, r, at)}, Source: srcs})

		return out, nil
	}

	answers, _ := r.Payload[TaskAnswers].([]any)
	if len(answers) != len(items) {
		return failAll(items, st.Call, fmt.Errorf("%d answers for %d items", len(answers), len(items))), nil
	}

	for i, it := range items {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		answer, err := CheckAnswer(task, answers[i])
		if err != nil {
			out.Fail(it, src, err)
			continue
		}

		j := maps.Clone(it.JSON)
		if j == nil {
			j = map[string]any{}
		}

		j[st.Field] = answerOf(st, answer, r, at)
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// CheckAnswer holds an agent's answer to a task's shape (D386): text for one answering as
// text; else JSON keeping to its schema, given as JSON or as text holding it.
func CheckAnswer(t ports.WaitTask, answer any) (any, error) {
	if t.AnswerAs == AnswerText || t.AnswerAs == "" {
		text, ok := answer.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, errors.New("the answer is text: write it as a string")
		}

		return text, nil
	}

	v, err := compile(t.Schema)
	if err != nil {
		return nil, fmt.Errorf("the task's schema: %w", err)
	}

	if text, ok := answer.(string); ok {
		return v.check(text)
	}

	// Through JSON, so numbers and the like are as the schema reads them.
	data, err := json.Marshal(answer)
	if err != nil {
		return nil, err
	}

	return v.check(string(data))
}

func readLocal(p engine.Params) (localState, time.Duration, error) {
	st := localState{Call: CallEach}

	text := func(name string) string {
		v, _ := p.String(name)
		return v
	}

	if call := text("call"); call == CallTogether {
		st.Call = CallTogether
	} else if call != CallEach && call != "" {
		return st, 0, fmt.Errorf("ask: %q is not each or together", call)
	}

	if st.Field = strings.TrimSpace(text("field")); st.Field == "" {
		return st, 0, errors.New("answer field: name where the answer goes")
	}

	timeout, err := p.Duration("timeout")
	if err != nil || timeout <= 0 {
		return st, 0, errors.New("timeout: how long to wait, like 10m")
	}

	st.Timeout = timeout.String()

	switch st.AnswerAs = text("answerAs"); st.AnswerAs {
	case AnswerText, "":
		st.AnswerAs = AnswerText
	case AnswerFields:
		rows, err := p.Table("fields", fieldColumns)
		if err != nil {
			return st, 0, err
		}

		if st.Schema, err = fieldsSchema(rows); err != nil {
			return st, 0, fmt.Errorf("fields: %w", err)
		}
	case AnswerSchema:
		v, err := compile(json.RawMessage(text("schema")))
		if err != nil {
			return st, 0, fmt.Errorf("JSON schema: %w", err)
		}

		st.Schema = v.raw
	default:
		return st, 0, fmt.Errorf("answer as: %q is not text, fields or schema", st.AnswerAs)
	}

	return st, timeout, nil
}

func promptOf(p engine.Params) (string, error) {
	prompt, _ := p.String("prompt")
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("task: what to ask is empty")
	}

	return prompt, nil
}

// failAll fails every item with err: one by one answering each, as one together.
func failAll(items []engine.Item, call string, err error) engine.Ports {
	out := engine.Ports{}

	if call == CallTogether {
		srcs := make([]engine.ItemRef, len(items))
		for i := range items {
			srcs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
		}

		out.Add(engine.ErrorPort, engine.Item{JSON: map[string]any{}, Source: srcs, Err: err})

		return out
	}

	for i, it := range items {
		out.Fail(it, engine.ItemRef{Port: engine.DefaultPort, Index: i}, err)
	}

	return out
}

func answerOf(st localState, answer any, r engine.Resumption, at string) map[string]any {
	out := map[string]any{"by": agentName(r), "answeredAt": at}

	if st.AnswerAs == AnswerText {
		out["text"] = answer
	} else {
		out["json"] = answer
	}

	return out
}

// agentName is the agent that answered, by its name.
func agentName(r engine.Resumption) string {
	if by := r.By(); by != "" {
		return by
	}

	return "your agent"
}

// taskKey is a random key for a task's wait, so an answer to another cannot answer it.
func taskKey() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

func toMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var m map[string]any

	return m, json.Unmarshal(data, &m)
}

func fromMap(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}
