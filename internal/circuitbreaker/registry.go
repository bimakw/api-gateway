package circuitbreaker

import (
	"sync"
)

// Registry manages multiple circuit breakers
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
	config   Config
}

func NewRegistry(config Config) *Registry {
	return &Registry{
		breakers: make(map[string]*CircuitBreaker),
		config:   config,
	}
}

func (r *Registry) Get(name string) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.breakers[name]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists = r.breakers[name]; exists {
		return cb
	}

	cb = New(name, r.config)
	r.breakers[name] = cb
	return cb
}

func (r *Registry) GetAll() map[string]*CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*CircuitBreaker, len(r.breakers))
	for k, v := range r.breakers {
		result[k] = v
	}
	return result
}

func (r *Registry) GetAllStats() []Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make([]Stats, 0, len(r.breakers))
	for _, cb := range r.breakers {
		stats = append(stats, cb.GetStats())
	}
	return stats
}

func (r *Registry) Reset() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cb := range r.breakers {
		cb.Reset()
	}
}

func (r *Registry) ResetByName(name string) bool {
	r.mu.RLock()
	cb, exists := r.breakers[name]
	r.mu.RUnlock()

	if !exists {
		return false
	}

	cb.Reset()
	return true
}
