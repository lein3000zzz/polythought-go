package tscompiler

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/lein3000zzz/polythought-go/sdk"
)

// SDK builds the SDK into a script for the runtime's prelude. It sets SDKGlobal, which
// compiled code imports, and InvokeGlobal.
func SDK() (string, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints:      []string{sdk.TypeScriptEntry},
		Bundle:           true,
		Format:           api.FormatIIFE,
		GlobalName:       SDKGlobal,
		Platform:         api.PlatformNeutral,
		Target:           api.ES2023,
		Charset:          api.CharsetUTF8,
		TsconfigRaw:      "{}",
		MinifyWhitespace: true, // it is parsed on every run
		MinifySyntax:     true,
		LegalComments:    api.LegalCommentsNone,
		LogLevel:         api.LogLevelSilent,
		Write:            false,
		Plugins:          []api.Plugin{embedded},
	})

	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, m := range result.Errors {
			msgs[i] = m.Text
		}

		return "", fmt.Errorf("tscompiler: build the SDK: %s", strings.Join(msgs, "; "))
	}

	if len(result.OutputFiles) != 1 {
		return "", errors.New("tscompiler: build the SDK: not one file")
	}

	return string(result.OutputFiles[0].Contents), nil
}

// embedded reads the SDK's files from the binary, and nothing else.
var embedded = api.Plugin{
	Name: "embedded",
	Setup: func(b api.PluginBuild) {
		b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			p := path.Clean(args.Path) // embed.FS takes no ./
			if args.Importer != "" {
				p = path.Join(path.Dir(args.Importer), args.Path)
			}

			return api.OnResolveResult{Path: p, Namespace: "embedded"}, nil
		})

		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "embedded"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
			b, err := sdk.TypeScript.ReadFile(args.Path)
			if err != nil {
				return api.OnLoadResult{}, err
			}

			contents := string(b)
			loader := api.LoaderJS
			if strings.HasSuffix(args.Path, ".ts") {
				loader = api.LoaderTS
			}

			return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
		})
	},
}
