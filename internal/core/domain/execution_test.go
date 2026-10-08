package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestExecutionFinish(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	end := start.Add(1500 * time.Millisecond)
	boom := errors.New("boom")

	tests := []struct {
		name      string
		prepare   func(t *testing.T, e *domain.Execution)
		status    domain.ExecutionStatus
		cause     error
		wantErr   bool
		wantState domain.ExecutionStatus
		wantError string
	}{
		{name: "succeeds", status: domain.ExecutionSucceeded, wantState: domain.ExecutionSucceeded},
		{name: "fails with a cause", status: domain.ExecutionFailed, cause: boom, wantState: domain.ExecutionFailed, wantError: "boom"},
		{name: "is cancelled", status: domain.ExecutionCancelled, cause: errors.New("context canceled"), wantState: domain.ExecutionCancelled, wantError: "context canceled"},
		{name: "cannot succeed with an error", status: domain.ExecutionSucceeded, cause: boom, wantErr: true, wantState: domain.ExecutionRunning},
		{name: "cannot finish as running", status: domain.ExecutionRunning, wantErr: true, wantState: domain.ExecutionRunning},
		{
			name: "cannot finish twice",
			prepare: func(t *testing.T, e *domain.Execution) {
				require.NoError(t, e.Finish(domain.ExecutionSucceeded, end, nil))
			},
			status:    domain.ExecutionFailed,
			cause:     boom,
			wantErr:   true,
			wantState: domain.ExecutionSucceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := domain.NewExecution("ex", "wf", "trigger", start)
			if tt.prepare != nil {
				tt.prepare(t, &e)
			}

			err := e.Finish(tt.status, end, tt.cause)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, end, e.FinishedAt)
				assert.Equal(t, 1500*time.Millisecond, e.Duration())
			}

			assert.Equal(t, tt.wantState, e.Status)
			assert.Equal(t, tt.wantError, e.Error)
		})
	}
}

func TestExecutionRecord(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	run := domain.NodeRun{NodeID: "a", Status: domain.NodeRunSucceeded, Output: map[string]int{"main": 3}}

	tests := []struct {
		name     string
		finished bool
		wantErr  error
		wantRuns int
	}{
		{name: "while running", wantRuns: 1},
		{name: "after finishing", finished: true, wantErr: domain.ErrExecutionFinished},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := domain.NewExecution("ex", "wf", "trigger", start)
			assert.Zero(t, e.Duration(), "no duration while running")

			if tt.finished {
				require.NoError(t, e.Finish(domain.ExecutionSucceeded, start, nil))
			}

			err := e.Record(run)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Len(t, e.Runs, tt.wantRuns)
		})
	}
}

func TestExecutionStatusTerminal(t *testing.T) {
	tests := []struct {
		status domain.ExecutionStatus
		want   bool
	}{
		{domain.ExecutionRunning, false},
		{domain.ExecutionSucceeded, true},
		{domain.ExecutionFailed, true},
		{domain.ExecutionCancelled, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.Terminal())
		})
	}
}
