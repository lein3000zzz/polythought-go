package logic_test

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.NodeMemory = (*memory)(nil)

// memory is node memory in maps: workflow/node/key to when it expires, and to the
// entries Admit let in.
type memory struct {
	mu      sync.Mutex
	keys    map[string]time.Time
	entries map[string][]entry
	err     error
}

type entry struct{ at, until time.Time }

func newMemory() *memory {
	return &memory{keys: map[string]time.Time{}, entries: map[string][]entry{}}
}

func memKey(wf domain.WorkflowID, node domain.NodeID, k string) string {
	return string(wf) + "/" + string(node) + "/" + k
}

func (m *memory) Remember(_ context.Context, wf domain.WorkflowID, node domain.NodeID, keys []string, now, expires time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}

	var fresh []string

	for _, k := range keys {
		if until, held := m.keys[memKey(wf, node, k)]; held && until.After(now) {
			continue
		}

		m.keys[memKey(wf, node, k)] = expires
		fresh = append(fresh, k)
	}

	return fresh, nil
}

func (m *memory) Admit(_ context.Context, wf domain.WorkflowID, node domain.NodeID, key string, members []string, limit int, since, now, expires time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}

	held := 0

	for _, e := range m.entries[memKey(wf, node, key)] {
		if e.at.After(since) && e.until.After(now) {
			held++
		}
	}

	admitted := members[:max(0, min(limit-held, len(members)))]
	for range admitted {
		m.entries[memKey(wf, node, key)] = append(m.entries[memKey(wf, node, key)], entry{at: now, until: expires})
	}

	return admitted, nil
}

func (m *memory) Count(_ context.Context, wf domain.WorkflowID, node domain.NodeID, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for k, until := range m.keys {
		if strings.HasPrefix(k, memKey(wf, node, "")) && until.After(now) {
			n++
		}
	}

	for k, es := range m.entries {
		for _, e := range es {
			if strings.HasPrefix(k, memKey(wf, node, "")) && e.until.After(now) {
				n++
			}
		}
	}

	return n, nil
}

func (m *memory) Forget(_ context.Context, wf domain.WorkflowID, node domain.NodeID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k := range m.keys {
		if strings.HasPrefix(k, memKey(wf, node, "")) {
			delete(m.keys, k)
		}
	}

	return nil
}

func (m *memory) ForgetOthers(context.Context, domain.WorkflowID, []domain.NodeID) error { return nil }

func (m *memory) Sweep(context.Context, time.Time) (int, error) { return 0, nil }
