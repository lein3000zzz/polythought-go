package jsruntime

// Compiles is how many scripts r compiled, its prelude included.
func Compiles(r *Runner) int64 {
	return r.compiles.Load()
}
