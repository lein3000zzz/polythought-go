package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestMonthOf(t *testing.T) {
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, first, domain.MonthOf(time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)), "the last second still October")
	assert.Equal(t, first.AddDate(0, 1, 0), domain.MonthOf(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)))

	tokyo := time.FixedZone("JST", 9*60*60)
	assert.Equal(t, first, domain.MonthOf(time.Date(2026, 11, 1, 8, 0, 0, 0, tokyo)), "UTC's month, wherever the clock is")
}

func TestAgentUsageTotal(t *testing.T) {
	assert.Equal(t, int64(15), domain.AgentUsage{InputTokens: 10, OutputTokens: 5}.TotalTokens())
}
