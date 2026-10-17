package polymarket

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func TestToActivityEvent(t *testing.T) {
	row := pm.ActivityTrade{
		Type: pm.ActivityTrades, ProxyWallet: "0x17aa1297346d985adff54621a48d4a2a4ce2ba4f", Side: pm.SideBuy,
		Asset: "610", ConditionID: "0xcf9e", Outcome: "Up", Title: "BTC up?", Slug: "btc", EventSlug: "btc-event",
		Price: decimal.RequireFromString("0.35"), Size: decimal.NewFromInt(5), Timestamp: 1792243030,
		TransactionHash: "0x48e4", Name: "0x17aa1297346d985adff54621a48d4a2a4ce2ba4f-1792243030294", Pseudonym: "Simple-Director",
	}

	got, ok := toActivityEvent(row)
	assert.True(t, ok)
	assert.Equal(t, ports.ActivityEvent{Fill: domain.Fill{
		Wallet: "0x17aa1297346d985AdfF54621a48D4a2a4cE2ba4f", TxHash: "0x48e4", TokenID: "610", MarketID: "0xcf9e",
		Outcome: "Up", Title: "BTC up?", Slug: "btc", EventSlug: "btc-event", Side: domain.SideBuy,
		Price: decimal.RequireFromString("0.35"), Size: decimal.NewFromInt(5),
		At: time.Unix(1792243030, 0).UTC(), Name: "Simple-Director",
	}}, got, "checksummed as the domain writes addresses; a placeholder name gives way to the pseudonym")

	row.Type = pm.ActivityOrdersMatched
	got, _ = toActivityEvent(row)
	assert.True(t, got.Taker)

	row.ProxyWallet = "not one"
	_, ok = toActivityEvent(row)
	assert.False(t, ok, "a row without a wallet is no one's")

	got, ok = toActivityEvent(pm.ActivityConnected{At: time.Now()})
	assert.True(t, ok)
	assert.True(t, got.Connected)
}

func TestTraderName(t *testing.T) {
	assert.Equal(t, "BOOOST", traderName("BOOOST", "Simple-Director"))
	assert.Equal(t, "Simple-Director", traderName("", "Simple-Director"))
	assert.Equal(t, "Simple-Director", traderName("0x17aa1297346d985adff54621a48d4a2a4ce2ba4f-1792243030294", "Simple-Director"))
}
