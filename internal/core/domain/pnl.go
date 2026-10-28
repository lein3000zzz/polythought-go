package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// PnLSample is a workflow's profit and loss in a mode at a time (D430), as its stats work
// it out; Since is what it counts from: its paper account's start or last reset, the zero
// time live.
type PnLSample struct {
	WorkflowID WorkflowID
	Mode       TradingMode
	Since      time.Time
	Profit     decimal.Decimal
	At         time.Time
}

// PnLExtremes are a workflow's highest and lowest profit and loss in a mode since Since,
// each with when, as the samples found them.
type PnLExtremes struct {
	WorkflowID WorkflowID
	Mode       TradingMode
	Since      time.Time
	High       decimal.Decimal
	HighAt     time.Time
	Low        decimal.Decimal
	LowAt      time.Time
}
