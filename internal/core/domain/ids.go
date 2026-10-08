package domain

// Distinct ID types, so a token ID cannot be passed where a market ID is expected.
type (
	EventID string

	// MarketID is the condition ID, the key shared by every Polymarket API.
	MarketID string

	// TokenID identifies one tradable outcome of a market.
	TokenID string

	WalletAddress string
)
