package loadbalancer

import (
	"math/rand"
)

type RandomSelector struct {
	backends []*Backend
}

func NewRandomSelector(backends []*Backend) *RandomSelector {
	return &RandomSelector{
		backends: backends,
	}
}

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

func (r *RandomSelector) SetHealthy(urlStr string, healthy bool) {
	for _, b := range r.backends {
		if b.URL.String() == urlStr {
			b.IsHealthy = healthy
			return
		}
	}
}

func (r *RandomSelector) GetBackends() []*Backend {
	return r.backends
}
