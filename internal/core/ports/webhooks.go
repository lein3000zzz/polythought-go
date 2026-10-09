package ports

import "context"

// WebhookSender posts JSON to addresses users put in their workflows. Implementations
// refuse addresses inside the server's own network unless the operator allows them.
type WebhookSender interface {
	Post(ctx context.Context, url string, body []byte) error
}
