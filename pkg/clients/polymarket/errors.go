package polymarket

import (
	"context"
	"errors"
	"fmt"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
)

var ErrNotFound = errors.New("polymarket: not found")

// APIError is a non-2xx response that is not mapped to a sentinel error.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("polymarket: http %d: %s", e.StatusCode, e.Body)
}

// succeeded is the RED outcome of a call. Not found is an answer, not a failure.
func succeeded(err error) bool {
	return err == nil || errors.Is(err, ErrNotFound)
}

// failed logs err inside the operation's span and returns it unchanged.
// Not found and caller cancellation are routine and stay at Debug.
func failed(ctx context.Context, log logging.Logger, op string, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		log.Debug(ctx, "Polymarket resource not found", "operation", op, logging.Err(err))
	case ctx.Err() != nil:
		log.Debug(ctx, "Polymarket call cancelled", "operation", op, logging.Err(err))
	default:
		log.Error(ctx, "Polymarket call failed", "operation", op, logging.Err(err))
	}

	return err
}
