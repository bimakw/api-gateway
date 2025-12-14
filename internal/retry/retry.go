/*
 * Copyright (c) 2024 Bima Kharisma Wicaksana
 * GitHub: https://github.com/bimakw
 *
 * Licensed under MIT License with Attribution Requirement.
 * See LICENSE file for details.
 */

package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// Config holds retry configuration
type Config struct {
	// MaxRetries is the maximum number of retry attempts (0 = no retries)
	MaxRetries int

	// InitialDelay is the initial delay before first retry
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration

	// Multiplier is the factor to multiply delay by after each retry
	Multiplier float64

	// JitterFactor adds randomness to prevent thundering herd (0.0-1.0)
	JitterFactor float64

	// RetryableStatusCodes are HTTP status codes that should trigger a retry
	RetryableStatusCodes []int
}

// DefaultConfig returns sensible default retry configuration
func DefaultConfig() Config {
	return Config{
		MaxRetries:   3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.1,
		RetryableStatusCodes: []int{
			http.StatusBadGateway,         // 502
			http.StatusServiceUnavailable, // 503
			http.StatusGatewayTimeout,     // 504
		},
	}
}

// Retryer handles retry logic with exponential backoff
type Retryer struct {
	config Config
}

// New creates a new Retryer with the given configuration
func New(cfg Config) *Retryer {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 5 * time.Second
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2.0
	}
	if cfg.JitterFactor < 0 || cfg.JitterFactor > 1 {
		cfg.JitterFactor = 0.1
	}
	if len(cfg.RetryableStatusCodes) == 0 {
		cfg.RetryableStatusCodes = DefaultConfig().RetryableStatusCodes
	}

	return &Retryer{config: cfg}
}

// Result contains the result of a retry operation
type Result struct {
	// Attempts is the total number of attempts made (1 = no retries)
	Attempts int

	// LastError is the last error encountered (nil if successful)
	LastError error

	// StatusCode is the final HTTP status code
	StatusCode int

	// Retried indicates if any retry was attempted
	Retried bool
}

// ShouldRetry determines if a request should be retried based on status code
func (r *Retryer) ShouldRetry(statusCode int) bool {
	for _, code := range r.config.RetryableStatusCodes {
		if code == statusCode {
			return true
		}
	}
	return false
}

// GetDelay calculates the delay before the next retry attempt
func (r *Retryer) GetDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return r.config.InitialDelay
	}

	// Calculate exponential delay
	delay := float64(r.config.InitialDelay) * math.Pow(r.config.Multiplier, float64(attempt))

	// Apply max delay cap
	if delay > float64(r.config.MaxDelay) {
		delay = float64(r.config.MaxDelay)
	}

	// Apply jitter to prevent thundering herd
	if r.config.JitterFactor > 0 {
		jitter := delay * r.config.JitterFactor * (rand.Float64()*2 - 1) // -jitter to +jitter
		delay += jitter
	}

	return time.Duration(delay)
}

// MaxRetries returns the configured maximum retries
func (r *Retryer) MaxRetries() int {
	return r.config.MaxRetries
}

// Execute runs a function with retry logic
// The function should return (statusCode, error)
// Retries are attempted for retryable status codes or transient errors
func (r *Retryer) Execute(ctx context.Context, fn func() (int, error)) Result {
	result := Result{
		Attempts: 0,
	}

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		result.Attempts = attempt + 1

		// Check context before attempting
		if ctx.Err() != nil {
			result.LastError = ctx.Err()
			return result
		}

		// Wait before retry (skip for first attempt)
		if attempt > 0 {
			result.Retried = true
			delay := r.GetDelay(attempt - 1)

			select {
			case <-ctx.Done():
				result.LastError = ctx.Err()
				return result
			case <-time.After(delay):
				// Continue with retry
			}
		}

		// Execute the function
		statusCode, err := fn()
		result.StatusCode = statusCode
		result.LastError = err

		// Success - no error and not a retryable status
		if err == nil && !r.ShouldRetry(statusCode) {
			return result
		}

		// Check if we should retry
		if err != nil {
			// Only retry on transient errors
			if !isTransientError(err) {
				return result
			}
		} else if !r.ShouldRetry(statusCode) {
			// Non-retryable status code
			return result
		}

		// Will retry if we haven't exhausted attempts
	}

	// Exhausted all retries
	if result.LastError == nil {
		result.LastError = errors.New("max retries exceeded")
	}

	return result
}

// isTransientError checks if an error is likely transient and worth retrying
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context errors - don't retry these
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors are generally transient
	errStr := err.Error()
	transientPatterns := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"temporary failure",
		"network is unreachable",
		"connection timed out",
		"EOF",
	}

	for _, pattern := range transientPatterns {
		if containsIgnoreCase(errStr, pattern) {
			return true
		}
	}

	return false
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)

	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

// toLower converts string to lowercase without strings package
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
