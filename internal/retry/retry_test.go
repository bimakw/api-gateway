package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.InitialDelay != 100*time.Millisecond {
		t.Errorf("InitialDelay = %v, want 100ms", cfg.InitialDelay)
	}
	if cfg.MaxDelay != 5*time.Second {
		t.Errorf("MaxDelay = %v, want 5s", cfg.MaxDelay)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("Multiplier = %f, want 2.0", cfg.Multiplier)
	}
	if cfg.JitterFactor != 0.1 {
		t.Errorf("JitterFactor = %f, want 0.1", cfg.JitterFactor)
	}
	if len(cfg.RetryableStatusCodes) != 3 {
		t.Errorf("RetryableStatusCodes length = %d, want 3", len(cfg.RetryableStatusCodes))
	}
}

func TestNewWithDefaults(t *testing.T) {
	r := New(Config{})

	// Should use defaults for zero values
	if r.config.MaxRetries < 0 {
		t.Error("MaxRetries should not be negative")
	}
	if r.config.InitialDelay <= 0 {
		t.Error("InitialDelay should be positive")
	}
	if r.config.MaxDelay <= 0 {
		t.Error("MaxDelay should be positive")
	}
	if r.config.Multiplier <= 0 {
		t.Error("Multiplier should be positive")
	}
}

func TestNewWithNegativeValues(t *testing.T) {
	r := New(Config{
		MaxRetries:   -1,
		InitialDelay: -100 * time.Millisecond,
		MaxDelay:     -5 * time.Second,
		Multiplier:   -2.0,
		JitterFactor: -0.5,
	})

	if r.config.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0", r.config.MaxRetries)
	}
	if r.config.InitialDelay != 100*time.Millisecond {
		t.Errorf("InitialDelay = %v, want 100ms", r.config.InitialDelay)
	}
	if r.config.MaxDelay != 5*time.Second {
		t.Errorf("MaxDelay = %v, want 5s", r.config.MaxDelay)
	}
	if r.config.Multiplier != 2.0 {
		t.Errorf("Multiplier = %f, want 2.0", r.config.Multiplier)
	}
	if r.config.JitterFactor != 0.1 {
		t.Errorf("JitterFactor = %f, want 0.1", r.config.JitterFactor)
	}
}

func TestNewWithJitterOutOfRange(t *testing.T) {
	r := New(Config{
		JitterFactor: 1.5, // Out of range (should be 0-1)
	})

	if r.config.JitterFactor != 0.1 {
		t.Errorf("JitterFactor = %f, want 0.1 (default)", r.config.JitterFactor)
	}
}

func TestShouldRetry(t *testing.T) {
	r := New(DefaultConfig())

	tests := []struct {
		statusCode int
		expected   bool
	}{
		{http.StatusOK, false},
		{http.StatusCreated, false},
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusNotFound, false},
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, true},         // 502
		{http.StatusServiceUnavailable, true}, // 503
		{http.StatusGatewayTimeout, true},     // 504
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.statusCode), func(t *testing.T) {
			if got := r.ShouldRetry(tt.statusCode); got != tt.expected {
				t.Errorf("ShouldRetry(%d) = %v, want %v", tt.statusCode, got, tt.expected)
			}
		})
	}
}

func TestShouldRetryWithCustomCodes(t *testing.T) {
	r := New(Config{
		RetryableStatusCodes: []int{http.StatusTooManyRequests, http.StatusInternalServerError},
	})

	if !r.ShouldRetry(http.StatusTooManyRequests) {
		t.Error("Should retry on 429")
	}
	if !r.ShouldRetry(http.StatusInternalServerError) {
		t.Error("Should retry on 500")
	}
	if r.ShouldRetry(http.StatusBadGateway) {
		t.Error("Should not retry on 502 with custom codes")
	}
}

func TestGetDelay(t *testing.T) {
	r := New(Config{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0, // No jitter for predictable tests
	})

	// Attempt 0 should be initial delay
	delay0 := r.GetDelay(0)
	if delay0 != 100*time.Millisecond {
		t.Errorf("GetDelay(0) = %v, want 100ms", delay0)
	}

	// Attempt 1 should be 100ms * 2 = 200ms
	delay1 := r.GetDelay(1)
	if delay1 != 200*time.Millisecond {
		t.Errorf("GetDelay(1) = %v, want 200ms", delay1)
	}

	// Attempt 2 should be 100ms * 4 = 400ms
	delay2 := r.GetDelay(2)
	if delay2 != 400*time.Millisecond {
		t.Errorf("GetDelay(2) = %v, want 400ms", delay2)
	}

	// Attempt 3 should be 100ms * 8 = 800ms
	delay3 := r.GetDelay(3)
	if delay3 != 800*time.Millisecond {
		t.Errorf("GetDelay(3) = %v, want 800ms", delay3)
	}

	// Attempt 4 should be capped at 1s (MaxDelay)
	delay4 := r.GetDelay(4)
	if delay4 != 1*time.Second {
		t.Errorf("GetDelay(4) = %v, want 1s (capped)", delay4)
	}
}

func TestGetDelayWithJitter(t *testing.T) {
	r := New(Config{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.1,
	})

	// With jitter, delay should be within 10% of base
	baseDelay := 100 * time.Millisecond
	minDelay := time.Duration(float64(baseDelay) * 0.9)
	maxDelay := time.Duration(float64(baseDelay) * 1.1)

	for i := 0; i < 10; i++ {
		delay := r.GetDelay(0)
		if delay < minDelay || delay > maxDelay {
			t.Errorf("GetDelay(0) with jitter = %v, want between %v and %v", delay, minDelay, maxDelay)
		}
	}
}

func TestGetDelayNegativeAttempt(t *testing.T) {
	r := New(Config{
		InitialDelay: 100 * time.Millisecond,
		JitterFactor: 0,
	})

	delay := r.GetDelay(-1)
	if delay != 100*time.Millisecond {
		t.Errorf("GetDelay(-1) = %v, want 100ms", delay)
	}
}

func TestMaxRetries(t *testing.T) {
	r := New(Config{MaxRetries: 5})

	if r.MaxRetries() != 5 {
		t.Errorf("MaxRetries() = %d, want 5", r.MaxRetries())
	}
}

func TestExecuteSuccess(t *testing.T) {
	r := New(Config{MaxRetries: 3})

	callCount := 0
	result := r.Execute(context.Background(), func() (int, error) {
		callCount++
		return http.StatusOK, nil
	})

	if result.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", result.Attempts)
	}
	if result.Retried {
		t.Error("Retried should be false")
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if result.LastError != nil {
		t.Errorf("LastError = %v, want nil", result.LastError)
	}
	if callCount != 1 {
		t.Errorf("Function called %d times, want 1", callCount)
	}
}

func TestExecuteRetryOnStatus(t *testing.T) {
	r := New(Config{
		MaxRetries:           3,
		InitialDelay:         1 * time.Millisecond,
		RetryableStatusCodes: []int{http.StatusServiceUnavailable},
	})

	callCount := 0
	result := r.Execute(context.Background(), func() (int, error) {
		callCount++
		if callCount < 3 {
			return http.StatusServiceUnavailable, nil
		}
		return http.StatusOK, nil
	})

	if result.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", result.Attempts)
	}
	if !result.Retried {
		t.Error("Retried should be true")
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
}

func TestExecuteRetryOnTransientError(t *testing.T) {
	r := New(Config{
		MaxRetries:   3,
		InitialDelay: 1 * time.Millisecond,
	})

	callCount := 0
	result := r.Execute(context.Background(), func() (int, error) {
		callCount++
		if callCount < 3 {
			return 0, errors.New("connection refused")
		}
		return http.StatusOK, nil
	})

	if result.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", result.Attempts)
	}
	if !result.Retried {
		t.Error("Retried should be true")
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
}

func TestExecuteNoRetryOnNonTransientError(t *testing.T) {
	r := New(Config{
		MaxRetries:   3,
		InitialDelay: 1 * time.Millisecond,
	})

	callCount := 0
	result := r.Execute(context.Background(), func() (int, error) {
		callCount++
		return 0, errors.New("permanent error")
	})

	if result.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (no retry for non-transient)", result.Attempts)
	}
	if result.Retried {
		t.Error("Retried should be false")
	}
}

func TestExecuteExhaustsRetries(t *testing.T) {
	r := New(Config{
		MaxRetries:           2,
		InitialDelay:         1 * time.Millisecond,
		RetryableStatusCodes: []int{http.StatusServiceUnavailable},
	})

	callCount := 0
	result := r.Execute(context.Background(), func() (int, error) {
		callCount++
		return http.StatusServiceUnavailable, nil
	})

	if result.Attempts != 3 { // 1 initial + 2 retries
		t.Errorf("Attempts = %d, want 3", result.Attempts)
	}
	if result.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", result.StatusCode)
	}
	if result.LastError == nil {
		t.Error("LastError should not be nil after exhausting retries")
	}
}

func TestExecuteWithCancelledContext(t *testing.T) {
	r := New(Config{MaxRetries: 3})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := r.Execute(ctx, func() (int, error) {
		return http.StatusOK, nil
	})

	if result.LastError != context.Canceled {
		t.Errorf("LastError = %v, want context.Canceled", result.LastError)
	}
}

func TestExecuteContextCancelledDuringRetry(t *testing.T) {
	r := New(Config{
		MaxRetries:           3,
		InitialDelay:         100 * time.Millisecond,
		RetryableStatusCodes: []int{http.StatusServiceUnavailable},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	callCount := 0
	result := r.Execute(ctx, func() (int, error) {
		callCount++
		return http.StatusServiceUnavailable, nil
	})

	// Should have made at least 1 call but been cancelled during retry wait
	if callCount < 1 {
		t.Error("Should have made at least 1 call")
	}
	if result.LastError == nil {
		t.Error("LastError should not be nil")
	}
}

func TestExecuteWithZeroRetries(t *testing.T) {
	r := New(Config{
		MaxRetries:           0,
		RetryableStatusCodes: []int{http.StatusServiceUnavailable},
	})

	callCount := 0
	result := r.Execute(context.Background(), func() (int, error) {
		callCount++
		return http.StatusServiceUnavailable, nil
	})

	if result.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", result.Attempts)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"connection refused", errors.New("connection refused"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"no such host", errors.New("no such host"), true},
		{"i/o timeout", errors.New("i/o timeout"), true},
		{"temporary failure", errors.New("temporary failure in name resolution"), true},
		{"network unreachable", errors.New("network is unreachable"), true},
		{"connection timed out", errors.New("connection timed out"), true},
		{"EOF", errors.New("EOF"), true},
		{"permanent error", errors.New("invalid request"), false},
		{"unknown error", errors.New("something went wrong"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientError(tt.err); got != tt.expected {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"Connection Refused", "connection refused", true},
		{"CONNECTION REFUSED", "Connection Refused", true},
		{"some error: connection refused", "connection refused", true},
		{"hello world", "HELLO", true},
		{"hello world", "goodbye", false},
		{"short", "longer string", false},
		{"", "test", false},
		{"test", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.substr, func(t *testing.T) {
			if got := containsIgnoreCase(tt.s, tt.substr); got != tt.expected {
				t.Errorf("containsIgnoreCase(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.expected)
			}
		})
	}
}

func TestToLower(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HELLO", "hello"},
		{"Hello World", "hello world"},
		{"hello", "hello"},
		{"123ABC", "123abc"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := toLower(tt.input); got != tt.expected {
				t.Errorf("toLower(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
