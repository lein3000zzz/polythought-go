package pluginhost

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// block is what the data cap counts every directory as, and every file as at least: about
// what the disk gives each, so a great many small files count as what they take.
const block = 4 << 10

// Usage is what dirs take on disk, as the data cap counts it (D341): each file's size,
// rounded up to a block, and a block for anything else. A directory not there takes
// nothing.
func Usage(dirs ...string) int64 {
	var total int64

	for _, dir := range dirs {
		if dir == "" {
			continue
		}

		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // gone meanwhile, or not there at all: nothing taken
			}

			n := int64(block)

			if d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					n = max(n, (info.Size()+block-1)/block*block)
				}
			}

			total += n

			return nil
		})
	}

	return total
}

// mib is bytes as admins read them.
func mib(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
