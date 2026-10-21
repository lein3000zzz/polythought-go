package domain

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// ErrProviderFailed means a language model provider refused or failed a call made with a
// user's key: the error goes on in its words, such as "Anthropic answered 401: invalid
// x-api-key".
var ErrProviderFailed = errors.New("the model provider failed")

// AgentUsage is what a user's agent nodes spent in a month (D268): every call's tokens,
// and the dollars providers reported, which not all of them do (D270).
type AgentUsage struct {
	Month        time.Time // 00:00 UTC on its first day
	InputTokens  int64
	OutputTokens int64
	Dollars      decimal.Decimal
	Calls        int
	CostedCalls  int // those a provider reported dollars for
}

func (u AgentUsage) TotalTokens() int64 { return u.InputTokens + u.OutputTokens }

// MonthOf is the month t falls in, as agent spend counts it: UTC's.
func MonthOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
