package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/http1/resp"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/events"
)

// EventService is what the handlers need to stream a user's events.
type EventService interface {
	Subscribe(user domain.UserID) (*events.Subscription, error)
}

// heartbeat keeps a quiet stream open through proxies, and lets the server notice a
// client that went away: Hertz does not tell handlers, a failed write does.
const heartbeat = 25 * time.Second

type executionEventDTO struct {
	WorkflowID string       `json:"workflowId"`
	Execution  executionDTO `json:"execution"`
} // @name ExecutionEvent

// streamEvents sends the user's events as server-sent events until the client leaves.
//
//	@Summary		Live events
//	@Description	A text/event-stream. Every finished execution of the user's workflows arrives as an "execution" event, its data an ExecutionEvent without item samples. Comment lines keep the stream open.
//	@ID				streamEvents
//	@Tags			events
//	@Produce		text/event-stream
//	@Success		200	{object}	executionEventDTO
//	@Failure		401	{object}	errorBody	"unauthenticated"
//	@Failure		429	{object}	errorBody	"rate_limited: too many streams open"
//	@Security		SessionCookie
//	@Security		APIToken
//	@Router			/events [get]
func (s *Server) streamEvents(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	sub, err := s.events.Subscribe(p.User.ID)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}
	defer sub.Close()

	c.SetStatusCode(http.StatusOK)
	c.Response.Header.SetContentType("text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("X-Accel-Buffering", "no") // nginx and friends: do not buffer

	w := resp.NewChunkedBodyWriter(&c.Response, c.GetWriter())
	c.Response.HijackWriter(w)

	// send reports whether the chunk reached the client; c.Write would hide the error.
	send := func(chunk string) bool {
		if _, err := w.Write([]byte(chunk)); err != nil {
			return false
		}

		return w.Flush() == nil
	}

	// A browser that loses the stream reconnects after this many milliseconds.
	if !send("retry: 5000\n\n") {
		return
	}

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()

	for {
		select {
		case <-s.closing:
			return

		case <-tick.C:
			if !send(": ping\n\n") {
				return
			}

		case ev, ok := <-sub.Events():
			if !ok {
				return
			}

			data, err := json.Marshal(executionEventDTO{
				WorkflowID: string(ev.Execution.WorkflowID),
				Execution:  toExecutionDTO(ev.Execution, false),
			})
			if err != nil {
				s.log.Error(ctx, "Event not encoded", "execution_id", ev.Execution.ID, logging.Err(err))
				continue
			}

			if !send("event: execution\ndata: " + string(data) + "\n\n") {
				return
			}
		}
	}
}
