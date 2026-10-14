// Command polythought runs the server and its operator tools.
//
//	polythought [serve]                       run the API server (default)
//	polythought admin create -email <email>   create an admin; the password is read from stdin
//	polythought demo [-once] [-interval 30s]  run the spread-watch demo workflow
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
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
	default:
		err = fmt.Errorf("unknown command %q (want serve, admin or demo)", cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "polythought:", err)
		os.Exit(1)
	}
}
