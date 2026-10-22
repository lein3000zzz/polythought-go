//go:build !linux

package pluginhost

// memoryOf is not watched here: only Linux has the sandbox, and /proc to read it from.
func memoryOf(int) (int64, bool) { return 0, false }
