package ports

import "context"

// WebhookSender posts JSON to addresses users put in their workflows. Implementations
// refuse addresses inside the server's own network unless the operator allows them.
// header adds headers to the request, such as a credential's Authorization; a redirect
// to another host does not carry them.
type WebhookSender interface {
	Post(ctx context.Context, url string, body []byte, header map[string]string) error
}
