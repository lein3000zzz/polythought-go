package domain

import "errors"

// ErrNotFound means the requested market, event or other entity does not exist.
var ErrNotFound = errors.New("not found")
