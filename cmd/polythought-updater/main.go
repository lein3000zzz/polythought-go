// Command polythought-updater is Polythought's updater (D425), run beside the stack by the
// Compose file's updater profile with the Docker socket:
//
//	polythought-updater [serve]   serve the server's asks to update
//	polythought-updater apply …   an update, in a container of its own (the updater runs it)
//	polythought-updater version
//
// serve reads POLYTHOUGHT_UPDATER_TOKEN, the token the server sends, at least 32
// characters; POLYTHOUGHT_UPDATER_ADDR, :8080 unless set; POLYTHOUGHT_UPDATER_SERVICES,
// the services to update, api,web unless set; and POLYTHOUGHT_UPDATER_PROJECT and
// POLYTHOUGHT_UPDATER_IMAGE, the stack's Compose project and the image an update runs in,
// the updater's own unless set.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging/slogging"

	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
	"github.com/lein3000zzz/polythought-go/internal/updater"
)

// binary is the updater's path in its image, where an update runs it again.
const binary = "/usr/local/bin/polythought-updater"

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
		err = serve(ctx)
	case "apply":
		err = apply(ctx, args)
	case "version":
		fmt.Println("polythought-updater", buildinfo.Get())
	default:
		err = fmt.Errorf("unknown command %q (want serve, apply or version)", cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "polythought-updater:", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context) error {
	log, err := slogging.NewSlogLoggerWithConfig(slogging.Config{Level: logging.LevelInfo, Format: logging.FormatText, ToStdout: true})
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	self, err := os.Hostname()
	if err != nil {
		return err
	}

	docker := updater.Docker{
		Run: updater.CLI{}, Self: self, Services: list(env("POLYTHOUGHT_UPDATER_SERVICES", "api,web")),
		Project: os.Getenv("POLYTHOUGHT_UPDATER_PROJECT"), Image: os.Getenv("POLYTHOUGHT_UPDATER_IMAGE"), Binary: binary,
	}

	s, err := updater.NewServer(ctx, os.Getenv("POLYTHOUGHT_UPDATER_TOKEN"), docker.Update, log)
	if err != nil {
		return fmt.Errorf("POLYTHOUGHT_UPDATER_TOKEN: %w", err)
	}

	srv := &http.Server{
		Addr: env("POLYTHOUGHT_UPDATER_ADDR", ":8080"), Handler: s.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()

		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = srv.Shutdown(shutdown)
	}()

	log.Info(ctx, "Updater listening", "addr", srv.Addr, "version", buildinfo.Get().Version, "services", docker.Services)

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func apply(ctx context.Context, args []string) error {
	var (
		a                        = updater.Apply{Run: updater.CLI{}}
		files, envFiles, service repeated
	)

	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.StringVar(&a.Version, "version", "", "the release to update to, such as 0.1.2")
	fs.StringVar(&a.Project, "project", "", "the stack's Compose project")
	fs.StringVar(&a.ProjectDir, "project-directory", "", "its project directory")
	fs.Var(&files, "file", "a Compose file it was started with; repeat for each")
	fs.Var(&envFiles, "env-file", "an env file it was started with; repeat for each, the version kept in the last")
	fs.Var(&service, "service", "a service to update; repeat for each")
	fs.StringVar(&a.Self, "self", "", "the updater's own service, updated after them as it can")

	if err := fs.Parse(args); err != nil {
		return err
	}

	a.Files, a.EnvFiles, a.Services = files, envFiles, service

	return a.Do(ctx, os.Stdout)
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	return fallback
}

func list(s string) []string {
	var out []string

	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// repeated is a flag given once for each value.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}
