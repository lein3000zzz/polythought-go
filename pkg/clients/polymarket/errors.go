package polymarket

import (
	"errors"
	"fmt"
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
