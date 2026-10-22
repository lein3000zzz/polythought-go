package coderunner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Pids are the runners' processes, those that run.
func (p *Pool) Pids() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []int

	for _, r := range p.all {
		if pr := r.current(); pr != nil {
			out = append(out, pr.pid())
		}
	}

	return out
}

// DataCap is a runner's hard memory limit for jobs of that much memory.
var DataCap = dataCap

// Rogue is a runner whose code escaped the engine (P1, P10): it answers each job as its
// source says, not as jsruntime would.
var Rogue = sandbox.Entry{Name: "coderunner-rogue", Run: rogue}

// Gone is cfg with Rogue's runners.
func Gone(cfg Config) Config {
	cfg.entry = Rogue.Name
	return cfg
}

func rogue() error {
	in, out := bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)

	send := func(m message) error {
		if err := writeFrame(out, m, maxFrame); err != nil {
			return err
		}

		return out.Flush()
	}

	if _, err := readFrame(in, maxFrame); err != nil {
		return err
	}

	if err := send(message{Kind: kindReady}); err != nil {
		return err
	}

	for {
		m, err := readFrame(in, maxFrame)
		if err != nil {
			return nil //nolint:nilerr // the server is done with it
		}

		id, limits := m.ID, m.Job.Limits
		done := &result{Output: json.RawMessage(`"done"`)}

		switch m.Job.Source {
		case "stale": // its result, then another
			err = errors.Join(send(message{Kind: kindResult, ID: id, Result: done}), send(message{Kind: kindResult, ID: id, Result: done}))
		case "another job's":
			err = send(message{Kind: kindResult, ID: id + 1, Result: done})
		case "calls": // one more than it may
			for i := range limits.HostCalls + 1 {
				if err = send(message{Kind: kindCall, ID: id, Call: i + 1, Name: "x"}); err == nil {
					_, err = readFrame(in, maxFrame)
				}

				if err != nil {
					break
				}
			}
		case "output":
			raw, _ := json.Marshal(strings.Repeat("x", 1000))
			err = send(message{Kind: kindResult, ID: id, Result: &result{Output: raw}})
		case "logs": // all it logged, cut to nothing, one line of no level the engine has
			lines := []ports.CodeLog{{Level: "log", Text: strings.Repeat("x", 5000)}, {Level: "shout", Text: "line 1"}}
			for i := 2; i < 150; i++ {
				lines = append(lines, ports.CodeLog{Level: "log", Text: fmt.Sprintf("line %d", i)})
			}

			err = send(message{Kind: kindResult, ID: id, Result: &result{Output: json.RawMessage(`null`), Logs: logs{lines: lines}}})
		case "frame": // more than its job needs, of lines with nothing in them
			lines := make([]ports.CodeLog, room(limits)/20)
			err = send(message{Kind: kindResult, ID: id, Result: &result{Logs: logs{lines: lines}}})
		default:
			err = send(message{Kind: kindResult, ID: id, Result: done})
		}

		if err != nil {
			return err
		}
	}
}
