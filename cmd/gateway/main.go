package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bimakw/api-gateway/config"
	"github.com/bimakw/api-gateway/internal/apikey"
	"github.com/bimakw/api-gateway/internal/handler"
	"github.com/bimakw/api-gateway/internal/health"
	"github.com/bimakw/api-gateway/internal/circuitbreaker"
	"github.com/bimakw/api-gateway/internal/middleware"
	"github.com/bimakw/api-gateway/internal/proxy"
	"github.com/bimakw/api-gateway/internal/ratelimit"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration
	cfg := config.Load()

	// Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	logger.Info("Connected to Redis")

	// Initialize components
	rateLimiter := ratelimit.New(redisClient, cfg.RateLimit.RequestsPerMinute, cfg.RateLimit.WindowDuration)
	apiKeyMgr := apikey.NewManager(redisClient)

	// Initialize health checker for backend services
	healthChecker := health.NewChecker(
		cfg.Services,
		30*time.Second, // check interval
		5*time.Second,  // timeout
		logger,
	)

	// Start health checker in background
	go healthChecker.Start(ctx)

	// Create circuit breaker config
	cbConfig := circuitbreaker.Config{
		MaxFailures:         cfg.CircuitBreaker.MaxFailures,
		ResetTimeout:        time.Duration(cfg.CircuitBreaker.ResetTimeoutSeconds) * time.Second,
		HalfOpenMaxRequests: cfg.CircuitBreaker.HalfOpenMaxRequests,
		SuccessThreshold:    cfg.CircuitBreaker.SuccessThreshold,
	}

	// Create reverse proxy with circuit breaker
	reverseProxy, err := proxy.New(cfg.Services, cbConfig)
	if err != nil {
		logger.Error("Failed to create reverse proxy", "error", err)
		os.Exit(1)
	}

	handlers := handler.New(cfg, apiKeyMgr, healthChecker, reverseProxy)

	// Create router
	mux := http.NewServeMux()

	// Gateway management endpoints
	mux.HandleFunc("GET /health", handlers.Health)
	mux.HandleFunc("GET /info", handlers.Info)
	mux.HandleFunc("GET /services/health", handlers.ServicesHealth)
	mux.HandleFunc("POST /admin/apikeys", handlers.CreateAPIKey)
	mux.HandleFunc("GET /admin/apikeys", handlers.ListAPIKeys)
	mux.HandleFunc("POST /admin/apikeys/{id}/revoke", handlers.RevokeAPIKey)
	mux.HandleFunc("DELETE /admin/apikeys/{id}", handlers.DeleteAPIKey)

	// Circuit breaker management endpoints
	mux.HandleFunc("GET /admin/circuit-breakers", handlers.GetCircuitBreakers)
	mux.HandleFunc("POST /admin/circuit-breakers/{name}/reset", handlers.ResetCircuitBreaker)
	mux.HandleFunc("POST /admin/circuit-breakers/reset", handlers.ResetAllCircuitBreakers)

	// Proxy all other requests
	mux.Handle("/", reverseProxy)

	// Apply middleware chain
	finalHandler := middleware.Chain(
		mux,
		middleware.Recover(logger),
		middleware.Logger(logger),
		middleware.CORS([]string{"*"}),
		middleware.APIKeyAuth(apiKeyMgr, false), // API key optional
		middleware.RateLimit(rateLimiter, cfg.RateLimit.BurstSize),
	)

	// Create server
	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      finalHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("Starting API Gateway", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}

	// Stop health checker
	healthChecker.Stop()

	// Close Redis connection
	redisClient.Close()

	logger.Info("Server exited")
}
