// Command polythought runs the server and its operator tools.
//
//	polythought [--config <file>] [serve]     run the API server (default)
//	polythought admin create -email <email>   create an admin; the password is read from stdin
//	polythought demo [-once] [-interval 30s]  run the spread-watch demo workflow
//
// --config names the YAML config file, or POLYTHOUGHT_CONFIG does; without either, it is
// configs/polythought.yml when that is there (D70).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/lein3000zzz/polythought-go/internal/adapters/coderunner"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
)

func main() {
	// The server's binary is plugins' sandbox launcher too (D337), and a code runner
	// (D356): run as one, it becomes the plugin, or runs the code, and never returns.
	sandbox.Launch(coderunner.Entry)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	args, configPath = configFlag(args)
	cmd := "serve"

	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	var err error

	switch cmd {
	case "serve":
		if err = serve(ctx); errors.Is(err, errRestart) {
			stop()
			err = reexec()
		}
	case "admin":
		err = admin(ctx, args)
	case "demo":
		err = demo(ctx, args)
	case "version":
		fmt.Println("polythought", buildinfo.Get())
	default:
		err = fmt.Errorf("unknown command %q (want serve, admin, demo or version)", cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "polythought:", err)
		os.Exit(1)
	}
}

// configPath is the config file --config names; empty when it does not.
var configPath string

// configFlag takes --config <file> or --config=<file> from before the command.
func configFlag(args []string) ([]string, string) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "--config" || a == "-config") && i+1 < len(args):
			return append(slices.Clone(args[:i]), args[i+2:]...), args[i+1]
		case strings.HasPrefix(a, "--config="), strings.HasPrefix(a, "-config="):
			return append(slices.Clone(args[:i]), args[i+1:]...), a[strings.IndexByte(a, '=')+1:]
		case !strings.HasPrefix(a, "-"):
			return args, "" // the command: flags after it are its own
		}
	}

	return args, ""
}
