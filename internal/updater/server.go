package updater

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/semver"
)

const (
	maxRequest = 1 << 10  // an update request is a version
	maxLog     = 16 << 10 // the end of an update's output that its status keeps
)

// Job updates the stack to version, writing what it does to out.
type Job func(ctx context.Context, version string, out io.Writer) error

// Server is the updater's HTTP server: one update at a time, each run by its job.
type Server struct {
	token [sha256.Size]byte
	job   Job
	ctx   context.Context // the server's: an update outlives the request asking for it
	log   logging.Logger
	now   func() time.Time

	mu     sync.Mutex
	status Status
	done   chan struct{} // closed as the latest update ends
}

// NewServer serves token's holder, running job for each update until ctx is done.
func NewServer(ctx context.Context, token string, job Job, log logging.Logger) (*Server, error) {
	if len(token) < MinToken {
		return nil, fmt.Errorf("the token must be at least %d characters, such as from openssl rand -hex 32", MinToken)
	}

	return &Server{
		token: sha256.Sum256([]byte(token)), job: job, ctx: ctx, log: log, now: func() time.Time { return time.Now().UTC() },
		status: Status{State: StateIdle},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+PathHealth, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET "+PathStatus, s.authorized(s.serveStatus))
	mux.HandleFunc("POST "+PathUpdate, s.authorized(s.serveUpdate))

	return mux
}

// Wait waits for the latest update to end, for tests and for stopping.
func (s *Server) Wait() {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()

	if done != nil {
		<-done
	}
}

func (s *Server) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		given, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(given))

		// Hashed first, so the comparison takes as long whatever the length.
		if !ok || subtle.ConstantTimeCompare(sum[:], s.token[:]) != 1 {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "the token is not the updater's"})
			return
		}

		next(w, r)
	}
}

func (s *Server) serveStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, st)
}

func (s *Server) serveUpdate(w http.ResponseWriter, r *http.Request) {
	var req UpdateRequest

	dec := json.NewDecoder(io.LimitReader(r.Body, maxRequest))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "want {\"version\": \"0.1.2\"}"})
		return
	}

	if !semver.Valid(req.Version) {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: fmt.Sprintf("%q is no version, such as 0.1.2", req.Version)})
		return
	}

	st, err := s.start(req.Version)
	if err != nil {
		writeJSON(w, http.StatusConflict, errorBody{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusAccepted, st)
}

var errUpdating = errors.New("an update is under way already")

// start runs an update to version, unless one is under way.
func (s *Server) start(version string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status.State == StateUpdating {
		return Status{}, fmt.Errorf("%w, to %s", errUpdating, s.status.Version)
	}

	s.status = Status{State: StateUpdating, Version: version, StartedAt: s.now()}
	s.done = make(chan struct{})

	go s.run(version, s.done)

	return s.status, nil
}

func (s *Server) run(version string, done chan struct{}) {
	defer close(done)

	s.log.Info(s.ctx, "Updating", "version", version)

	out := &tail{max: maxLog}
	err := s.job(s.ctx, version, out)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.status.State, s.status.Log, s.status.FinishedAt = StateDone, out.String(), s.now()

	if err != nil {
		s.status.State, s.status.Error = StateFailed, err.Error()
		s.log.Error(s.ctx, "Update failed", "version", version, logging.Err(err), "output", out.String())

		return
	}

	s.log.Info(s.ctx, "Updated", "version", version)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}

	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return strings.ToValidUTF8(string(t.buf), "")
}
