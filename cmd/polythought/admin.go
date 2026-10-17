package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// admin create -email <email>: the password comes from stdin so it never lands in shell
// history or the process list, e.g. `printf '%s' "$PASS" | polythought admin create -email a@b.c`.
func admin(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: polythought admin create -email <email>  (password on stdin)")
	}

	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	email := fs.String("email", "", "email of the new admin")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if *email == "" {
		return errors.New("-email is required")
	}

	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Password (input is visible; pipe it in to hide it): ")
	}

	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && password == "" {
		return fmt.Errorf("read password: %w", err)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer shutdown(a.util)

	user, err := a.auth.BootstrapAdmin(ctx, *email, strings.TrimRight(password, "\r\n"))
	if err != nil {
		return err
	}

	fmt.Printf("Admin %s created (id %s)\n", user.Email, user.ID)

	return nil
}
