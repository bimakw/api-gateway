package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	client   *redis.Client
	requests int
	window   time.Duration
}

type Result struct {
	Allowed    bool
	Remaining  int
	ResetAfter time.Duration
}

func New(client *redis.Client, requestsPerWindow int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		client:   client,
		requests: requestsPerWindow,
		window:   window,
	}
}

// Allow checks if a request is allowed based on the key (IP or API key)
func (rl *RateLimiter) Allow(ctx context.Context, key string) (*Result, error) {
	now := time.Now()
	windowStart := now.Truncate(rl.window)
	windowKey := fmt.Sprintf("ratelimit:%s:%d", key, windowStart.Unix())

	pipe := rl.client.Pipeline()
	incr := pipe.Incr(ctx, windowKey)
	pipe.Expire(ctx, windowKey, rl.window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to execute rate limit check: %w", err)
	}

	count := int(incr.Val())
	remaining := rl.requests - count
	if remaining < 0 {
		remaining = 0
	}

	resetAfter := rl.window - time.Since(windowStart)

	return &Result{
		Allowed:    count <= rl.requests,
		Remaining:  remaining,
		ResetAfter: resetAfter,
	}, nil
}

// AllowWithBurst implements token bucket algorithm for burst handling
func (rl *RateLimiter) AllowWithBurst(ctx context.Context, key string, burstSize int) (*Result, error) {
	now := time.Now()
	bucketKey := fmt.Sprintf("ratelimit:bucket:%s", key)
	lastKey := fmt.Sprintf("ratelimit:last:%s", key)

	// Get current tokens and last update time
	pipe := rl.client.Pipeline()
	tokensCmd := pipe.Get(ctx, bucketKey)
	lastCmd := pipe.Get(ctx, lastKey)
	pipe.Exec(ctx)

	var tokens float64
	var lastUpdate time.Time

	if tokensStr, err := tokensCmd.Result(); err == nil {
		fmt.Sscanf(tokensStr, "%f", &tokens)
	} else {
		tokens = float64(burstSize)
	}

	if lastStr, err := lastCmd.Result(); err == nil {
		var lastUnix int64
		fmt.Sscanf(lastStr, "%d", &lastUnix)
		lastUpdate = time.Unix(0, lastUnix)
	} else {
		lastUpdate = now
	}

	// Calculate tokens to add based on time elapsed
	elapsed := now.Sub(lastUpdate)
	tokensPerSecond := float64(rl.requests) / rl.window.Seconds()
	tokensToAdd := elapsed.Seconds() * tokensPerSecond
	tokens = min(float64(burstSize), tokens+tokensToAdd)

	allowed := tokens >= 1
	if allowed {
		tokens--
	}

	// Update Redis
	pipe = rl.client.Pipeline()
	pipe.Set(ctx, bucketKey, fmt.Sprintf("%f", tokens), rl.window*2)
	pipe.Set(ctx, lastKey, fmt.Sprintf("%d", now.UnixNano()), rl.window*2)
	pipe.Exec(ctx)

	return &Result{
		Allowed:    allowed,
		Remaining:  int(tokens),
		ResetAfter: time.Duration(float64(time.Second) / tokensPerSecond),
	}, nil
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
