package loadbalancer

import (
	"math/rand"
)

// RandomSelector implements random load balancing
type RandomSelector struct {
	backends []*Backend
}

// NewRandomSelector creates a new random selector
func NewRandomSelector(backends []*Backend) *RandomSelector {
	return &RandomSelector{
		backends: backends,
	}
}

// Select returns a random healthy backend
// Returns nil if no healthy backends are available
func (r *RandomSelector) Select() *Backend {
	if len(r.backends) == 0 {
		return nil
	}

	// Collect healthy backends
	healthy := make([]*Backend, 0, len(r.backends))
	for _, b := range r.backends {
		if b.IsHealthy {
			healthy = append(healthy, b)
		}
	}

	if len(healthy) == 0 {
		return nil
	}

	// Select random healthy backend
	return healthy[rand.Intn(len(healthy))]
}

// SetHealthy updates the health status of a backend
func (r *RandomSelector) SetHealthy(urlStr string, healthy bool) {
	for _, b := range r.backends {
		if b.URL.String() == urlStr {
			b.IsHealthy = healthy
			return
		}
	}
}

// GetBackends returns all backends
func (r *RandomSelector) GetBackends() []*Backend {
	return r.backends
}
