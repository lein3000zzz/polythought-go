package ports

import (
	"context"
	"encoding/json"
	"time"
)

// CodeCompiler turns user TypeScript into a script for a CodeRunner. The code may import
// only the SDK. Code that does not compile is a *domain.CodeError with its problems.
type CodeCompiler interface {
	Compile(ctx context.Context, source string) (CompiledCode, error)
}

// CompiledCode is a script that puts the module's exports in a global object.
type CompiledCode struct {
	Script  string
	Exports string // the global the exports go in
	Hash    string // of the source
}

// Entry is what a CodeJob calls to run the export named name.
func (c CompiledCode) Entry(name string) string {
	return c.Exports + "." + name
}

// CodeRunner runs user JavaScript in a sandbox: no files, network or environment, only
// the host calls the job answers. Code that fails, by throwing or going past a limit, is
// a *domain.CodeError; the logs come back either way.
type CodeRunner interface {
	Run(ctx context.Context, job CodeJob) (CodeResult, error)
}

type CodeJob struct {
	// Source is a script that defines the function Entry names, as a name at its top
	// level or a path of names from one, like exports.run. Run calls it with Input and
	// waits for the promise it may return.
	Source string
	Entry  string
	Input  json.RawMessage
	Host   CodeHost // nil: the code has no host calls
	Limits CodeLimits
}

// CodeHost answers the code's host calls, its only way out of the sandbox. An error goes
// back to the code, thrown.
type CodeHost func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error)

// CodeLimits bound one run. Zero means the runner's default.
type CodeLimits struct {
	Memory    int64         // bytes the engine may allocate
	Time      time.Duration // in all, host calls included
	Output    int           // bytes of the result, as JSON
	HostCalls int
}

type CodeResult struct {
	Output    json.RawMessage // what the entry function returned, as JSON; null for nothing
	Logs      []CodeLog
	HostCalls int
}

// CodeLog is a line the code wrote to its console.
type CodeLog struct {
	Level string // log, info, warn or error
	Text  string
}
