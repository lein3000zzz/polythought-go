package domain

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	// ErrCredentialsRejected means the exchange refused a wallet's API credentials.
	ErrCredentialsRejected = errors.New("polymarket rejected the credentials")

	// ErrUnavailable means an upstream service could not be reached; trying later may work.
	ErrUnavailable = errors.New("upstream service unavailable")
)

// Order is one of an account's orders on the book. Sizes are shares.
type Order struct {
	ID        string
	MarketID  MarketID
	TokenID   TokenID
	Outcome   string
	Side      Side
	Price     decimal.Decimal
	Size      decimal.Decimal // as placed
	Filled    decimal.Decimal
	Status    string // live, matched, canceled, ...
	Type      string // GTC, GTD, FOK, FAK
	CreatedAt time.Time
	ExpiresAt time.Time // zero for orders that do not expire
}

// Remaining is the size still open on the book.
func (o Order) Remaining() decimal.Decimal {
	return o.Size.Sub(o.Filled)
}
