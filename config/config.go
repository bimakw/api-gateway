package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server         ServerConfig
	Redis          RedisConfig
	RateLimit      RateLimitConfig
	CircuitBreaker CircuitBreakerConfig
	Services       []ServiceConfig
}

type CircuitBreakerConfig struct {
	MaxFailures         int
	ResetTimeoutSeconds int
	HalfOpenMaxRequests int
	SuccessThreshold    int
}

type ServerConfig struct {
	Host string
	Port string
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
}

type RateLimitConfig struct {
	RequestsPerMinute int
	BurstSize         int
	WindowDuration    time.Duration
}

type ServiceConfig struct {
	Name       string
	PathPrefix string
	TargetURL  string
	StripPath  bool
}

func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Host: getEnv("HOST", "0.0.0.0"),
			Port: getEnv("PORT", "8081"),
		},
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnv("REDIS_PORT", "6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		RateLimit: RateLimitConfig{
			RequestsPerMinute: getEnvInt("RATE_LIMIT_RPM", 60),
			BurstSize:         getEnvInt("RATE_LIMIT_BURST", 10),
			WindowDuration:    time.Minute,
		},
		CircuitBreaker: CircuitBreakerConfig{
			MaxFailures:         getEnvInt("CB_MAX_FAILURES", 5),
			ResetTimeoutSeconds: getEnvInt("CB_RESET_TIMEOUT_SECONDS", 30),
			HalfOpenMaxRequests: getEnvInt("CB_HALF_OPEN_MAX_REQUESTS", 3),
			SuccessThreshold:    getEnvInt("CB_SUCCESS_THRESHOLD", 2),
		},
		Services: loadServicesFromEnv(),
	}
}

func loadServicesFromEnv() []ServiceConfig {
	// Default services for demo
	services := []ServiceConfig{
		{
			Name:       "auth-service",
			PathPrefix: "/api/auth",
			TargetURL:  getEnv("AUTH_SERVICE_URL", "http://localhost:8080"),
			StripPath:  false,
		},
		{
			Name:       "user-service",
			PathPrefix: "/api/users",
			TargetURL:  getEnv("USER_SERVICE_URL", "http://localhost:8082"),
			StripPath:  false,
		},
	}
	return services
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
