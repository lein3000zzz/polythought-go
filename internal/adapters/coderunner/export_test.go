package coderunner

// Pids are the runners' processes, those that run.
func (p *Pool) Pids() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []int

	for _, r := range p.all {
		if pr := r.current(); pr != nil {
			out = append(out, pr.pid())
		}
	}

	return out
}
