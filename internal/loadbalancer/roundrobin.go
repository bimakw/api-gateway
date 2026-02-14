package loadbalancer

import (
	"sync/atomic"
)

type RoundRobinSelector struct {
	backends []*Backend
	counter  uint64
}

func NewRoundRobinSelector(backends []*Backend) *RoundRobinSelector {
	return &RoundRobinSelector{
		backends: backends,
		counter:  0,
	}
}

// Returns nil if no healthy backends are available
func (r *RoundRobinSelector) Select() *Backend {
	if len(r.backends) == 0 {
		return nil
	}

	// Count healthy backends
	healthyCount := 0
	for _, b := range r.backends {
		if b.IsHealthy {
			healthyCount++
		}
	}

	if healthyCount == 0 {
		return nil
	}

	// Find the next healthy backend
	n := len(r.backends)
	for i := 0; i < n; i++ {
		idx := atomic.AddUint64(&r.counter, 1) % uint64(n)
		backend := r.backends[idx]
		if backend.IsHealthy {
			return backend
		}
	}

	return nil
}

func (r *RoundRobinSelector) SetHealthy(urlStr string, healthy bool) {
	for _, b := range r.backends {
		if b.URL.String() == urlStr {
			b.IsHealthy = healthy
			return
		}
	}
}

func (r *RoundRobinSelector) GetBackends() []*Backend {
	return r.backends
}
