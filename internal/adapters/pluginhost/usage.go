package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// block is what the data cap counts every directory as, and every file as at least: about
// what the disk gives each, so a great many small files count as what they take.
const block = 4 << 10

// Usage is what dirs take on disk, as the data cap counts it (D341): each file's size,
// rounded up to a block, and a block for anything else. A directory not there takes
// nothing; an entry that cannot be measured is passed over.
func Usage(dirs ...string) int64 {
	n, _ := taken(dirs...)
	return n
}

// taken is Usage, saying why when an entry cannot be measured, other than one gone
// meanwhile (P9): one whose path is past what the system takes, say, or that the server
// may not read, where a plugin's data would hide from its cap. The error is the system's
// alone, without the entry's path, which the plugin names.
func taken(dirs ...string) (int64, error) {
	var (
		total int64
		first error
	)

	for _, dir := range dirs {
		if dir == "" {
			continue
		}

		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			n := int64(block)

			if err == nil && d.Type().IsRegular() {
				var info fs.FileInfo
				if info, err = d.Info(); err == nil {
					n = rounded(info.Size())
				}
			}

			switch {
			case errors.Is(err, fs.ErrNotExist):
				return nil // gone meanwhile, or not there at all: nothing taken
			case err != nil:
				if first == nil {
					first = bare(err)
				}

				return nil // the rest still measured
			}

			total += n

			return nil
		})
	}

	return total, first
}

// rounded is a file's size as the data cap counts it: up to a block, a block at least.
func rounded(size int64) int64 {
	return max(block, (size+block-1)/block*block)
}

// bare is err without the path it names.
func bare(err error) error {
	if pe := new(fs.PathError); errors.As(err, &pe) {
		return pe.Err
	}

	return err
}

// mib is bytes as admins read them.
func mib(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
