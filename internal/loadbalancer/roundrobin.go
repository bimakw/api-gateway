package loadbalancer

import (
	"sync/atomic"
)

// RoundRobinSelector implements round-robin load balancing
type RoundRobinSelector struct {
	backends []*Backend
	counter  uint64
}

// NewRoundRobinSelector creates a new round-robin selector
func NewRoundRobinSelector(backends []*Backend) *RoundRobinSelector {
	return &RoundRobinSelector{
		backends: backends,
		counter:  0,
	}
}

// Select returns the next healthy backend using round-robin
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

// SetHealthy updates the health status of a backend
func (r *RoundRobinSelector) SetHealthy(urlStr string, healthy bool) {
	for _, b := range r.backends {
		if b.URL.String() == urlStr {
			b.IsHealthy = healthy
			return
		}
	}
}

// GetBackends returns all backends
func (r *RoundRobinSelector) GetBackends() []*Backend {
	return r.backends
}
