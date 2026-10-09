package engine

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// samplesPerPort and sampleBudget bound what an execution keeps of a node's output:
// enough to see what flowed through, small enough to keep many executions.
const (
	samplesPerPort = 5
	sampleBudget   = 16 << 10 // bytes of item JSON per node run
)

// ExecuteOption changes one Execute call.
type ExecuteOption func(*executeOptions)

type executeOptions struct {
	samples bool
}

// KeepSamples keeps the first items each node emits, for inspecting the execution later.
// It suits runs started by hand; triggers that fire every few seconds would fill the
// database with them.
func KeepSamples() ExecuteOption {
	return func(o *executeOptions) { o.samples = true }
}

// sample keeps the first items of each port of out. The error port goes first, since
// failed items explain a failure, then main, then the rest by name: when the budget runs
// out, it is the least telling ports that go without. A port's sampling stops at the
// first item that no longer fits.
func sample(out Ports) map[string][]domain.ItemSample {
	if out.Len() == 0 {
		return nil
	}

	ports := slices.SortedFunc(maps.Keys(out), comparePorts)

	samples := make(map[string][]domain.ItemSample, len(ports))
	budget := sampleBudget

	for _, port := range ports {
		for _, item := range out[port][:min(len(out[port]), samplesPerPort)] {
			s := sampleOf(item)

			size := len(s.JSON) + len(s.Error)
			if size > budget {
				break
			}

			budget -= size
			samples[port] = append(samples[port], s)
		}
	}

	return samples
}

func sampleOf(item Item) domain.ItemSample {
	var s domain.ItemSample

	if item.Err != nil {
		s.Error = item.Err.Error()
	}

	raw, err := json.Marshal(item.JSON)
	if err != nil {
		s.Error = strings.TrimSpace(s.Error + " (the item could not be encoded as JSON: " + err.Error() + ")")
		return s
	}

	s.JSON = raw

	return s
}

func comparePorts(a, b string) int {
	rank := func(port string) int {
		switch port {
		case ErrorPort:
			return 0
		case DefaultPort:
			return 1
		default:
			return 2
		}
	}

	if ra, rb := rank(a), rank(b); ra != rb {
		return ra - rb
	}

	return strings.Compare(a, b)
}
