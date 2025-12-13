package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	ErrCircuitOpen    = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many requests in half-open state")
)

// Config holds circuit breaker configuration
type Config struct {
	// MaxFailures is the maximum number of failures before opening the circuit
	MaxFailures int
	// ResetTimeout is how long to wait before transitioning from open to half-open
	ResetTimeout time.Duration
	// HalfOpenMaxRequests is the maximum number of requests allowed in half-open state
	HalfOpenMaxRequests int
	// SuccessThreshold is the number of consecutive successes needed to close the circuit
	SuccessThreshold int
}

// DefaultConfig returns default circuit breaker configuration
func DefaultConfig() Config {
	return Config{
		MaxFailures:         5,
		ResetTimeout:        30 * time.Second,
		HalfOpenMaxRequests: 3,
		SuccessThreshold:    2,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name   string
	config Config

	mu                  sync.RWMutex
	state               State
	failures            int
	successes           int
	lastFailure         time.Time
	halfOpenRequests    int
	consecutiveSuccesses int
}

// New creates a new circuit breaker
func New(name string, config Config) *CircuitBreaker {
	if config.MaxFailures <= 0 {
		config.MaxFailures = 5
	}
	if config.ResetTimeout <= 0 {
		config.ResetTimeout = 30 * time.Second
	}
	if config.HalfOpenMaxRequests <= 0 {
		config.HalfOpenMaxRequests = 3
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 2
	}

	return &CircuitBreaker{
		name:   name,
		config: config,
		state:  StateClosed,
	}
}

// Execute runs the given function through the circuit breaker
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	err := fn()

	cb.afterRequest(err == nil)
	return err
}

// AllowRequest checks if a request is allowed
func (cb *CircuitBreaker) AllowRequest() bool {
	return cb.beforeRequest() == nil
}

// beforeRequest is called before each request
func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastFailure) >= cb.config.ResetTimeout {
			cb.toHalfOpen()
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// Limit requests in half-open state
		if cb.halfOpenRequests >= cb.config.HalfOpenMaxRequests {
			return ErrTooManyRequests
		}
		cb.halfOpenRequests++
		return nil
	}

	return nil
}

// afterRequest is called after each request completes
func (cb *CircuitBreaker) afterRequest(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		if success {
			cb.failures = 0
		} else {
			cb.failures++
			cb.lastFailure = time.Now()
			if cb.failures >= cb.config.MaxFailures {
				cb.toOpen()
			}
		}

	case StateHalfOpen:
		if success {
			cb.consecutiveSuccesses++
			if cb.consecutiveSuccesses >= cb.config.SuccessThreshold {
				cb.toClosed()
			}
		} else {
			cb.toOpen()
		}
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.afterRequest(true)
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.afterRequest(false)
}

// State transitions
func (cb *CircuitBreaker) toOpen() {
	cb.state = StateOpen
	cb.lastFailure = time.Now()
	cb.consecutiveSuccesses = 0
}

func (cb *CircuitBreaker) toClosed() {
	cb.state = StateClosed
	cb.failures = 0
	cb.consecutiveSuccesses = 0
	cb.halfOpenRequests = 0
}

func (cb *CircuitBreaker) toHalfOpen() {
	cb.state = StateHalfOpen
	cb.halfOpenRequests = 0
	cb.consecutiveSuccesses = 0
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStats returns circuit breaker statistics
func (cb *CircuitBreaker) GetStats() Stats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return Stats{
		Name:                 cb.name,
		State:                cb.state.String(),
		Failures:             cb.failures,
		ConsecutiveSuccesses: cb.consecutiveSuccesses,
		LastFailure:          cb.lastFailure,
	}
}

// Stats represents circuit breaker statistics
type Stats struct {
	Name                 string    `json:"name"`
	State                string    `json:"state"`
	Failures             int       `json:"failures"`
	ConsecutiveSuccesses int       `json:"consecutive_successes"`
	LastFailure          time.Time `json:"last_failure,omitempty"`
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.toClosed()
}
