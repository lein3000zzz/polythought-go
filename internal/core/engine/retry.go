package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Retrying (D440): a node tries again what failed, as its Retry setting says, waiting
// twice as long each time from a second, at most 30 s a wait and 2 minutes in all.
const (
	// DefaultRetries is how many more tries a node that only reads makes, unless its
	// Retry setting says otherwise.
	DefaultRetries = 3

	firstRetryWait = time.Second
	maxRetryWait   = 30 * time.Second
	maxRetrying    = 2 * time.Minute
)

// Final marks why something failed as no use trying again: a 404, a setting that is
// wrong. A node's retries leave it failed.
func Final(err error) error {
	if err == nil {
		return nil
	}

	return finalError{err}
}

type finalError struct{ error }

func (e finalError) Unwrap() error { return e.error }

// IsFinal says trying again would fail as err did: marked Final, or something not there
// or not right that no wait mends.
func IsFinal(err error) bool { return final(err) }

func final(err error) bool {
	var f finalError

	return errors.As(err, &f) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalidInput) ||
		errors.Is(err, ErrInvalidParam) || errors.Is(err, ErrMissingParam)
}

// WithRetryWaits replaces how long the n-th retry waits, for tests.
func WithRetryWaits(wait func(n int) time.Duration) ExecutorOption {
	return func(e *Executor) { e.retryWait = wait }
}

// RetryWait is how long the n-th retry waits, n from 1: a second, then twice the last,
// at most 30 s.
func RetryWait(n int) time.Duration {
	if n < 1 {
		return 0
	}

	if n > 6 {
		return maxRetryWait
	}

	return min(firstRetryWait<<(n-1), maxRetryWait)
}

// Retries is how many more tries a node set as spec makes when it fails: its Retry
// setting, else DefaultRetries for one that only reads, else none.
func Retries(typ NodeType, spec domain.NodeSpec) int {
	switch {
	case typ.NoRetry || typ.Trades:
		return 0
	case spec.Retry != nil:
		return min(max(*spec.Retry, 0), domain.MaxRetry)
	case reads(typ, spec.Params):
		return DefaultRetries
	default:
		return 0
	}
}

// reads says a node set with params only reads.
func reads(typ NodeType, params map[string]any) bool {
	if !typ.Reads {
		return false
	}

	when := typ.ReadsWhen
	if when == nil {
		return true
	}

	v, ok := params[when.Param]
	if !ok || v == nil {
		for _, p := range typ.Params {
			if p.Name == when.Param {
				v = p.Default
			}
		}
	}

	s, ok := v.(string)

	return ok && strings.EqualFold(strings.TrimSpace(s), when.Value)
}

// checkRetry reports a Retry setting the node cannot have.
func checkRetry(v *domain.ValidationError, n domain.NodeSpec, typ NodeType, trigger bool) {
	if n.Retry == nil {
		return
	}

	problem := func(msg string) {
		v.Add(domain.Problem{NodeID: n.ID, Message: fmt.Sprintf("node %q: retry %s", n.ID, msg), Err: ErrInvalidParam})
	}

	switch r := *n.Retry; {
	case r < 0 || r > domain.MaxRetry:
		problem(fmt.Sprintf("%d: from 0 to %d more tries", r, domain.MaxRetry))
	case r > 0 && trigger:
		problem("on a trigger: triggers are not retried")
	case r > 0 && (typ.NoRetry || typ.Trades):
		problem("on a node that trades: a retry could order twice, so it is never retried; its orders resend where that is safe")
	}
}

// retried tries what the node failed again, up to retries more times: the whole run when
// it failed as a whole, else only the items it failed, each with its own settings, those
// it now sends on going after the others. A node that waits, panics or is cancelled is not
// retried. It gives what the last try left and how many tries there were.
func (x *execution) retried(
	ctx context.Context,
	id domain.NodeID,
	retries int,
	rc RunContext,
	given, out Ports,
	err error,
	run func(context.Context, RunContext, Ports) (Ports, error),
) (Ports, int, error) {
	tries := 1
	start := x.executor.now()

	for n := 1; n <= retries; n++ {
		whole := err != nil

		var (
			sub, rest Ports
			kept      map[string][]int
		)

		switch {
		case whole && !retriable(ctx, err):
			return out, tries, err
		case !whole:
			if sub, kept, rest = failedInputs(out, given); sub.Len() == 0 {
				return out, tries, err
			}
		}

		wait := x.executor.retryWait(n)
		if x.executor.now().Sub(start)+wait > maxRetrying || !sleep(ctx, wait) {
			return out, tries, err
		}

		tries++
		x.executor.log.Debug(ctx, "Node tried again", "node_id", id, "try", tries, "items", sub.Len(), "whole", whole)

		if whole {
			out, err = run(ctx, rc, given)
			continue
		}

		again := rc
		again.Params = remapped{inner: rc.Params, kept: kept}

		got, gotErr := run(ctx, again, sub)
		if gotErr != nil {
			continue // its items failed still, as they were
		}

		out = rest

		for port, items := range got {
			for _, it := range items {
				it.Source = originals(it.Source, kept)
				out.Add(port, it)
			}
		}
	}

	return out, tries, err
}

func retriable(ctx context.Context, err error) bool {
	var w *Wait
	return ctx.Err() == nil && !errors.As(err, &w) && !errors.Is(err, ErrNodePanicked) && !final(err)
}

// failedInputs are the items the node got that it failed, to try again, by port, with the
// index each had; and its output without them. A failed item not traced to one item it got,
// or failed for good, stays failed.
func failedInputs(out, given Ports) (sub Ports, kept map[string][]int, rest Ports) {
	sub, kept, rest = Ports{}, map[string][]int{}, Ports{}
	seen := map[ItemRef]bool{}

	for port, items := range out {
		if port != ErrorPort {
			rest.Add(port, items...)
		}
	}

	for _, it := range out[ErrorPort] {
		if len(it.Source) != 1 || final(it.Err) {
			rest.Add(ErrorPort, it)
			continue
		}

		ref := it.Source[0]
		in := given[ref.Port]

		switch {
		case ref.Index < 0 || ref.Index >= len(in):
			rest.Add(ErrorPort, it)
		case !seen[ref]:
			seen[ref] = true
			sub.Add(ref.Port, in[ref.Index])
			kept[ref.Port] = append(kept[ref.Port], ref.Index)
		}
	}

	return sub, kept, rest
}

// originals are refs to the items tried again as the indexes of the items the node got.
func originals(refs []ItemRef, kept map[string][]int) []ItemRef {
	out := make([]ItemRef, len(refs))

	for i, ref := range refs {
		if k := kept[ref.Port]; ref.Index >= 0 && ref.Index < len(k) {
			ref.Index = k[ref.Index]
		}

		out[i] = ref
	}

	return out
}

// sleep waits d, false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
