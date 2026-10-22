package coderunner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/lein3000zzz/the-watchers/pkg/observability"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Entry is the runner, an entry of the server's own binary: main hands it to
// sandbox.Launch.
var Entry = sandbox.Entry{Name: "coderunner", Run: Serve}

// Headroom is what a runner takes beside its job's memory: the Go runtime, the engine
// compiled, the instance's own overhead; about 80 MiB idle under the sandbox.
const Headroom = 192 << 20

// memoryCap is what a runner may take running a job of that much memory: twice it, as the
// engine's memory is copied as it grows, and Headroom; past it, its process is stopped.
func memoryCap(memory, headroom int64) int64 { return 2*memory + headroom }

// Serve is a runner: it makes its engine with the prelude the server sends, then runs the
// jobs that come on stdin one at a time, asking the server for each host call, until stdin
// closes (D355).
func Serve() error {
	in, out := bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)

	send := func(m message, limit int) error {
		if err := writeFrame(out, m, limit); err != nil {
			return err
		}

		return out.Flush()
	}

	init, err := readFrame(in, maxFrame)
	if err != nil {
		return fmt.Errorf("its init: %w", err)
	}

	if init.Kind != kindInit {
		return fmt.Errorf("a %s first, not init", init.Kind)
	}

	// The job's limits, as the server bounded them: the engine's most for this job.
	var limits ports.CodeLimits

	engine, err := jsruntime.New(context.Background(), observability.NoopUtility(), jsruntime.Config{
		Concurrency: 1, Prelude: init.Prelude, Limits: func() ports.CodeLimits { return limits },
	})
	if err != nil {
		_ = send(message{Kind: kindReady, Error: err.Error()}, readyRoom)
		return err
	}

	if err := send(message{Kind: kindReady}, readyRoom); err != nil {
		return err
	}

	for {
		m, err := readFrame(in, maxFrame)

		switch {
		case errors.Is(err, io.EOF):
			return nil // the server is done with it
		case err != nil:
			return err
		case m.Kind != kindJob || m.Job == nil:
			return fmt.Errorf("a %s, not a job", m.Kind)
		}

		limits = m.Job.Limits
		debug.SetMemoryLimit(memoryCap(limits.Memory, Headroom))

		j := ports.CodeJob{Source: m.Job.Source, Entry: m.Job.Entry, Input: m.Job.Input, Limits: limits}
		if m.Job.Hosted {
			j.Host = asker(in, send, m.ID, room(limits))
		}

		outcome := resultOf(engine.Run(context.Background(), j))

		if err := send(message{Kind: kindResult, ID: m.ID, Result: &outcome}, maxFrame); err != nil {
			return err
		}
	}
}

// asker answers a job's host calls by asking the server, each in a call frame its answer
// follows. A call too big for a frame of the job's is not sent: the code gets the error.
func asker(in io.Reader, send func(message, int) error, id uint64, limit int) ports.CodeHost {
	calls := 0

	return func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		calls++

		var big *tooBig

		err := send(message{Kind: kindCall, ID: id, Call: calls, Name: name, Args: args}, limit)

		switch {
		case errors.As(err, &big):
			return nil, fmt.Errorf("a host call of %s, more than the %s one may send", size(int64(big.n)), size(int64(limit)))
		case err != nil:
			return nil, err
		}

		a, err := readFrame(in, maxFrame)

		switch {
		case err != nil:
			return nil, err
		case a.Kind != kindAnswer || a.Call != calls:
			return nil, fmt.Errorf("a %s %d, not the answer to call %d", a.Kind, a.Call, calls)
		case a.Error != "":
			return nil, errors.New(a.Error)
		}

		return a.Value, nil
	}
}
