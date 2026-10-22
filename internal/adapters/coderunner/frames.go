// Package coderunner runs user code in runner processes (5e, D352 to D356) rather than in
// the server: a warm pool of them, as many as runs at once, each running one job at a
// time in jsruntime, under the sandbox where there is one. A runner is the server's own
// binary, which the launcher runs in place once under the sandbox (Entry), so it reaches
// no file, no network and no other process: only the server, over its stdin and stdout.
//
// They talk in frames, each a 4-byte big-endian length and that many bytes of JSON (D355):
// the server sends init, then a job at a time, and an answer to each host call; the
// runner sends ready, then a call for each host call and a result for each job.
package coderunner

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// maxFrame is the most a frame may be: a job's source and input, a result's output.
const maxFrame = 64 << 20

// What a frame is.
const (
	kindInit   = "init"   // the server's first: the prelude
	kindReady  = "ready"  // the runner's answer, once its engine is made
	kindJob    = "job"    // a job to run
	kindCall   = "call"   // a host call the job's code makes
	kindAnswer = "answer" // the server's answer to it
	kindResult = "result" // the job's outcome
)

// message is a frame's content: which fields it has goes by its kind.
type message struct {
	Kind string `json:"kind"`

	Prelude string `json:"prelude,omitempty"` // init

	Job *job `json:"job,omitempty"`

	// A call, and its answer: Value, or Error, thrown in the code.
	Call  int             `json:"call,omitempty"`
	Name  string          `json:"name,omitempty"`
	Args  json.RawMessage `json:"args,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`

	Result *result `json:"result,omitempty"`

	// Error is ready's, when the runner could not make its engine, or an answer's.
	Error string `json:"error,omitempty"`
}

// job is a ports.CodeJob, its limits bounded already, saying whether it has host calls.
type job struct {
	Source string           `json:"source"`
	Entry  string           `json:"entry"`
	Input  json.RawMessage  `json:"input,omitempty"`
	Hosted bool             `json:"hosted"`
	Limits ports.CodeLimits `json:"limits"`
}

// result is a job's outcome: what it gave, none apart from a null, and the code's failure
// or the runner's.
type result struct {
	Output    json.RawMessage `json:"output,omitempty"`
	Logs      []ports.CodeLog `json:"logs,omitempty"`
	HostCalls int             `json:"hostCalls"`

	Code  *domain.CodeError `json:"code,omitempty"`
	Error string            `json:"error,omitempty"`
}

func resultOf(res ports.CodeResult) result {
	return result{Output: res.Output, Logs: res.Logs, HostCalls: res.HostCalls}
}

func (r *result) codeResult() ports.CodeResult {
	return ports.CodeResult{Output: r.Output, Logs: r.Logs, HostCalls: r.HostCalls}
}

func writeFrame(w io.Writer, m message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}

	if len(raw) > maxFrame {
		return fmt.Errorf("a %s of %d bytes, more than the %d a frame may be", m.Kind, len(raw), maxFrame)
	}

	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw))) //nolint:gosec // bounded above

	if _, err := w.Write(size[:]); err != nil {
		return err
	}

	_, err = w.Write(raw)

	return err
}

func readFrame(r io.Reader) (message, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return message{}, err
	}

	n := binary.BigEndian.Uint32(size[:])
	if n > maxFrame {
		return message{}, fmt.Errorf("a frame of %d bytes, more than the %d it may be", n, maxFrame)
	}

	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return message{}, errors.Join(errors.New("a frame cut short"), err)
	}

	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return message{}, fmt.Errorf("a frame not as it should be: %w", err)
	}

	return m, nil
}
