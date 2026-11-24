package ports

import (
	"context"
	"time"
)

// HTTPRequester makes the requests of HTTP request nodes (D438), to addresses users put in
// their workflows: guarded as webhooks are, refusing addresses inside the server's own
// network unless the operator allows them, through the outbound proxy where one is set.
// Its errors name the host, never the whole URL, which may carry a key.
type HTTPRequester interface {
	Do(ctx context.Context, req HTTPRequest) (HTTPResponse, error)
}

type HTTPRequest struct {
	Method string
	URL    string
	// Header holds the request's headers by name; a redirect to another host or scheme
	// carries only those that say nothing of the user, such as Accept.
	Header map[string]string
	Body   []byte
	// Timeout bounds the whole request, its redirects and its body; zero for the
	// requester's own.
	Timeout time.Duration
}

// HTTPResponse is any answer, whatever its status: one not 2xx is no error here.
type HTTPResponse struct {
	Status int
	Header map[string][]string
	Body   []byte
}
