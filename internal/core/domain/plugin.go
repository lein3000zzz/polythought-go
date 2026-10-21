package domain

import "errors"

// ErrStoreFull means a plugin node's store has no room for a value (D303).
var ErrStoreFull = errors.New("the node's store is full")
