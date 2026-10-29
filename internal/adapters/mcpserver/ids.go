package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Agents sharing a session (D432): an agent's subagents, each numbering its requests from 0,
// send the same ID while one of them waits, as in wait_for_task, which the SDK refuses: the
// spec keeps IDs apart within a session. The server gives each request in a session an ID
// of its own, and puts the caller's back on its answer.

// ownPrefix starts the IDs the server gives requests.
const ownPrefix = "pt-"

type requestIDs struct {
	next atomic.Uint64

	mu sync.Mutex
	// inFlight are each session's requests being answered: the caller's ID, as its JSON,
	// to the server's for it, the latest last.
	inFlight map[string]map[string][]string
}

func newRequestIDs() *requestIDs {
	return &requestIDs{inFlight: map[string]map[string][]string{}}
}

// wrap renumbers the requests posted to a session, and their answers back. What is not a
// session's, or does not parse, goes on as it came, for the SDK to answer.
func (r *requestIDs) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		session := req.Header.Get("Mcp-Session-Id")
		if req.Method != http.MethodPost || session == "" {
			next.ServeHTTP(w, req)
			return
		}

		body, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
		if err != nil {
			http.Error(w, "the request could not be read", http.StatusBadRequest)
			return
		}

		req.Body = io.NopCloser(bytes.NewReader(body))

		if len(body) > maxBody {
			next.ServeHTTP(w, req) // the SDK refuses it
			return
		}

		renumbered, ours, ok := r.renumber(session, body)
		if !ok {
			next.ServeHTTP(w, req)
			return
		}

		defer r.answered(session, ours)

		req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(renumbered)), int64(len(renumbered))

		aw := &answers{ResponseWriter: w, ours: ours}
		next.ServeHTTP(aw, req)
		aw.finish()
	})
}

// renumber gives each request in body an ID of the server's, and points a cancellation at
// the latest request the caller gave that ID: ours maps the server's IDs to the caller's.
// Not ok, nothing changed.
func (r *requestIDs) renumber(session string, body []byte) (renumbered []byte, ours map[string]json.RawMessage, ok bool) {
	trimmed := bytes.TrimSpace(body)
	batch := len(trimmed) > 0 && trimmed[0] == '['

	var msgs []map[string]json.RawMessage

	if batch {
		if json.Unmarshal(trimmed, &msgs) != nil {
			return nil, nil, false
		}
	} else {
		var m map[string]json.RawMessage
		if json.Unmarshal(trimmed, &m) != nil || m == nil {
			return nil, nil, false
		}

		msgs = []map[string]json.RawMessage{m}
	}

	ours = map[string]json.RawMessage{}
	if !r.mark(session, msgs, ours) {
		return nil, nil, false
	}

	var err error
	if batch {
		renumbered, err = json.Marshal(msgs)
	} else {
		renumbered, err = json.Marshal(msgs[0])
	}

	if err != nil {
		r.answered(session, ours)
		return nil, nil, false
	}

	return renumbered, ours, true
}

// mark renumbers the requests among msgs, in place, keeping them in flight, and points
// their cancellations at the server's IDs: whether any changed.
func (r *requestIDs) mark(session string, msgs []map[string]json.RawMessage, ours map[string]json.RawMessage) bool {
	changed := false

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, m := range msgs {
		if _, isCall := m["method"]; !isCall {
			continue // an answer to the server's own request: its ID is the server's
		}

		if id, ok := idOf(m["id"]); ok {
			mine := fmt.Sprintf("%q", fmt.Sprintf("%s%d", ownPrefix, r.next.Add(1)))
			ours[mine], m["id"], changed = id, json.RawMessage(mine), true

			byCaller := r.inFlight[session]
			if byCaller == nil {
				byCaller = map[string][]string{}
				r.inFlight[session] = byCaller
			}

			byCaller[string(id)] = append(byCaller[string(id)], mine)

			continue
		}

		var method string
		if json.Unmarshal(m["method"], &method) == nil && method == "notifications/cancelled" {
			changed = r.cancelling(session, m) || changed
		}
	}

	return changed
}

// cancelling points a cancellation at the server's ID for the caller's: of the requests it
// gave that ID, the latest. r.mu is held.
func (r *requestIDs) cancelling(session string, m map[string]json.RawMessage) bool {
	var params map[string]json.RawMessage
	if json.Unmarshal(m["params"], &params) != nil || params == nil {
		return false
	}

	id, ok := idOf(params["requestId"])
	if !ok {
		return false
	}

	mine := r.inFlight[session][string(id)]
	if len(mine) == 0 {
		return false
	}

	params["requestId"] = json.RawMessage(mine[len(mine)-1])

	data, err := json.Marshal(params)
	if err != nil {
		return false
	}

	m["params"] = data

	return true
}

// answered forgets the session's requests whose answers are written, or will never be.
func (r *requestIDs) answered(session string, ours map[string]json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	byCaller := r.inFlight[session]

	for mine, theirs := range ours {
		left := byCaller[string(theirs)]
		for i, id := range left {
			if id == mine {
				left = append(left[:i], left[i+1:]...)
				break
			}
		}

		if len(left) == 0 {
			delete(byCaller, string(theirs))
		} else {
			byCaller[string(theirs)] = left
		}
	}

	if len(byCaller) == 0 {
		delete(r.inFlight, session)
	}
}

// idOf is a request's ID, compacted: absent or null, a notification has none.
func idOf(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var b bytes.Buffer
	if json.Compact(&b, raw) != nil || b.String() == "null" {
		return nil, false
	}

	return b.Bytes(), true
}

// answers writes the SDK's answers with the callers' IDs back on them: event by event on a
// stream, all at once otherwise.
type answers struct {
	http.ResponseWriter
	ours map[string]json.RawMessage

	buf     []byte
	decided bool
	stream  bool
}

func (a *answers) decide() {
	if !a.decided {
		a.decided = true
		a.stream = strings.HasPrefix(a.Header().Get("Content-Type"), "text/event-stream")
	}
}

func (a *answers) WriteHeader(status int) {
	a.decide()

	if !a.stream {
		a.Header().Del("Content-Length") // the IDs put back change it
	}

	a.ResponseWriter.WriteHeader(status)
}

func (a *answers) Write(p []byte) (int, error) {
	a.decide()
	a.buf = append(a.buf, p...)

	if !a.stream {
		return len(p), nil
	}

	for {
		end := bytes.Index(a.buf, []byte("\n\n"))
		if end < 0 {
			return len(p), nil
		}

		event := a.buf[:end+2]
		if _, err := a.ResponseWriter.Write(a.event(event)); err != nil {
			return 0, err
		}

		a.buf = a.buf[end+2:]
	}
}

// Flush sends the events written whole; one half written waits for its end.
func (a *answers) Flush() {
	_ = http.NewResponseController(a.ResponseWriter).Flush()
}

func (a *answers) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// finish writes what is left: the whole answer, when not a stream.
func (a *answers) finish() {
	if len(a.buf) == 0 {
		return
	}

	if a.stream {
		_, _ = a.ResponseWriter.Write(a.event(a.buf))
	} else {
		_, _ = a.ResponseWriter.Write(a.message(bytes.TrimSpace(a.buf)))
	}

	a.buf = nil
}

func (a *answers) event(event []byte) []byte {
	if !bytes.Contains(event, []byte(ownPrefix)) {
		return event
	}

	lines := bytes.Split(event, []byte("\n"))
	for i, line := range lines {
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			lines[i] = append([]byte("data: "), a.message(bytes.TrimSpace(data))...)
		}
	}

	return bytes.Join(lines, []byte("\n"))
}

// message is an answer, or a batch of them, with the callers' IDs on those the server
// renumbered.
func (a *answers) message(data []byte) []byte {
	if !bytes.Contains(data, []byte(ownPrefix)) {
		return data
	}

	if len(data) > 0 && data[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(data, &batch) != nil {
			return data
		}

		for i, m := range batch {
			batch[i] = a.message(m)
		}

		out, err := json.Marshal(batch)
		if err != nil {
			return data
		}

		return out
	}

	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}

	if _, isCall := m["method"]; isCall {
		return data // the server's own request or notification
	}

	id, ok := idOf(m["id"])
	if !ok {
		return data
	}

	theirs, ok := a.ours[string(id)]
	if !ok {
		return data
	}

	m["id"] = theirs

	out, err := json.Marshal(m)
	if err != nil {
		return data
	}

	return out
}
