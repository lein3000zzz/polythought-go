// Package tscompiler turns user TypeScript into JavaScript for the runtime, with esbuild.
// Code may import only the SDK, which the runtime provides as a global: nothing from npm,
// the file system or the network gets in. Compiled code is cached in memory by the
// source's hash; compiling again after a restart takes a millisecond.
package tscompiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	opCompile  = "tscompiler.Compile"

	// SDKModule is the one module code may import. It reads SDKGlobal, which the
	// runtime sets.
	SDKModule = "@polythought/sdk"
	SDKGlobal = "__polythought_sdk"

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

var _ ports.CodeCompiler = (*Compiler)(nil)

type Compiler struct {
	cache       *lru.Cache[string, string] // scripts by the source's hash
	expressions *lru.Cache[string, string] // JavaScript by the expression
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
		kit:         kit,
		scope:       tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
	}, nil
}

func (c *Compiler) Compile(ctx context.Context, source string) (code ports.CompiledCode, err error) {
	done := c.kit.Start(ctx, opCompile)

	defer func() { done(err == nil || isCodeError(err)) }() // code that does not compile is not the compiler failing

	return tracing.InScope(ctx, c.scope, opCompile, func(_ context.Context, span trace.Span) (ports.CompiledCode, error) {
		span.SetAttributes(attribute.Int("code.size", len(source)))

		if len(source) > MaxSource {
			return ports.CompiledCode{}, &domain.CodeError{
				Kind:    domain.CodeDidNotCompile,
				Message: fmt.Sprintf("the code is %d KiB, more than the %d KiB allowed", len(source)>>10, MaxSource>>10),
			}
		}

		sum := sha256.Sum256([]byte(source))
		hash := hex.EncodeToString(sum[:])

		script, cached := c.cache.Get(hash)
		span.SetAttributes(attribute.Bool("code.cached", cached))

		if !cached {
			if script, err = compile(source); err != nil {
				return ports.CompiledCode{}, err
			}

			c.cache.Put(hash, script)
		}

		return ports.CompiledCode{Script: script, Exports: ExportsGlobal, Hash: hash, Invoke: InvokeGlobal}, nil
	})
}

func compile(source string) (string, error) {
	result := api.Build(api.BuildOptions{
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
	})

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
func (c *Compiler) CompileExpression(_ context.Context, expr string) (string, error) {
	if len(expr) > MaxExpression {
		return "", &domain.CodeError{
			Kind:    domain.CodeDidNotCompile,
			Message: fmt.Sprintf("the expression is %d KiB, more than the %d KiB allowed", len(expr)>>10, MaxExpression>>10),
		}
	}

	if js, ok := c.expressions.Get(expr); ok {
		return js, nil
	}

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

// onlySDK refuses every import but the SDK's, which becomes the global the runtime sets.
// Every path goes through it, so esbuild never looks for a file.
var onlySDK = api.Plugin{
	Name: "only-sdk",
	Setup: func(b api.PluginBuild) {
		b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			if args.Path != SDKModule {
				return api.OnResolveResult{}, fmt.Errorf("only %s can be imported, not %q", SDKModule, args.Path)
			}

			return api.OnResolveResult{Path: SDKModule, Namespace: "sdk"}, nil
		})

		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "sdk"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
			contents := "module.exports = globalThis." + SDKGlobal

			return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
		})
	},
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
