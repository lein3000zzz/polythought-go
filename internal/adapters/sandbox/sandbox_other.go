//go:build !linux

package sandbox

import (
	"fmt"
	"runtime"
)

// errOffLinux is why there is no sandbox here.
var errOffLinux = fmt.Errorf("%w: only Linux has the sandbox, and this is %s", ErrUnavailable, runtime.GOOS)

func available() error { return errOffLinux }

func become(Policy, func() error) error { return available() }
