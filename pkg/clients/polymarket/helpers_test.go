package polymarket

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

// Metric names registered by redkit and the stream client.
const (
	metricOperationsTotal = "operations_total"
	metricEventsDropped   = "events_dropped_total"
	metricReconnects      = "reconnects_total"
)

// newMockedUtil returns a Utility whose metrics manager hands out the given counters by
// metric name. Every other counter, gauge and histogram accepts any call, so a test
// asserts only the metrics it names.
func newMockedUtil(t *testing.T, counters map[string]*metricsmocks.MockCounter) *observability.Utility {
	t.Helper()

	manager := metricsmocks.NewMockManager(t)

	manager.EXPECT().RegisterCounter(mock.Anything).
		RunAndReturn(func(d metrics.CounterDefinition) (metrics.Counter, error) {
			if c, ok := counters[d.Name]; ok {
				return c, nil
			}

			c := metricsmocks.NewMockCounter(t)
			c.EXPECT().Inc(mock.Anything).Return(nil).Maybe()

			return c, nil
		}).Maybe()

	manager.EXPECT().RegisterGauge(mock.Anything).
		RunAndReturn(func(metrics.GaugeDefinition) (metrics.Gauge, error) {
			g := metricsmocks.NewMockGauge(t)
			g.EXPECT().Inc(mock.Anything).Return(nil).Maybe()
			g.EXPECT().Dec(mock.Anything).Return(nil).Maybe()

			return g, nil
		}).Maybe()

	manager.EXPECT().RegisterHistogram(mock.Anything).
		RunAndReturn(func(metrics.HistogramDefinition) (metrics.Histogram, error) {
			h := metricsmocks.NewMockHistogram(t)
			h.EXPECT().Observe(mock.Anything, mock.Anything).Return(nil).Maybe()

			return h, nil
		}).Maybe()

	return observability.NewUtility(logging.Noop(), manager, observability.NoopUtility().TracerManager)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	return b
}
