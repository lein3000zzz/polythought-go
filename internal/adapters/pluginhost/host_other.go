//go:build !unix

package pluginhost

import "os/exec"

// ownSession is unix's alone (P8).
func ownSession(*exec.Cmd) {}

// unlinked is Linux's alone (P9).
func unlinked(int) (int64, error) { return 0, nil }
