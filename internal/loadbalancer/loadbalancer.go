package loadbalancer

import (
	"net/url"
	"sync"
)

type Backend struct {
	URL       *url.URL
	Weight    int
	IsHealthy bool
}

// Selector defines the interface for load balancing strategies
type Selector interface {
	// Select returns the next healthy backend, or nil if none available
	Select() *Backend
	// SetHealthy updates the health status of a backend
	SetHealthy(urlStr string, healthy bool)
	// GetBackends returns all backends
	GetBackends() []*Backend
}

// LoadBalancer manages backend selection with health awareness
type LoadBalancer struct {
	selector Selector
	mu       sync.RWMutex
}

func New(strategy string, backends []*Backend) *LoadBalancer {
	var selector Selector

	switch strategy {
	case "random":
		selector = NewRandomSelector(backends)
	default:
		// Default to round-robin
		selector = NewRoundRobinSelector(backends)
	}

	return &LoadBalancer{
		selector: selector,
	}
}

func (lb *LoadBalancer) Select() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.selector.Select()
}

func (lb *LoadBalancer) SetHealthy(urlStr string, healthy bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.selector.SetHealthy(urlStr, healthy)
}

func (lb *LoadBalancer) GetBackends() []*Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.selector.GetBackends()
}

func (lb *LoadBalancer) HealthyCount() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	count := 0
	for _, b := range lb.selector.GetBackends() {
		if b.IsHealthy {
			count++
		}
	}
	return count
}
