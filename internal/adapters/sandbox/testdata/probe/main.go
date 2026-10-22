// Probe tries what a sandboxed program may and may not do, and says how each went, as
// JSON: "ok", or the error.
package main

import (
	"encoding/json"
	"os"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox/internal/probe"
)

func main() {
	if len(os.Args) > 1 {
		return // run again by itself
	}

	tries := probe.Try(os.Getenv("PROBE_DIR"), os.Getenv("PROBE_DATA"), os.Getenv("PROBE_OUTSIDE"), []string{os.Args[0], "again"})
	_ = json.NewEncoder(os.Stdout).Encode(tries)
}
