package triggers

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const WebhookType = "trigger.webhook"

var _ engine.Trigger = (*Webhook)(nil)

// Webhook starts its workflow for each call to its secret address (D439), one run after
// another: the call's body and query on the item, the run taking the ID the caller was
// given.
type Webhook struct {
	inbox ports.HookInbox
}

func NewWebhook(inbox ports.HookInbox) *Webhook {
	return &Webhook{inbox: inbox}
}

func (*Webhook) Type() engine.NodeType {
	return engine.NodeType{
		Name:  WebhookType,
		Label: "Inbound webhook",
		Description: "Starts the workflow when a service calls its secret address with a POST, the call's body and " +
			"query on the item. The address is in the node's settings once the workflow is saved.",
		Outputs: []string{engine.DefaultPort},
		Items: shape.Own(
			engine.Field{Name: "body", Kind: engine.FieldAny, Description: "The call's body: what its JSON holds, read, or else its text; null for none."},
			engine.Field{
				Name: "query", Kind: engine.FieldAny,
				Description: "The address's query, by name: ?city=nyc is query.city; a name given more than once, a list.",
			},
			shape.Time("receivedAt", "When the call came."),
		),
	}
}

// Start takes the calls until ctx is done, each a run, the next once it ends.
func (h *Webhook) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	calls, cancel := h.inbox.Listen(tc.WorkflowID, tc.NodeID)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil
		case c, ok := <-calls:
			if !ok {
				return nil
			}

			_ = emit(engine.WithExecutionID(ctx, c.Execution), WebhookOutput(c))
		}
	}
}

// WebhookOutput is what the trigger emits for a call.
func WebhookOutput(c ports.HookCall) engine.Ports {
	query := c.Query
	if query == nil {
		query = map[string]any{}
	}

	return engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"body": c.Body, "query": query, "receivedAt": c.At.UTC()}}}}
}
