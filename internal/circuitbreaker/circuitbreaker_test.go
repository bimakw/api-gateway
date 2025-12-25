package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("State.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxFailures != 5 {
		t.Errorf("MaxFailures = %d, want 5", cfg.MaxFailures)
	}
	if cfg.ResetTimeout != 30*time.Second {
		t.Errorf("ResetTimeout = %v, want 30s", cfg.ResetTimeout)
	}
	if cfg.HalfOpenMaxRequests != 3 {
		t.Errorf("HalfOpenMaxRequests = %d, want 3", cfg.HalfOpenMaxRequests)
	}
	if cfg.SuccessThreshold != 2 {
		t.Errorf("SuccessThreshold = %d, want 2", cfg.SuccessThreshold)
	}
}

func TestNewCircuitBreaker(t *testing.T) {
	cb := New("test", DefaultConfig())

	if cb.name != "test" {
		t.Errorf("name = %s, want test", cb.name)
	}
	if cb.GetState() != StateClosed {
		t.Errorf("initial state = %v, want Closed", cb.GetState())
	}
}

func TestNewWithZeroConfig(t *testing.T) {
	cb := New("test", Config{})

	// Should use defaults for zero values
	if cb.config.MaxFailures != 5 {
		t.Errorf("MaxFailures = %d, want 5 (default)", cb.config.MaxFailures)
	}
	if cb.config.ResetTimeout != 30*time.Second {
		t.Errorf("ResetTimeout = %v, want 30s (default)", cb.config.ResetTimeout)
	}
}

func TestCircuitBreakerClosedState(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         3,
		ResetTimeout:        100 * time.Millisecond,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	})

	// Should allow requests in closed state
	if !cb.AllowRequest() {
		t.Error("closed circuit should allow requests")
	}

	// Success should reset failures
	cb.RecordSuccess()
	stats := cb.GetStats()
	if stats.Failures != 0 {
		t.Errorf("failures after success = %d, want 0", stats.Failures)
	}
}

func TestCircuitBreakerOpensAfterMaxFailures(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         3,
		ResetTimeout:        1 * time.Second,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	})

	// Record failures up to threshold
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Circuit should now be open
	if cb.GetState() != StateOpen {
		t.Errorf("state = %v, want Open", cb.GetState())
	}

	// Should not allow requests when open
	if cb.AllowRequest() {
		t.Error("open circuit should not allow requests")
	}
}

func TestCircuitBreakerTransitionsToHalfOpen(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	})

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.GetState() != StateOpen {
		t.Fatalf("state = %v, want Open", cb.GetState())
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Next request should transition to half-open
	if !cb.AllowRequest() {
		t.Error("should allow request after reset timeout")
	}

	if cb.GetState() != StateHalfOpen {
		t.Errorf("state = %v, want HalfOpen", cb.GetState())
	}
}

func TestCircuitBreakerHalfOpenLimitsRequests(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         1,
		ResetTimeout:        1 * time.Millisecond,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	})

	// Open the circuit
	cb.RecordFailure()
	time.Sleep(5 * time.Millisecond)

	// First AllowRequest transitions from Open to HalfOpen (no increment yet)
	if !cb.AllowRequest() {
		t.Error("first request should be allowed (transition to half-open)")
	}

	// Second request: already in half-open, increments counter to 1
	if !cb.AllowRequest() {
		t.Error("second request should be allowed (counter=1)")
	}

	// Third request: increments counter to 2
	if !cb.AllowRequest() {
		t.Error("third request should be allowed (counter=2)")
	}

	// Fourth request should be denied (counter >= HalfOpenMaxRequests)
	if cb.AllowRequest() {
		t.Error("half-open should limit requests after HalfOpenMaxRequests")
	}
}

func TestCircuitBreakerClosesAfterSuccessThreshold(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         1,
		ResetTimeout:        1 * time.Millisecond,
		HalfOpenMaxRequests: 5,
		SuccessThreshold:    2,
	})

	// Open and transition to half-open
	cb.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	cb.AllowRequest()

	// Record enough successes to close
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.GetState() != StateClosed {
		t.Errorf("state = %v, want Closed", cb.GetState())
	}
}

func TestCircuitBreakerReopensOnHalfOpenFailure(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         1,
		ResetTimeout:        1 * time.Millisecond,
		HalfOpenMaxRequests: 5,
		SuccessThreshold:    2,
	})

	// Open and transition to half-open
	cb.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	cb.AllowRequest()

	// One success then a failure
	cb.RecordSuccess()
	cb.RecordFailure()

	if cb.GetState() != StateOpen {
		t.Errorf("state = %v, want Open", cb.GetState())
	}
}

func TestCircuitBreakerExecute(t *testing.T) {
	cb := New("test", DefaultConfig())

	// Successful execution
	err := cb.Execute(func() error {
		return nil
	})

	if err != nil {
		t.Errorf("Execute() error = %v, want nil", err)
	}

	// Failed execution
	expectedErr := errors.New("test error")
	err = cb.Execute(func() error {
		return expectedErr
	})

	if err != expectedErr {
		t.Errorf("Execute() error = %v, want %v", err, expectedErr)
	}
}

func TestCircuitBreakerExecuteReturnsErrorWhenOpen(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:  1,
		ResetTimeout: 1 * time.Hour, // Long timeout
	})

	// Open the circuit
	cb.Execute(func() error { return errors.New("fail") })

	// Next execution should return circuit open error
	err := cb.Execute(func() error { return nil })

	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Execute() error = %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:  1,
		ResetTimeout: 1 * time.Hour,
	})

	// Open the circuit
	cb.RecordFailure()

	if cb.GetState() != StateOpen {
		t.Fatalf("state = %v, want Open", cb.GetState())
	}

	// Reset
	cb.Reset()

	if cb.GetState() != StateClosed {
		t.Errorf("state after reset = %v, want Closed", cb.GetState())
	}
}

func TestCircuitBreakerGetStats(t *testing.T) {
	cb := New("test-cb", DefaultConfig())

	cb.RecordFailure()
	cb.RecordFailure()

	stats := cb.GetStats()

	if stats.Name != "test-cb" {
		t.Errorf("Name = %s, want test-cb", stats.Name)
	}
	if stats.State != "closed" {
		t.Errorf("State = %s, want closed", stats.State)
	}
	if stats.Failures != 2 {
		t.Errorf("Failures = %d, want 2", stats.Failures)
	}
}

func TestCircuitBreakerConcurrency(t *testing.T) {
	cb := New("test", Config{
		MaxFailures:         100,
		ResetTimeout:        1 * time.Second,
		HalfOpenMaxRequests: 10,
		SuccessThreshold:    5,
	})

	done := make(chan bool)

	// Concurrent access
	for i := 0; i < 50; i++ {
		go func() {
			cb.AllowRequest()
			cb.RecordSuccess()
			cb.RecordFailure()
			cb.GetState()
			cb.GetStats()
			done <- true
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}

	// Should not panic
}
