//go:build linux

package pluginhost

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// memoryOf is the memory a process takes, its resident set (D343); false when it cannot
// be read.
func memoryOf(pid int) (int64, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, false
	}

	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, false
	}

	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}

	return pages * int64(os.Getpagesize()), true
}
