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

	// Warned and Capped are the caps the month's 80% and cap notices went out at, empty
	// before they have (D274): raised since, a notice may go again.
	Warned, Capped string
}

func (u AgentUsage) TotalTokens() int64 { return u.InputTokens + u.OutputTokens }

// MonthOf is the month t falls in, as agent spend counts it: UTC's.
func MonthOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// SpendNotice is one of a month's notices about agent spend (D274).
type SpendNotice string

const (
	NoticeWarned SpendNotice = "warned" // 80% of a cap
	NoticeCapped SpendNotice = "capped" // a cap reached, a call refused
)

// SpendAlert is a notice that a user's agents reached 80% of a monthly cap, or a cap.
type SpendAlert struct {
	Notice SpendNotice
	What   string // which cap: dollars, input tokens, output tokens or tokens
	Cap    string // as people read it: $20.00, 20,000,000
	Used   string
	Resets time.Time // when the month ends
}

// AgentCaps cap what agents spend (D271, D272): dollars, to the cent, as providers report
// them (D270), and input, output and total tokens. On a user's, nil is the instance's and
// 0 none; on a node run's, nil and 0 are both none.
type AgentCaps struct {
	Dollars      *decimal.Decimal
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
}

// Over is the user's caps, or the instance's where they have none of their own.
func (c AgentCaps) Over(instance AgentCaps) AgentCaps {
	if c.Dollars == nil {
		c.Dollars = instance.Dollars
	}

	if c.InputTokens == nil {
		c.InputTokens = instance.InputTokens
	}

	if c.OutputTokens == nil {
		c.OutputTokens = instance.OutputTokens
	}

	if c.TotalTokens == nil {
		c.TotalTokens = instance.TotalTokens
	}

	return c
}

// None reports whether no cap applies.
func (c AgentCaps) None() bool {
	return !capped(c.InputTokens) && !capped(c.OutputTokens) && !capped(c.TotalTokens) &&
		(c.Dollars == nil || !c.Dollars.IsPositive())
}

func capped(n *int64) bool { return n != nil && *n > 0 }
