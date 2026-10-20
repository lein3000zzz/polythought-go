// Package tscompiler turns user TypeScript into JavaScript for the runtime, with esbuild.
// Code may import only the SDK, which the runtime provides as a global, and workflow code
// the builder too, bundled from the binary: nothing from npm, the file system or the
// network gets in. Compiled code is cached in memory by the source's hash; compiling
// again after a restart takes a millisecond.
package tscompiler

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/lru"
)

const (
	tracerName        = "github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	opCompile         = "tscompiler.Compile"
	opCompileWorkflow = "tscompiler.CompileWorkflow"

	// SDKModule is the one module code may import. It reads SDKGlobal, which the
	// runtime sets.
	SDKModule = "@polythought/sdk"
	SDKGlobal = "__polythought_sdk"

	// WorkflowModule is the builder of workflow code (D64), which only workflow code may
	// import.
	WorkflowModule = "@polythought/workflow"

	// WorkflowEntry is the function compiled workflow code defines, which returns the
	// code's default export: the workflow.
	WorkflowEntry = "__polythought_workflow"

	// ExportsGlobal is where compiled code puts its exports.
	ExportsGlobal = "__polythought_code"

	// InvokeGlobal is the SDK's function that runs an export (sdk/sdk.ts).
	InvokeGlobal = "__polythought_invoke"

	// MaxSource is the largest source compiled, in bytes.
	MaxSource = 256 << 10

	cacheSize           = 256
	expressionCacheSize = 4096

	// MaxExpression is the longest expression compiled, in bytes.
	MaxExpression = 16 << 10
)

// The builder, sdk/workflow.ts: the editor loads the same file as @polythought/workflow.
//
//go:embed sdk/workflow.ts
var workflowSource string

var _ ports.CodeCompiler = (*Compiler)(nil)

type Compiler struct {
	cache       *lru.Cache[string, string] // scripts by the source's hash
	expressions *lru.Cache[string, string] // JavaScript by the expression
	slots       chan struct{}              // compiles at once: a slow one cannot take every CPU (R5)
	kit         *redkit.Kit
	scope       tracing.Scope
}

func New(util *observability.Utility) (*Compiler, error) {
	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    util.Logger.With("component", "tscompiler"),
		Subsystem: "tscompiler",
		Buckets:   redkit.MediumLongBuckets,
		Component: "code compile",
	})
	if err != nil {
		return nil, fmt.Errorf("tscompiler: register metrics: %w", err)
	}

	return &Compiler{
		cache:       lru.New[string, string](cacheSize),
		expressions: lru.New[string, string](expressionCacheSize),
		slots:       make(chan struct{}, max(2, runtime.NumCPU()/2)),
		kit:         kit,
		scope:       tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
	}, nil
}

func (c *Compiler) Compile(ctx context.Context, source string) (ports.CompiledCode, error) {
	return c.compileCached(ctx, opCompile, source, false)
}

// CompileWorkflow compiles workflow code (D74) as Compile does code nodes', with the
// builder bundled in as WorkflowModule. Its Invoke is WorkflowEntry.
func (c *Compiler) CompileWorkflow(ctx context.Context, source string) (ports.CompiledCode, error) {
	return c.compileCached(ctx, opCompileWorkflow, source, true)
}

func (c *Compiler) compileCached(ctx context.Context, op, source string, workflow bool) (code ports.CompiledCode, err error) {
	done := c.kit.Start(ctx, op)

	defer func() { done(err == nil || isCodeError(err)) }() // code that does not compile is not the compiler failing

	return tracing.InScope(ctx, c.scope, op, func(_ context.Context, span trace.Span) (ports.CompiledCode, error) {
		span.SetAttributes(attribute.Int("code.size", len(source)))

		if len(source) > MaxSource {
			return ports.CompiledCode{}, &domain.CodeError{
				Kind:    domain.CodeDidNotCompile,
				Message: fmt.Sprintf("the code is %d KiB, more than the %d KiB allowed", len(source)>>10, MaxSource>>10),
			}
		}

		sum := sha256.Sum256([]byte(source))
		hash := hex.EncodeToString(sum[:])

		key, invoke := hash, InvokeGlobal
		if workflow {
			key, invoke = "workflow:"+hash, WorkflowEntry // the same source, another script
		}

		script, cached := c.cache.Get(key)
		span.SetAttributes(attribute.Bool("code.cached", cached), attribute.Bool("code.workflow", workflow))

		if !cached {
			if why := tooNested(source, maxArrows); why != "" {
				return ports.CompiledCode{}, &domain.CodeError{Kind: domain.CodeDidNotCompile, Message: why}
			}

			release, err := c.slot(ctx)
			if err != nil {
				return ports.CompiledCode{}, err
			}

			script, err = compile(source, workflow)
			release()

			if err != nil {
				return ports.CompiledCode{}, err
			}

			c.cache.Put(key, script)
		}

		return ports.CompiledCode{Script: script, Exports: ExportsGlobal, Hash: hash, Invoke: invoke}, nil
	})
}

func compile(source string, workflow bool) (string, error) {
	opts := api.BuildOptions{
		Stdin:      &api.StdinOptions{Contents: source, Loader: api.LoaderTS, Sourcefile: "code.ts"},
		Bundle:     true,
		Format:     api.FormatIIFE,
		GlobalName: ExportsGlobal,
		Platform:   api.PlatformNeutral,
		Target:     api.ES2023, // all of which the engine runs
		Charset:    api.CharsetUTF8,
		// Nothing read from disk: no tsconfig.json decides how code compiles.
		TsconfigRaw:   "{}",
		LegalComments: api.LegalCommentsNone,
		LogLevel:      api.LogLevelSilent,
		Write:         false,
		Plugins:       []api.Plugin{onlySDK},
	}

	if workflow {
		opts.Plugins = []api.Plugin{withWorkflow}
		// Code that exports nothing leaves the global undefined.
		opts.Footer = map[string]string{
			"js": "function " + WorkflowEntry + "() { return globalThis." + ExportsGlobal + "?.default; }",
		}
	}

	result := api.Build(opts)

	if len(result.Errors) > 0 {
		return "", problems(source, result.Errors)
	}

	if len(result.OutputFiles) != 1 {
		return "", fmt.Errorf("tscompiler: esbuild wrote %d files, not one", len(result.OutputFiles))
	}

	return string(result.OutputFiles[0].Contents), nil
}

// CompileExpression turns one TypeScript expression, the inside of {{ }}, into the same
// expression in JavaScript. Statements and imports are not expressions, and fail.
func (c *Compiler) CompileExpression(ctx context.Context, expr string) (string, error) {
	if len(expr) > MaxExpression {
		return "", &domain.CodeError{
			Kind:    domain.CodeDidNotCompile,
			Message: fmt.Sprintf("the expression is %d KiB, more than the %d KiB allowed", len(expr)>>10, MaxExpression>>10),
		}
	}

	if js, ok := c.expressions.Get(expr); ok {
		return js, nil
	}

	if why := tooNested(expr, maxExprArrs); why != "" {
		return "", &domain.CodeError{Kind: domain.CodeDidNotCompile, Message: strings.Replace(why, "the code", "the expression", 1)}
	}

	release, err := c.slot(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	// In parentheses, only an expression parses; on lines of their own, a // comment at
	// the end cannot swallow the closing one.
	result := api.Transform("(\n"+expr+"\n)", api.TransformOptions{
		Loader:      api.LoaderTS,
		Target:      api.ES2023,
		Charset:     api.CharsetUTF8,
		TsconfigRaw: "{}",
		LogLevel:    api.LogLevelSilent,
	})

	if len(result.Errors) > 0 {
		lines := strings.Split(expr, "\n")

		for i := range result.Errors {
			loc := result.Errors[i].Location
			if loc == nil {
				continue
			}

			loc.Line-- // the opening parenthesis's line

			// Stuck at the closing parenthesis: the expression stopped short.
			if loc.Line > len(lines) {
				loc.Line, loc.Column = len(lines), len(lines[len(lines)-1])
				result.Errors[i].Text = "Unexpected end of the expression"
			}
		}

		return "", problems(expr, result.Errors)
	}

	js := strings.TrimSuffix(strings.TrimSpace(string(result.Code)), ";")
	c.expressions.Put(expr, js)

	return js, nil
}

// slot waits for a compile's turn.
func (c *Compiler) slot(ctx context.Context) (func(), error) {
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// onlySDK refuses every import but the SDK's, which becomes the global the runtime sets;
// withWorkflow lets the builder in too, from the binary. Every path goes through them,
// so esbuild never looks for a file.
var (
	onlySDK      = imports(false)
	withWorkflow = imports(true)
)

func imports(workflow bool) api.Plugin {
	allowed := SDKModule
	if workflow {
		allowed = SDKModule + " and " + WorkflowModule
	}

	return api.Plugin{
		Name: "imports",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				switch {
				case args.Path == SDKModule:
					return api.OnResolveResult{Path: SDKModule, Namespace: "sdk"}, nil
				case args.Path == WorkflowModule && workflow:
					return api.OnResolveResult{Path: WorkflowModule, Namespace: "workflow"}, nil
				default:
					return api.OnResolveResult{}, fmt.Errorf("only %s can be imported, not %q", allowed, args.Path)
				}
			})

			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "sdk"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
				contents := "module.exports = globalThis." + SDKGlobal

				return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
			})

			// Its own import of the SDK is of types only, which esbuild drops.
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "workflow"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
				contents := workflowSource

				return api.OnLoadResult{Contents: &contents, Loader: api.LoaderTS}, nil
			})
		},
	}
}

// problems describes what did not compile, where. Columns count characters, as an
// editor does.
func problems(source string, msgs []api.Message) error {
	lines := strings.Split(source, "\n")
	out := make([]domain.CodeProblem, 0, len(msgs))

	for _, m := range msgs {
		p := domain.CodeProblem{Message: m.Text}

		if loc := m.Location; loc != nil {
			p.Line = loc.Line
			p.Column = loc.Column + 1

			if loc.Line >= 1 && loc.Line <= len(lines) && loc.Column <= len(lines[loc.Line-1]) {
				p.Column = utf8.RuneCountInString(lines[loc.Line-1][:loc.Column]) + 1
			}
		}

		out = append(out, p)
	}

	msg := describe(out[0])
	if len(out) > 1 {
		msg += fmt.Sprintf(" (and %d more)", len(out)-1)
	}

	return &domain.CodeError{Kind: domain.CodeDidNotCompile, Message: msg, Problems: out}
}

func describe(p domain.CodeProblem) string {
	if p.Line == 0 {
		return p.Message
	}

	return fmt.Sprintf("line %d, column %d: %s", p.Line, p.Column, p.Message)
}

func isCodeError(err error) bool {
	var codeErr *domain.CodeError
	return errors.As(err, &codeErr)
}
