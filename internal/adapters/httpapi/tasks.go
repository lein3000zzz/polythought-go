package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/tasks"
)

// TaskService is the tasks of the user's Local agent nodes, as the app shows them (D413).
type TaskService interface {
	Status(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID) (tasks.Status, error)
	HandBack(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID) error
	AnswerYourself(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID, answer any, answers []any) error
}

// TaskAgents is which of the user's grants and tokens take tasks now, by holder (D413).
type TaskAgents interface {
	Taking(owner domain.UserID) map[string]bool
}

// executionTaskDTO is the task a run waits on, and who has it.
type executionTaskDTO struct {
	// Call is each, an answer for each item, or together, one for all of them.
	Call     string `json:"call" enums:"each,together"`
	AnswerAs string `json:"answerAs" enums:"text,fields,schema"`
	// Schema is the JSON schema an answer keeps to, answering in fields or to a schema.
	Schema map[string]any `json:"schema,omitempty" validate:"optional"`
	// Prompt is what it asks, together; each item has its own.
	Prompt string        `json:"prompt,omitempty" validate:"optional"`
	Items  []taskItemDTO `json:"items"`
	// Until is when the run stops waiting, and its items fail.
	Until time.Time `json:"until" format:"date-time"`
	// HeldBy is the agent or token that has it, by its name, until heldUntil; absent while
	// it waits for one to take it.
	HeldBy    string     `json:"heldBy,omitempty" validate:"optional"`
	HeldUntil *time.Time `json:"heldUntil,omitempty" validate:"optional" format:"date-time"`
} // @name ExecutionTask

type taskItemDTO struct {
	Prompt string         `json:"prompt,omitempty" validate:"optional"`
	Item   map[string]any `json:"item"`
} // @name TaskItem

// answerTaskRequest is the user's answer: answers, one for each item in order, asking each;
// answer, asking together. Each is a string answering as text, else JSON keeping to the
// schema.
type answerTaskRequest struct {
	Answer  any   `json:"answer,omitempty" validate:"optional"`
	Answers []any `json:"answers,omitempty" validate:"optional"`
} // @name AnswerTaskRequest

func toExecutionTaskDTO(st tasks.Status) executionTaskDTO {
	t := st.Task.Task

	out := executionTaskDTO{
		Call: t.Call, AnswerAs: t.AnswerAs, Prompt: t.Prompt, Items: make([]taskItemDTO, len(t.Items)), Until: st.Until,
		HeldBy: st.HeldBy,
	}

	if st.HeldBy != "" {
		out.HeldUntil = optionalTime(st.LeaseUntil)
	}

	if len(t.Schema) > 0 {
		_ = json.Unmarshal(t.Schema, &out.Schema)
	}

	for i, it := range t.Items {
		item := it.Item
		if item == nil {
			item = map[string]any{}
		}

		out.Items[i] = taskItemDTO{Prompt: it.Prompt, Item: item}
	}

	return out
}

// getExecutionTask is the task a run waits on at a Local agent node (D413).
//
//	@Summary	The task a run waits on
//	@ID			getExecutionTask
//	@Tags		executions
//	@Param		id	path		string	true	"Workflow ID"
//	@Param		eid	path		string	true	"Execution ID"
//	@Success	200	{object}	executionTaskDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found: it waits on no task"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/executions/{eid}/task [get]
func (s *Server) getExecutionTask(ctx context.Context, c *app.RequestContext) {
	if s.tasks == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	p, _ := principal(c)

	st, err := s.tasks.Status(ctx, p, domain.WorkflowID(c.Param("id")), domain.ExecutionID(c.Param("eid")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toExecutionTaskDTO(st))
}

// handBackTask takes a run's task from the agent that has it, for another of yours to take
// at once (D413), at a browser only.
//
//	@Summary	Hand a task back
//	@ID			handBackTask
//	@Tags		executions
//	@Param		id	path	string	true	"Workflow ID"
//	@Param		eid	path	string	true	"Execution ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not from a browser session"
//	@Failure	404	{object}	errorBody	"not_found: it waits on no task"
//	@Security	SessionCookie
//	@Router		/workflows/{id}/executions/{eid}/task/hand-back [post]
func (s *Server) handBackTask(ctx context.Context, c *app.RequestContext) {
	if s.tasks == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	p, _ := principal(c)

	if err := s.tasks.HandBack(ctx, p, domain.WorkflowID(c.Param("id")), domain.ExecutionID(c.Param("eid"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// answerTask answers a run's task yourself, held to its shape as an agent's answer is
// (D413), at a browser only; the run carries on in the background.
//
//	@Summary	Answer a task yourself
//	@ID			answerTask
//	@Tags		executions
//	@Param		id		path	string				true	"Workflow ID"
//	@Param		eid		path	string				true	"Execution ID"
//	@Param		body	body	answerTaskRequest	true	"The answer"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input: it does not keep to the task's shape"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not from a browser session"
//	@Failure	404	{object}	errorBody	"not_found: it waits on no task"
//	@Security	SessionCookie
//	@Router		/workflows/{id}/executions/{eid}/task/answer [post]
func (s *Server) answerTask(ctx context.Context, c *app.RequestContext) {
	if s.tasks == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	var req answerTaskRequest
	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	err := s.tasks.AnswerYourself(ctx, p, domain.WorkflowID(c.Param("id")), domain.ExecutionID(c.Param("eid")), req.Answer, req.Answers)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// takingTasks says whether the user's grant or token takes tasks now (D413).
func (s *Server) takingTasks(owner domain.UserID) func(holder string) bool {
	if s.taskAgents == nil {
		return func(string) bool { return false }
	}

	taking := s.taskAgents.Taking(owner)

	return func(holder string) bool { return taking[holder] }
}
