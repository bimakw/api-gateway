package loadbalancer

import (
	"net/url"
	"sync"
)

// Backend represents a single backend server instance
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

// New creates a new LoadBalancer with the specified strategy
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

// Select returns the next healthy backend
func (lb *LoadBalancer) Select() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.selector.Select()
}

// SetHealthy updates the health status of a backend by URL
func (lb *LoadBalancer) SetHealthy(urlStr string, healthy bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.selector.SetHealthy(urlStr, healthy)
}

// GetBackends returns all backends
func (lb *LoadBalancer) GetBackends() []*Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.selector.GetBackends()
}

// HealthyCount returns the number of healthy backends
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
