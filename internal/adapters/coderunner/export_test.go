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

// Restarts are the runners started again.
func (p *Pool) Restarts() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.restarts
}
