package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

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
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many requests in half-open state")
)

type Config struct {
	MaxFailures         int
	ResetTimeout        time.Duration
	HalfOpenMaxRequests int
	SuccessThreshold    int
}

func DefaultConfig() Config {
	return Config{
		MaxFailures:         7,
		ResetTimeout:        45 * time.Second,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    3,
	}
}

type CircuitBreaker struct {
	name   string
	config Config

	mu                   sync.RWMutex
	state                State
	failures             int
	successes            int
	lastFailure          time.Time
	halfOpenRequests     int
	consecutiveSuccesses int
}

func New(name string, config Config) *CircuitBreaker {
	if config.MaxFailures <= 0 {
		config.MaxFailures = 7
	}
	if config.ResetTimeout <= 0 {
		config.ResetTimeout = 45 * time.Second
	}
	if config.HalfOpenMaxRequests <= 0 {
		config.HalfOpenMaxRequests = 2
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 3
	}

	return &CircuitBreaker{
		name:   name,
		config: config,
		state:  StateClosed,
	}
}

func (cb *CircuitBreaker) Execute(fn func() error) error {
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	err := fn()

	cb.afterRequest(err == nil)
	return err
}

func (cb *CircuitBreaker) AllowRequest() bool {
	return cb.beforeRequest() == nil
}

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

func (cb *CircuitBreaker) RecordSuccess() {
	cb.afterRequest(true)
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.afterRequest(false)
}

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

func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

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

type Stats struct {
	Name                 string    `json:"name"`
	State                string    `json:"state"`
	Failures             int       `json:"failures"`
	ConsecutiveSuccesses int       `json:"consecutive_successes"`
	LastFailure          time.Time `json:"last_failure,omitempty"`
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.toClosed()
}
