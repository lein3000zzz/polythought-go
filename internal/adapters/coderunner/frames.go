// Package coderunner runs user code in runner processes (5e, D352 to D356) rather than in
// the server: a warm pool of them, as many as runs at once, each running one job at a
// time in jsruntime, under the sandbox where there is one. A runner is the server's own
// binary, which the launcher runs in place once under the sandbox (Entry), so it reaches
// no file, no network and no other process: only the server, over its stdin and stdout.
//
// They talk in frames, each a 4-byte big-endian length and that many bytes of JSON (D355):
// the server sends init, then a job at a time, and an answer to each host call; the
// runner sends ready, then a call for each host call and a result for each job, each
// naming the job it is of.
package coderunner

import (
	"bytes"
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

// What a runner's frames may be (P10): its ready, an engine's error at most; while it
// runs a job, room for the job's output, its logs, each line's text escaped at worst six
// bytes a byte, and the rest: an error's report, which jsruntime reads 64 KiB of, or a
// call's name and arguments.
const (
	readyRoom = 1 << 20
	logRoom   = (maxLogs + 1) * (6*maxLogBytes + 64)
	restRoom  = 1 << 20
)

// jsruntime's caps on a run's logs (runner.go), which the pool holds a runner to (P10).
const (
	maxLogs     = 100
	maxLogBytes = 2000
	cutLog      = "…"
	droppedLogs = "… and %d more lines, not kept"
)

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

	// ID is the job's, in its frame and in each call and result of it (P1).
	ID uint64 `json:"id,omitempty"`

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
	Logs      logs            `json:"logs,omitzero"`
	HostCalls int             `json:"hostCalls"`

	Code  *failure `json:"code,omitempty"`
	Error string   `json:"error,omitempty"`
}

func resultOf(res ports.CodeResult, err error) result {
	out := result{Output: res.Output, Logs: logs{lines: res.Logs}, HostCalls: res.HostCalls}

	var codeErr *domain.CodeError

	switch {
	case errors.As(err, &codeErr):
		out.Code = &failure{Kind: codeErr.Kind, Message: codeErr.Message, Stack: codeErr.Stack}
	case err != nil:
		out.Error = err.Error()
	}

	return out
}

// failure is a domain.CodeError as a frame carries it: jsruntime's have no problems, so a
// runner has no list of them to send (P10).
type failure struct {
	Kind    domain.CodeErrorKind `json:"kind"`
	Message string               `json:"message"`
	Stack   string               `json:"stack,omitempty"`
}

func (f *failure) codeError() *domain.CodeError {
	return &domain.CodeError{Kind: f.Kind, Message: f.Message, Stack: f.Stack}
}

// logs are a result's lines. Read, no more than a run keeps are decoded, maxLogs and
// jsruntime's line saying how many more it had; the rest are only counted, so a runner
// cannot have the server decode millions of them (P10).
type logs struct {
	lines []ports.CodeLog
	more  int
}

func (l logs) MarshalJSON() ([]byte, error) { return encode(l.lines) }

func (l *logs) UnmarshalJSON(raw []byte) error {
	*l = logs{}

	raw = bytes.TrimSpace(raw)

	switch {
	case bytes.Equal(raw, []byte("null")):
		return nil
	case len(raw) < 2 || raw[0] != '[':
		return errors.New("logs that are not a list")
	}

	// The list is valid JSON, its decoder's to see to: its lines are what lies between
	// its commas, outside any string or deeper value.
	depth, quoted, escaped, start := 0, false, false, 1

	for i := 1; i < len(raw); i++ {
		switch c := raw[i]; {
		case quoted:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				quoted = false
			}
		case c == '"':
			quoted = true
		case c == '[' || c == '{':
			depth++
		case depth > 0 && (c == ']' || c == '}'):
			depth--
		case depth == 0 && (c == ',' || c == ']'):
			if err := l.add(raw[start:i]); err != nil {
				return err
			}

			start = i + 1
		}
	}

	return nil
}

// add decodes a line, or counts it once there are as many as a run keeps.
func (l *logs) add(raw []byte) error {
	switch {
	case len(bytes.TrimSpace(raw)) == 0:
		return nil // the empty list's
	case len(l.lines) > maxLogs:
		l.more++
		return nil
	}

	var line ports.CodeLog
	if err := json.Unmarshal(raw, &line); err != nil {
		return err
	}

	l.lines = append(l.lines, line)

	return nil
}

// room is the most a frame from a runner may be as it runs a job of these limits (P10).
func room(limits ports.CodeLimits) int {
	return min(limits.Output+logRoom+restRoom, maxFrame)
}

// tooBig is a frame not sent, as it is more than it may be.
type tooBig struct {
	kind     string
	n, limit int
}

func (e *tooBig) Error() string {
	return fmt.Sprintf("a %s of %d bytes, more than the %d a frame may be", e.kind, e.n, e.limit)
}

// encode is v as JSON, its strings as they are: a result's output keeps the length the
// engine measured it at, with no <, > or & made six bytes.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// writeFrame writes m, unless it is more than limit bytes: then it writes nothing, and
// says so with a *tooBig.
func writeFrame(w io.Writer, m message, limit int) error {
	raw, err := encode(m)
	if err != nil {
		return err
	}

	if len(raw) > limit {
		return &tooBig{kind: m.Kind, n: len(raw), limit: limit}
	}

	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw))) //nolint:gosec // bounded by maxFrame

	if _, err := w.Write(size[:]); err != nil {
		return err
	}

	_, err = w.Write(raw)

	return err
}

// readFrame reads a frame of at most limit bytes.
func readFrame(r io.Reader, limit int) (message, error) {
	n, err := readLength(r)
	if err != nil {
		return message{}, err
	}

	if n > limit {
		return message{}, fmt.Errorf("a frame of %d bytes, more than the %d it may be", n, limit)
	}

	return readBody(r, n)
}

// readLength reads how long the frame that follows is.
func readLength(r io.Reader) (int, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return 0, err
	}

	return int(binary.BigEndian.Uint32(size[:])), nil
}

// readBody reads a frame n bytes long, its length read already.
func readBody(r io.Reader, n int) (message, error) {
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
