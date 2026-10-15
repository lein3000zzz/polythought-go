package domain

// CodeErrorKind says how user code failed.
type CodeErrorKind string

const (
	CodeThrew        CodeErrorKind = "threw" // it threw, or did not compile
	CodeOutOfTime    CodeErrorKind = "time"
	CodeOutOfMemory  CodeErrorKind = "memory"
	CodeOutputTooBig CodeErrorKind = "output"
	CodeTooManyCalls CodeErrorKind = "host_calls"
	CodeNoEntry      CodeErrorKind = "entry"   // the function to call is not there
	CodeNeverSettled CodeErrorKind = "pending" // it returned a promise that never settles
	CodeCrashed      CodeErrorKind = "crashed" // the engine itself stopped
)

// CodeError is user code that failed: it threw, or went past a limit. The message is for
// whoever wrote the code.
type CodeError struct {
	Kind    CodeErrorKind
	Message string // like "TypeError: cannot read property 'x' of undefined"
	Stack   string // where it threw, when it did
}

func (e *CodeError) Error() string {
	return e.Message
}
