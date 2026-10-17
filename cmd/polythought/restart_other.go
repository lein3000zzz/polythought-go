//go:build !unix

package main

import "errors"

func reexec() error {
	return errors.New("restart: not supported on this system; start the server again")
}
