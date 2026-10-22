//go:build !linux

package sandbox

// Memory is not watched here: only Linux has the sandbox, and /proc to read it from.
func Memory(int) (int64, bool) { return 0, false }
