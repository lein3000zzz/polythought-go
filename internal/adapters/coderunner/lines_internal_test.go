package coderunner

import (
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

// A runner's stderr reaches the server's log within the pool's quota, each line cut (P4).
func TestLinesQuota(t *testing.T) {
	p := &Pool{log: logging.Noop(), said: &quota{limit: rate.NewLimiter(linesPerSecond, linesBurst)}}
	r := &runner{pool: p}

	taken := 0
	for range linesBurst * 3 {
		if p.said.take(p.log) {
			taken++
		}
	}

	assert.Equal(t, linesBurst, taken, "a burst, then the rest dropped")

	p.said.mu.Lock()
	dropped := p.said.dropped
	p.said.mu.Unlock()
	assert.Equal(t, linesBurst*2, dropped, "and counted")

	_, err := (&lines{r: r}).Write([]byte(strings.Repeat("x", 3*maxLine) + "\n"))
	assert.NoError(t, err)
	assert.LessOrEqual(t, len(r.last), maxLine+len(" …"), "its line cut")
}
