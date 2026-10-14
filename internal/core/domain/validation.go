package domain

import "strings"

// Problem is one thing wrong with a workflow, or with other input. NodeID and Param point
// at what to fix, so an editor can highlight it; both are empty for problems with the
// input as a whole. For settings, Param is the setting's key.
type Problem struct {
	NodeID  NodeID
	Param   string
	Message string
	Err     error // a more specific sentinel, matched by errors.Is; optional
}

// ValidationError lists every problem found in a workflow, or in other input. It matches
// Base and the Err of each problem.
type ValidationError struct {
	Problems []Problem
	Base     error // what kind of input was invalid; nil means ErrInvalidWorkflow
}

func (v *ValidationError) base() error {
	if v.Base != nil {
		return v.Base
	}

	return ErrInvalidWorkflow
}

func (v *ValidationError) Add(p Problem) {
	v.Problems = append(v.Problems, p)
}

// Merge appends the problems of err if it is a ValidationError, or wraps it as one problem.
func (v *ValidationError) Merge(err error) {
	if err == nil {
		return
	}

	if other, ok := err.(*ValidationError); ok { //nolint:errorlint // only an unwrapped ValidationError carries problems
		v.Problems = append(v.Problems, other.Problems...)
		return
	}

	v.Add(Problem{Message: err.Error(), Err: err})
}

// Err returns v as an error, or nil when there are no problems.
func (v *ValidationError) Err() error {
	if len(v.Problems) == 0 {
		return nil
	}

	return v
}

func (v *ValidationError) Error() string {
	lines := make([]string, len(v.Problems))
	for i, p := range v.Problems {
		lines[i] = v.base().Error() + ": " + p.Message
	}

	return strings.Join(lines, "\n")
}

func (v *ValidationError) Unwrap() []error {
	errs := []error{v.base()}

	for _, p := range v.Problems {
		if p.Err != nil {
			errs = append(errs, p.Err)
		}
	}

	return errs
}
