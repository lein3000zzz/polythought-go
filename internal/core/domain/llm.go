package domain

import "errors"

// ErrProviderFailed means a language model provider refused or failed a call made with a
// user's key: the error goes on in its words, such as "Anthropic answered 401: invalid
// x-api-key".
var ErrProviderFailed = errors.New("the model provider failed")
